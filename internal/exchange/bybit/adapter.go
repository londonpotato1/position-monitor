// Package bybit provides Bybit exchange adapter.
// 바이빗 인터페이스 어댑터 - OverseasExchange 구현
package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// Adapter 바이빗 어댑터 (OverseasExchange 인터페이스 구현)
type Adapter struct {
	client    *Client
	connected atomic.Bool
}

// NewAdapter 새 어댑터 생성
func NewAdapter(apiKey, apiSecret string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, apiSecret),
	}
}

// Connect 연결 (잔고 조회로 테스트)
func (a *Adapter) Connect(ctx context.Context) error {
	_, err := a.client.GetWalletBalance("UNIFIED")
	if err != nil {
		return fmt.Errorf("Bybit connection failed: %w", err)
	}
	a.connected.Store(true)

	// 계정 전체 원웨이 모드 설정 시도
	if err := a.client.SwitchPositionMode(0, ""); err != nil {
		logger.Warnf("[Bybit Adapter] position mode switch to one-way failed: %v", err)
	}

	return nil
}

// IsConnected 연결 상태
func (a *Adapter) IsConnected() bool {
	return a.connected.Load()
}

// ========== 현물 ==========

// GetSpotTicker 현물 티커 조회 (REST)
func (a *Adapter) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	symbol = symbol + "USDT"

	data, err := a.client.request("GET", "/v5/market/tickers", map[string]string{
		"category": "spot",
		"symbol":   symbol,
	})
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("ticker query failed: %s", resp.RetMsg)
	}

	var result struct {
		List []struct {
			Symbol    string `json:"symbol"`
			LastPrice string `json:"lastPrice"`
			Bid1Price string `json:"bid1Price"`
			Ask1Price string `json:"ask1Price"`
			Volume24h string `json:"volume24h"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	if len(result.List) == 0 {
		return nil, fmt.Errorf("ticker not found: %s", symbol)
	}

	t := result.List[0]
	price, _ := strconv.ParseFloat(t.LastPrice, 64)
	bid, _ := strconv.ParseFloat(t.Bid1Price, 64)
	ask, _ := strconv.ParseFloat(t.Ask1Price, 64)
	volume, _ := strconv.ParseFloat(t.Volume24h, 64)

	return &exchange.Ticker{
		Symbol:    symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Timestamp: time.Now(),
	}, nil
}

// GetSpotOrderbook 현물 오더북 조회 (REST)
func (a *Adapter) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	symbol = symbol + "USDT"

	data, err := a.client.request("GET", "/v5/market/orderbook", map[string]string{
		"category": "spot",
		"symbol":   symbol,
		"limit":    "10",
	})
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var result struct {
		B [][]string `json:"b"`
		A [][]string `json:"a"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	orderbook := &exchange.Orderbook{
		Symbol:    symbol,
		Timestamp: time.Now(),
	}
	orderbook.UpdatedAt = time.Now()

	for _, bid := range result.B {
		if len(bid) >= 2 {
			price, _ := strconv.ParseFloat(bid[0], 64)
			qty, _ := strconv.ParseFloat(bid[1], 64)
			orderbook.Bids = append(orderbook.Bids, exchange.OrderbookEntry{
				Price: price, Quantity: qty,
			})
		}
	}

	for _, ask := range result.A {
		if len(ask) >= 2 {
			price, _ := strconv.ParseFloat(ask[0], 64)
			qty, _ := strconv.ParseFloat(ask[1], 64)
			orderbook.Asks = append(orderbook.Asks, exchange.OrderbookEntry{
				Price: price, Quantity: qty,
			})
		}
	}

	return orderbook, nil
}

// ========== 선물 ==========

// GetFuturesTicker 선물 티커 조회 (REST)
// 1000x 토큰: 선물 가격을 현물 기준으로 보정 (price /= factor)
func (a *Adapter) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	spotBase := symbol

	futSymbol := ResolveFuturesBase(symbol) + "USDT"

	data, err := a.client.request("GET", "/v5/market/tickers", map[string]string{
		"category": "linear",
		"symbol":   futSymbol,
	})
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("futures ticker query failed: %s", resp.RetMsg)
	}

	var result struct {
		List []struct {
			Symbol    string `json:"symbol"`
			LastPrice string `json:"lastPrice"`
			Bid1Price string `json:"bid1Price"`
			Ask1Price string `json:"ask1Price"`
			Volume24h string `json:"volume24h"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	if len(result.List) == 0 {
		return nil, fmt.Errorf("futures ticker not found: %s", futSymbol)
	}

	t := result.List[0]
	price, _ := strconv.ParseFloat(t.LastPrice, 64)
	bid, _ := strconv.ParseFloat(t.Bid1Price, 64)
	ask, _ := strconv.ParseFloat(t.Ask1Price, 64)
	volume, _ := strconv.ParseFloat(t.Volume24h, 64)

	ticker := &exchange.Ticker{
		Symbol:    futSymbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Timestamp: time.Now(),
	}

	// 1000x 보정: 선물 가격 -> 현물 기준
	factor := exchange.GetScaleFactor("bybit", spotBase)
	if factor > 1 {
		logger.Infof("[1000x] bybit GetFuturesTicker %s: price=%.8f (raw=%.8f, factor=%.0f)", spotBase, ticker.Price/factor, ticker.Price, factor)
		ticker.Price /= factor
		ticker.Bid /= factor
		ticker.Ask /= factor
	}
	return ticker, nil
}

// GetFuturesOrderbook 선물 오더북 조회 (REST)
// 1000x 토큰: 선물 가격을 현물 기준으로 보정
func (a *Adapter) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	spotBase := symbol

	futSymbol := ResolveFuturesBase(symbol) + "USDT"

	data, err := a.client.request("GET", "/v5/market/orderbook", map[string]string{
		"category": "linear",
		"symbol":   futSymbol,
		"limit":    "10",
	})
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var result struct {
		B [][]string `json:"b"`
		A [][]string `json:"a"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	orderbook := &exchange.Orderbook{
		Symbol:    futSymbol,
		Timestamp: time.Now(),
	}
	orderbook.UpdatedAt = time.Now()

	for _, bid := range result.B {
		if len(bid) >= 2 {
			price, _ := strconv.ParseFloat(bid[0], 64)
			qty, _ := strconv.ParseFloat(bid[1], 64)
			orderbook.Bids = append(orderbook.Bids, exchange.OrderbookEntry{
				Price: price, Quantity: qty,
			})
		}
	}

	for _, ask := range result.A {
		if len(ask) >= 2 {
			price, _ := strconv.ParseFloat(ask[0], 64)
			qty, _ := strconv.ParseFloat(ask[1], 64)
			orderbook.Asks = append(orderbook.Asks, exchange.OrderbookEntry{
				Price: price, Quantity: qty,
			})
		}
	}

	// 1000x 보정
	factor := exchange.GetScaleFactor("bybit", spotBase)
	if factor > 1 {
		for i := range orderbook.Bids {
			orderbook.Bids[i].Price /= factor
		}
		for i := range orderbook.Asks {
			orderbook.Asks[i].Price /= factor
		}
	}

	return orderbook, nil
}

// ========== 잔고 ==========

// GetSpotBalance 현물 잔고 조회
func (a *Adapter) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	balances, err := a.client.GetWalletBalance("UNIFIED")
	if err != nil {
		return nil, err
	}

	for _, b := range balances {
		if b.Coin == currency {
			wallet, _ := strconv.ParseFloat(b.WalletBalance, 64)
			free, _ := strconv.ParseFloat(b.Free, 64)
			locked, _ := strconv.ParseFloat(b.Locked, 64)
			// UTA UNIFIED 계정: free 필드가 빈 문자열로 반환될 수 있음
			if free == 0 && wallet > 0 {
				free = wallet - locked
			}
			return &exchange.Balance{
				Currency: currency,
				Free:     free,
				Used:     locked,
				Total:    wallet,
			}, nil
		}
	}

	return nil, nil
}

