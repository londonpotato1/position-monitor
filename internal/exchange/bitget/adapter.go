// Package bitget provides Bitget exchange adapter.
// Bitget 인터페이스 어댑터 - OverseasExchange 구현
package bitget

import (
	"context"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

// Adapter Bitget 어댑터 (OverseasExchange 인터페이스 구현)
type Adapter struct {
	client *Client
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

// GetAllPositions 전체 선물 포지션 조회
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	return a.client.GetAllPositions(ctx)
}

// GetPosition 포지션 조회
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	return a.client.GetPosition(ctx, exchange.ResolveFuturesSymbol("bitget", symbol)+"USDT")
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }
