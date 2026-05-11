// Package exchange provides exchange interface definitions.
// 거래소 인터페이스 정의 (포지션 관리에 필요한 메서드만 추출)
package exchange

import (
	"context"
	"time"
)

// Ticker 티커 정보
type Ticker struct {
	Symbol    string
	Price     float64
	Bid       float64
	Ask       float64
	Volume24h float64
	Change24h float64
	Timestamp time.Time
}

// OrderbookEntry 호가 항목
type OrderbookEntry struct {
	Price    float64
	Quantity float64
}

// Orderbook 오더북
type Orderbook struct {
	Symbol    string
	Bids      []OrderbookEntry // 매수 호가 (높은 가격순)
	Asks      []OrderbookEntry // 매도 호가 (낮은 가격순)
	Timestamp time.Time        // 거래소 timestamp
	UpdatedAt time.Time        // 우리쪽 수신/갱신 시각 (walk staleness guard용)
}

// BestBid 최우선 매수호가
func (o *Orderbook) BestBid() float64 {
	if len(o.Bids) > 0 {
		return o.Bids[0].Price
	}
	return 0
}

// BestAsk 최우선 매도호가
func (o *Orderbook) BestAsk() float64 {
	if len(o.Asks) > 0 {
		return o.Asks[0].Price
	}
	return 0
}

// Balance 잔고 정보
type Balance struct {
	Currency string
	Free     float64 // 사용 가능
	Used     float64 // 사용 중 (주문)
	Total    float64 // 총 잔고
}

// Position 선물 포지션
type Position struct {
	Symbol       string
	Side         string  // long, short
	Size         float64 // 수량
	EntryPrice   float64 // 진입가
	MarkPrice    float64 // 현재가
	UnrealizedPL float64 // 미실현 손익
	Leverage     int     // 레버리지
}

// DomesticExchange 국내 거래소 인터페이스 (업비트/빗썸)
// 포지션 관리에 필요한 메서드만 포함
type DomesticExchange interface {
	// 연결
	Connect(ctx context.Context) error
	IsConnected() bool

	// 가격 조회
	GetTicker(ctx context.Context, symbol string) (*Ticker, error)
	GetOrderbook(ctx context.Context, symbol string) (*Orderbook, error)

	// 잔고
	GetBalance(ctx context.Context, currency string) (*Balance, error)
	GetAllBalances(ctx context.Context) (map[string]*Balance, error)

	// 마켓 정보
	GetAvailableMarkets() []string
}

// OverseasExchange 해외 거래소 인터페이스 (바이빗/바이낸스)
// 포지션 관리에 필요한 메서드만 포함
type OverseasExchange interface {
	// 연결
	Connect(ctx context.Context) error
	IsConnected() bool

	// 현물 가격 조회
	GetSpotTicker(ctx context.Context, symbol string) (*Ticker, error)
	GetSpotOrderbook(ctx context.Context, symbol string) (*Orderbook, error)

	// 선물 가격 조회
	GetFuturesTicker(ctx context.Context, symbol string) (*Ticker, error)
	GetFuturesOrderbook(ctx context.Context, symbol string) (*Orderbook, error)

	// 잔고
	GetSpotBalance(ctx context.Context, currency string) (*Balance, error)
	GetAllBalances(ctx context.Context) (map[string]*Balance, error)
	GetFuturesBalance(ctx context.Context) (*Balance, error) // USDT 잔고

	// 선물 포지션
	GetPosition(ctx context.Context, symbol string) (*Position, error)
	GetAllPositions(ctx context.Context) ([]*Position, error)

	// 계정 타입 — UTA(Unified Trading Account)면 true
	// GetAllBalances()가 선물 잔고를 포함하므로 GetFuturesBalance() 별도 합산 불필요
	IsUnifiedAccount() bool
}

// MultiWalletProvider 추가 지갑 잔고를 제공하는 거래소
// OverseasExchange 인터페이스와 별도로, type assertion으로 사용
type MultiWalletProvider interface {
	// GetAdditionalBalances 추가 지갑 잔고 조회 (earn, margin, funding, collateral 등)
	// 반환: 지갑타입 -> USD 가치 맵 (예: {"earn": 1500.0, "margin": 200.0})
	GetAdditionalBalances(ctx context.Context) (map[string]float64, error)
}

