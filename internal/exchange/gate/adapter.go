// Package gate provides Gate.io exchange adapter.
// Gate.io 인터페이스 어댑터 - OverseasExchange 구현
package gate

import (
	"context"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// Adapter Gate.io 어댑터 (OverseasExchange 인터페이스 구현)
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
	return a.client.GetSpotTicker(ctx, symbol+"_USDT")
}

// GetSpotOrderbook 현물 오더북 조회
func (a *Adapter) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetSpotOrderbook(ctx, symbol+"_USDT")
}

// ========== 선물 ==========

// GetFuturesTicker 선물 티커 조회
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetFuturesTicker(ctx, exchange.ResolveFuturesSymbol("gate", symbol)+"_USDT")
}

// GetFuturesOrderbook 선물 오더북 조회
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetFuturesOrderbook(ctx, exchange.ResolveFuturesSymbol("gate", symbol)+"_USDT")
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

// GetAllBalances 전체 잔고 조회
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	return a.client.GetAllBalances(ctx)
}

// ========== 포지션 ==========

// GetAllPositions 전체 선물 포지션 조회
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	return a.client.GetAllPositions(ctx)
}

// GetPosition 포지션 조회
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	return a.client.GetPosition(ctx, exchange.ResolveFuturesSymbol("gate", symbol)+"_USDT")
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }

// GetAdditionalBalances 추가 지갑 잔고 조회 (margin, earn, collateral)
func (a *Adapter) GetAdditionalBalances(ctx context.Context) (map[string]float64, error) {
	result := make(map[string]float64)

	// Cross Margin
	marginUSD, err := a.client.GetCrossMarginBalance()
	if err != nil {
		logger.Warnf("[Gate] Margin 잔고 조회 실패: %v", err)
	} else if marginUSD > 0.01 {
		result["margin"] = marginUSD
	}

	// Earn (Uni Lending)
	earnUSD, err := a.client.GetEarnBalance()
	if err != nil {
		logger.Warnf("[Gate] Earn 잔고 조회 실패: %v", err)
	} else if earnUSD > 0.01 {
		result["earn"] = earnUSD
	}

	// Collateral (담보 - 대출)
	collateralUSD, err := a.client.GetCollateralBalance()
	if err != nil {
		logger.Warnf("[Gate] Collateral 잔고 조회 실패: %v", err)
	} else if collateralUSD > 0.01 || collateralUSD < -0.01 {
		result["collateral"] = collateralUSD
	}

	return result, nil
}
