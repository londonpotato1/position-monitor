// Package bithumb provides Bithumb exchange adapter.
// 빗썸 인터페이스 어댑터 - DomesticExchange 구현
package bithumb

import (
	"context"
	"sync/atomic"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// Adapter 빗썸 어댑터 (DomesticExchange 인터페이스 구현)
type Adapter struct {
	client    *Client
	connected atomic.Bool
}

// NewAdapter 새 어댑터 생성
func NewAdapter(apiKey, apiSecret string) *Adapter {
	lg := logger.NewLogger("Bithumb")
	client := NewClient(apiKey, apiSecret, lg)
	return &Adapter{
		client: client,
	}
}

// Connect 연결
func (a *Adapter) Connect(ctx context.Context) error {
	if err := a.client.Connect(ctx); err != nil {
		return err
	}
	a.connected.Store(true)
	return nil
}

// IsConnected 연결 상태
func (a *Adapter) IsConnected() bool {
	return a.connected.Load()
}

// GetTicker 티커 조회 (REST)
func (a *Adapter) GetTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetTicker(ctx, symbol)
}

// GetOrderbook 오더북 조회 (REST)
func (a *Adapter) GetOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetOrderbook(ctx, symbol)
}

// GetBalance 잔고 조회
func (a *Adapter) GetBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	return a.client.GetBalance(ctx, currency)
}

// GetAllBalances 전체 잔고 조회
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	return a.client.GetAllBalances(ctx)
}

// GetAvailableMarkets 마켓 목록
func (a *Adapter) GetAvailableMarkets() []string {
	return []string{"BTC", "ETH", "XRP", "USDT", "SOL", "DOGE"}
}

// StartAssetStream WS 실시간 잔고 스트림 시작 (비블로킹)
func (a *Adapter) StartAssetStream(ctx context.Context, onUpdate func(map[string]*exchange.Balance)) {
	stream := NewAssetStream(a.client, onUpdate)
	stream.Start(ctx)
}

// compile-time interface check
var _ exchange.DomesticExchange = (*Adapter)(nil)