// GetFuturesBalance 선물 잔고 (USDT) 조회
func (a *Adapter) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	acct, err := a.client.GetAccountBalance("UNIFIED")
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, nil
	}

	totalAvail, _ := strconv.ParseFloat(acct.TotalAvailableBalance, 64)
	totalWallet, _ := strconv.ParseFloat(acct.TotalWalletBalance, 64)

	return &exchange.Balance{
		Currency: "USDT",
		Free:     totalAvail,
		Used:     totalWallet - totalAvail,
		Total:    totalWallet,
	}, nil
}

// GetAllBalances UNIFIED 지갑 + Funding 지갑 통합 반환 (헷지 담보 후보)
func (a *Adapter) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	result, err := a.client.GetAllBalances(ctx)
	if err != nil {
		return nil, err
	}

	// Funding 지갑 코인별 잔고 병합
	fundingCoins, err := a.client.GetFundingWalletCoins(ctx)
	if err != nil {
		logger.Warnf("[Bybit] Funding 지갑 코인 조회 실패 (UNIFIED만 반환): %v", err)
	} else {
		for coin, fb := range fundingCoins {
			if existing, ok := result[coin]; ok {
				existing.Total += fb.Total
				existing.Free += fb.Free
				existing.Used += fb.Used
			} else {
				result[coin] = fb
			}
		}
	}

	return result, nil
}

// ========== 포지션 ==========

// GetPosition 포지션 조회
// 1000x 토큰: 선물 기준 -> 현물 기준으로 변환
func (a *Adapter) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	spotBase := symbol
	futSymbol := ResolveFuturesBase(symbol) + "USDT"

	positions, err := a.client.GetPositions(futSymbol)
	if err != nil {
		return nil, err
	}

	for _, p := range positions {
		size, _ := strconv.ParseFloat(p.Size, 64)
		if size > 0 {
			entry, _ := strconv.ParseFloat(p.AvgPrice, 64)
			pnl, _ := strconv.ParseFloat(p.UnrealisedPnl, 64)
			leverage, _ := strconv.Atoi(p.Leverage)

			side := "long"
			if p.Side == "Sell" {
				side = "short"
			}

			pos := &exchange.Position{
				Symbol:       futSymbol,
				Side:         side,
				Size:         size,
				EntryPrice:   entry,
				UnrealizedPL: pnl,
				Leverage:     leverage,
			}

			// 1000x 보정
			factor := exchange.GetScaleFactor("bybit", spotBase)
			if factor > 1 {
				logger.Infof("[1000x] bybit GetPosition %s: size=%.8f (raw=%.8f), entryPrice=%.8f (raw=%.8f), factor=%.0f",
					spotBase, pos.Size/factor, pos.Size, pos.EntryPrice/factor, pos.EntryPrice, factor)
				pos.Size /= factor
				pos.EntryPrice /= factor
			}
			return pos, nil
		}
	}

	return nil, nil
}

// GetAllPositions 전체 선물 포지션 조회
func (a *Adapter) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	positions, err := a.client.GetAllPositions()
	if err != nil {
		return nil, err
	}

	var result []*exchange.Position
	for _, p := range positions {
		size, _ := strconv.ParseFloat(p.Size, 64)
		if size <= 0 {
			continue
		}
		entry, _ := strconv.ParseFloat(p.AvgPrice, 64)
		mark, _ := strconv.ParseFloat(p.MarkPrice, 64)
		pnl, _ := strconv.ParseFloat(p.UnrealisedPnl, 64)
		leverage, _ := strconv.Atoi(p.Leverage)

		side := "long"
		if p.Side == "Sell" {
			side = "short"
		}

		result = append(result, &exchange.Position{
			Symbol:       p.Symbol,
			Side:         side,
			Size:         size,
			EntryPrice:   entry,
			MarkPrice:    mark,
			UnrealizedPL: pnl,
			Leverage:     leverage,
		})
	}

	return result, nil
}

// IsUnifiedAccount Bybit UTA(Unified Trading Account)는 현물+선물 통합
func (a *Adapter) IsUnifiedAccount() bool { return true }

// StartPrivateWS Bybit V5 private WS 시작 (비블로킹)
// wallet + position 이벤트 수신 시 onUpdate() 호출
func (a *Adapter) StartPrivateWS(ctx context.Context, onUpdate func()) {
	stream := NewPrivateWS(a.client, onUpdate)
	stream.Start(ctx)
}

// GetAdditionalBalances 추가 지갑 잔고 조회 (funding, loan 차감)
func (a *Adapter) GetAdditionalBalances(ctx context.Context) (map[string]float64, error) {
	result := make(map[string]float64)

	// Funding 지갑
	fundingUSD, err := a.client.GetFundingWalletBalance()
	if err != nil {
		logger.Warnf("[Bybit] Funding 잔고 조회 실패: %v", err)
	} else if fundingUSD > 0.01 {
		result["funding"] = fundingUSD
	}

	// Loan 부채 (마이너스 처리)
	loanUSD, err := a.client.GetLoanBalance()
	if err != nil {
		logger.Warnf("[Bybit] Loan 잔고 조회 실패: %v", err)
	} else if loanUSD > 0.01 {
		result["loan"] = -loanUSD
	}

	return result, nil
}
