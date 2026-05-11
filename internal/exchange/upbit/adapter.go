// Package upbit provides Upbit exchange adapter.
// 업비트 인터페이스 어댑터 - DomesticExchange 구현
package upbit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

// Adapter 업비트 어댑터 (DomesticExchange 인터페이스 구현)
type Adapter struct {
	client    *Client
	connected atomic.Bool
}

// NewAdapter 새 어댑터 생성
func NewAdapter(accessKey, secretKey string) *Adapter {
	client := NewClient(accessKey, secretKey)
	return &Adapter{
		client: client,
	}
}

// Connect 연결
func (a *Adapter) Connect(ctx context.Context) error {
	// 연결 테스트 (계좌 조회)
	_, err := a.client.GetAccounts()
	if err != nil {
		return fmt.Errorf("업비트 연결 실패: %w", err)
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
	market := "KRW-" + symbol
	tickers, err := a.client.GetTicker(market)
	if err != nil {
		return nil, err
	}
	if len(tickers) == 0 {
		return nil, fmt.Errorf("티커 없음: %s", symbol)
	}

	t := tickers[0]
	return &exchange.Ticker{
		Symbol:    symbol,
		Price:     t.TradePrice,
		Volume24h: t.AccTradeVolume24h,
		Change24h: t.SignedChangeRate * 100,
		Timestamp: time.UnixMilli(t.Timestamp),
	}, nil
}

// GetOrderbook 오더북 조회 (REST)
func (a *Adapter) GetOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	market := "KRW-" + symbol

	data, err := a.client.request("GET", "/orderbook", map[string]string{"markets": market}, nil)
	if err != nil {
		return nil, err
	}

	var result []struct {
		Market         string `json:"market"`
		OrderbookUnits []struct {
			AskPrice float64 `json:"ask_price"`
			BidPrice float64 `json:"bid_price"`
			AskSize  float64 `json:"ask_size"`
			BidSize  float64 `json:"bid_size"`
		} `json:"orderbook_units"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	if len(result) == 0 || len(result[0].OrderbookUnits) == 0 {
		return nil, fmt.Errorf("오더북 데이터 없음")
	}

	orderbook := &exchange.Orderbook{
		Symbol:    symbol,
		Timestamp: time.Now(),
	}
	orderbook.UpdatedAt = time.Now()

	for _, unit := range result[0].OrderbookUnits {
		orderbook.Bids = append(orderbook.Bids, exchange.OrderbookEntry{
			Price:    unit.BidPrice,
			Quantity: unit.BidSize,
		})
		orderbook.Asks = append(orderbook.Asks, exchange.OrderbookEntry{
			Price:    unit.AskPrice,
			Quantity: unit.AskSize,
		})
	}

	return orderbook, nil
}

// GetBalance 잔고 조회
func (a *Adapter) GetBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	accounts, err := a.client.GetAccounts()
	if err != nil {
		return nil, err
	}

	for _, acc := range accounts {
		if acc.Currency == currency {
			balance, err := strconv.ParseFloat(acc.Balance, 64)
			if err != nil {
				log.Printf("[WARN] Upbit ParseFloat failed for balance %q (currency=%s): %v", acc.Balance, currency, err)
			}
			locked, err := strconv.ParseFloat(acc.Locked, 64)
			if err != nil {
				log.Printf("[WARN] Upbit ParseFloat failed for locked %q (currency=%s): %v", acc.Locked, currency, err)
			}
			return &exchange.Balance{
				Currency: currency,
				Free:     balance,
				Used:     locked,
				Total:    balance + locked,
			}, nil
		}
	}

	return &exchange.Balance{Currency: currency}, nil
}

// GetAllBalances 전체 잔고 조회
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	accounts, err := a.client.GetAccounts()
	if err != nil {
		return nil, err
	}

	balances := make(map[string]*exchange.Balance)
	for _, acc := range accounts {
		balance, err := strconv.ParseFloat(acc.Balance, 64)
		if err != nil {
			log.Printf("[WARN] Upbit ParseFloat failed for balance %q (currency=%s): %v", acc.Balance, acc.Currency, err)
		}
		locked, err := strconv.ParseFloat(acc.Locked, 64)
		if err != nil {
			log.Printf("[WARN] Upbit ParseFloat failed for locked %q (currency=%s): %v", acc.Locked, acc.Currency, err)
		}
		balances[acc.Currency] = &exchange.Balance{
			Currency: acc.Currency,
			Free:     balance,
			Used:     locked,
			Total:    balance + locked,
		}
	}

	return balances, nil
}

// GetAvailableMarkets 마켓 목록
func (a *Adapter) GetAvailableMarkets() []string {
	return []string{"BTC", "ETH", "XRP", "USDT"}
}

// StartAssetStream WS 실시간 잔고 스트림 시작 (비블로킹)
// 연결 실패 시 로그만 남기고 REST 폴링 계속
func (a *Adapter) StartAssetStream(ctx context.Context, onUpdate func(map[string]*exchange.Balance)) {
	stream := NewAssetStream(a.client, onUpdate)
	stream.Start(ctx)
}

// compile-time interface check
var _ exchange.DomesticExchange = (*Adapter)(nil)
