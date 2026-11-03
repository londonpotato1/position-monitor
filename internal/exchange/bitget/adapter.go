// Package bitget provides Bitget exchange adapter.
// Bitget 인터페이스 어댑터 - OverseasExchange 구현
package bitget

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

// Adapter Bitget 어댑터 (OverseasExchange 인터페이스 구현)
type Adapter struct {
	client *Client

	// TradFi 선물 심볼 캐시 — GetAllPositions 가 Position.TradFi 설정 (주식 선물을 같은 티커 코인과 짝짓지 않게)
	tradFi exchange.TradFiSet
}

// NewAdapter 새 어댑터 생성
func NewAdapter(apiKey, apiSecret, passphrase string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, apiSecret, passphrase),
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
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetFuturesTicker(ctx, exchange.ResolveFuturesSymbol("bitget", symbol)+"USDT")
}

// GetFuturesOrderbook 선물 오더북 조회
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetFuturesOrderbook(ctx, exchange.ResolveFuturesSymbol("bitget", symbol)+"USDT")
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

// GetAllPositions 전체 선물 포지션 조회 (TradFi 선물 표시 포함)
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	positions, err := a.client.GetAllPositions(ctx)
	if err != nil {
		return nil, err
	}
	a.tradFi.Mark("bitget", positions, a.loadTradFi)
	return positions, nil
}

// loadTradFi USDT-FUTURES 상품정보에서 isRwa 가 "YES"(주식·원자재·외환 등 RWA)인 symbol 수집
// docs: symbol 을 생략하면 한 번에 전체 반환 (페이지네이션 없음)
func (a *Adapter) loadTradFi() (map[string]bool, error) {
	data, err := a.client.publicRequest("/api/v2/mix/market/contracts?productType=USDT-FUTURES")
	if err != nil {
		return nil, err
	}
	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("contracts code=%s msg=%s", resp.Code, resp.Msg)
	}
	var contracts []struct {
		Symbol string `json:"symbol"`
		IsRwa  string `json:"isRwa"`
	}
	if err := json.Unmarshal(resp.Data, &contracts); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, c := range contracts {
		if c.IsRwa == "YES" {
			set[c.Symbol] = true
		}
	}
	return set, nil
}

// GetPosition 포지션 조회
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	return a.client.GetPosition(ctx, exchange.ResolveFuturesSymbol("bitget", symbol)+"USDT")
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }
