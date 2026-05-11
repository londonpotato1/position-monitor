// Package mexc provides MEXC exchange adapter.
// MEXC 인터페이스 어댑터 - OverseasExchange 구현
package mexc

import (
	"context"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

type Adapter struct {
	client *Client
}

func NewAdapter(apiKey, apiSecret string) *Adapter {
	return &Adapter{client: NewClient(apiKey, apiSecret)}
}

func (a *Adapter) Connect(ctx context.Context) error    { return a.client.Connect(ctx) }
func (a *Adapter) IsConnected() bool                    { return a.client.IsConnected() }

func (a *Adapter) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetSpotTicker(ctx, FormatSpotSymbol(symbol))
}
func (a *Adapter) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetSpotOrderbook(ctx, FormatSpotSymbol(symbol))
}
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetFuturesTicker(ctx, FormatFuturesSymbol(symbol))
}
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetFuturesOrderbook(ctx, FormatFuturesSymbol(symbol))
}
func (a *Adapter) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	return a.client.GetSpotBalance(ctx, currency)
}
func (a *Adapter) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	return a.client.GetFuturesBalance(ctx)
}
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	return a.client.GetAllSpotBalances(ctx)
}

func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	return a.client.GetAllPositions(ctx)
}

func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	return a.client.GetPosition(ctx, FormatFuturesSymbol(symbol))
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }
