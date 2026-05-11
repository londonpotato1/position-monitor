// Package binance provides Binance exchange adapter.
// 바이낸스 인터페이스 어댑터 - OverseasExchange 구현
package binance

import (
	"context"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// Adapter 바이낸스 어댑터 (OverseasExchange 인터페이스 구현)
type Adapter struct {
	client *Client
}

// NewAdapter 새 어댑터 생성
func NewAdapter(apiKey, apiSecret string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, apiSecret),
	}
}

// Connect 연결
func (a *Adapter) Connect(ctx context.Context) error {
	return a.client.Connect(ctx)
}

// IsConnected 연결 상태
func (a *Adapter) IsConnected() bool {
	return a.client.IsConnected()
}

// ========== 현물 ==========

// GetSpotTicker 현물 티커 조회
func (a *Adapter) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetSpotTicker(ctx, symbol+"USDT")
}

// GetSpotOrderbook 현물 오더북 조회
func (a *Adapter) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetSpotOrderbook(ctx, symbol+"USDT")
}

// ========== 선물 ==========

// GetFuturesTicker 선물 티커 조회
// 1000x 토큰: 선물 가격을 현물 기준으로 보정 (price /= factor)
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	ticker, err := a.client.GetFuturesTicker(ctx, ResolveFuturesBase(symbol)+"USDT")
	if err != nil {
		return nil, err
	}

	// 1000x 보정: 선물 가격 -> 현물 기준
	factor := exchange.GetScaleFactor("binance", symbol)
	if factor > 1 {
		logger.Infof("[1000x] binance GetFuturesTicker %s: price=%.8f (raw=%.8f, factor=%.0f)", symbol, ticker.Price/factor, ticker.Price, factor)
		ticker.Price /= factor
		ticker.Bid /= factor
		ticker.Ask /= factor
	}
	return ticker, nil
}

// GetFuturesOrderbook 선물 오더북 조회
// 1000x 토큰: 선물 가격을 현물 기준으로 보정 (price /= factor)
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	ob, err := a.client.GetFuturesOrderbook(ctx, ResolveFuturesBase(symbol)+"USDT")
	if err != nil {
		return nil, err
	}

	// 1000x 보정: 선물 가격 -> 현물 기준
	factor := exchange.GetScaleFactor("binance", symbol)
	if factor > 1 {
		for i := range ob.Bids {
			ob.Bids[i].Price /= factor
		}
		for i := range ob.Asks {
			ob.Asks[i].Price /= factor
		}
	}
	return ob, nil
}

// ========== 잔고 ==========

// GetSpotBalance 현물 잔고 조회
func (a *Adapter) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	return a.client.GetSpotBalance(ctx, currency)
}

// GetFuturesBalance 선물 잔고 (USDT) 조회
func (a *Adapter) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	return a.client.GetFuturesBalance(ctx)
}

// GetAllBalances Spot + Flexible Earn + Cross Margin netAsset 통합 반환 (헷지 담보 후보)
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	result, err := a.client.GetAllBalances(ctx)
	if err != nil {
		return nil, err
	}

	// Flexible Earn 코인별 잔고 병합
	earnCoins, err := a.client.GetEarnCoins(ctx)
	if err != nil {
		logger.Warnf("[Binance] Flexible Earn 코인 조회 실패 (Spot만 반환): %v", err)
	} else {
		for coin, eb := range earnCoins {
			if existing, ok := result[coin]; ok {
				existing.Total += eb.Total
				existing.Free += eb.Free
				existing.Used += eb.Used
			} else {
				result[coin] = eb
			}
		}
	}

	// Cross Margin netAsset 병합
	marginCoins, err := a.client.GetMarginCoins(ctx)
	if err != nil {
		logger.Warnf("[Binance] Cross Margin 코인 조회 실패 (Spot+Earn만 반환): %v", err)
	} else {
		for coin, mb := range marginCoins {
			if existing, ok := result[coin]; ok {
				existing.Total += mb.Total
				existing.Free += mb.Free
				existing.Used += mb.Used
			} else {
				result[coin] = mb
			}
		}
	}

	return result, nil
}

// ========== 포지션 ==========

// GetAllPositions 전체 선물 포지션 조회
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	return a.client.GetAllPositions(ctx)
}

// GetPosition 포지션 조회
// 1000x 토큰: 선물 기준 -> 현물 기준으로 변환 (size *= factor, entryPrice /= factor)
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	pos, err := a.client.GetPosition(ctx, ResolveFuturesBase(symbol)+"USDT")
	if err != nil {
		return nil, err
	}
	if pos == nil || pos.Size == 0 {
		return pos, nil
	}

	// 1000x 보정: 선물 기준 -> 현물 기준
	factor := exchange.GetScaleFactor("binance", symbol)
	if factor > 1 {
		logger.Infof("[1000x] binance GetPosition %s: size=%.8f (raw=%.8f), entryPrice=%.8f (raw=%.8f), factor=%.0f",
			symbol, pos.Size/factor, pos.Size, pos.EntryPrice/factor, pos.EntryPrice, factor)
		pos.Size /= factor
		pos.EntryPrice /= factor
		pos.MarkPrice /= factor
	}
	return pos, nil
}

// StartUserStream Binance Futures User Data Stream 시작 (비블로킹)
// ACCOUNT_UPDATE 이벤트 수신 시 onUpdate() 호출
func (a *Adapter) StartUserStream(ctx context.Context, onUpdate func()) {
	stream := NewUserStream(a.client, onUpdate)
	stream.Start(ctx)
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }

// GetAdditionalBalances 추가 지갑 잔고 조회 (earn, margin, funding)
func (a *Adapter) GetAdditionalBalances(ctx context.Context) (map[string]float64, error) {
	result := make(map[string]float64)

	// Earn (Flexible + Locked)
	earnUSD, err := a.client.GetEarnBalance()
	if err != nil {
		logger.Warnf("[Binance] Earn 잔고 조회 실패: %v", err)
	} else if earnUSD > 0.01 {
		result["earn"] = earnUSD
	}

	// Cross Margin
	marginUSD, err := a.client.GetMarginBalance()
	if err != nil {
		logger.Warnf("[Binance] Margin 잔고 조회 실패: %v", err)
	} else if marginUSD > 0.01 {
		result["margin"] = marginUSD
	}

	// Funding
	fundingUSD, err := a.client.GetFundingBalance()
	if err != nil {
		logger.Warnf("[Binance] Funding 잔고 조회 실패: %v", err)
	} else if fundingUSD > 0.01 {
		result["funding"] = fundingUSD
	}

	return result, nil
}
