// Package lighter provides Lighter DEX adapter.
// Lighter 인터페이스 어댑터 - OverseasExchange 구현 (DEX - 선물만 지원)
package lighter

import (
	"context"
	"fmt"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

// Adapter Lighter 어댑터 (OverseasExchange 인터페이스 구현)
// 주의: Lighter는 DEX로 선물(perpetual) 거래만 지원
type Adapter struct {
	client *Client
}

// NewAdapter 새 어댑터 생성
func NewAdapter(privateKey string, accountIndex int, testnet bool) (*Adapter, error) {
	client, err := NewClient(privateKey, accountIndex, testnet)
	if err != nil {
		return nil, err
	}
	return &Adapter{
		client: client,
	}, nil
}

// Connect 연결
func (a *Adapter) Connect(ctx context.Context) error {
	return a.client.Connect(ctx)
}

// IsConnected 연결 상태
func (a *Adapter) IsConnected() bool {
	return a.client.IsConnected()
}

// ========== 현물 (미지원) ==========

// GetSpotTicker 현물 티커 조회 - Lighter는 현물 미지원
func (a *Adapter) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return nil, fmt.Errorf("spot trading not supported on Lighter DEX")
}

// GetSpotOrderbook 현물 오더북 조회 - Lighter는 현물 미지원
func (a *Adapter) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return nil, fmt.Errorf("spot trading not supported on Lighter DEX")
}

// GetSpotBalance 현물 잔고 조회 - Lighter는 현물 미지원
func (a *Adapter) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	return nil, fmt.Errorf("spot trading not supported on Lighter DEX")
}

// ========== 선물 ==========

// GetFuturesTicker 선물 티커 조회
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	return a.client.GetFuturesTicker(ctx, symbol)
}

// GetFuturesOrderbook 선물 오더북 조회
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return a.client.GetFuturesOrderbook(ctx, symbol)
}

// GetFuturesBalance 선물 잔고 (USDC) 조회
func (a *Adapter) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	return a.client.GetFuturesBalance(ctx)
}

// GetAllBalances 전체 잔고 조회 (Lighter는 선물 전용이므로 빈 맵 반환)
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	return map[string]*exchange.Balance{}, nil
}

// GetAllPositions 전체 선물 포지션 조회
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	return a.client.GetAllPositions(ctx)
}

// GetPosition 포지션 조회
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	return a.client.GetPosition(ctx, symbol)
}

// IsUnifiedAccount 이 거래소는 통합 계정이 아님
func (a *Adapter) IsUnifiedAccount() bool { return false }
