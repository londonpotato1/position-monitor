// Package okx provides OKX exchange API client.
// OKX REST API 클라이언트 (포지션 관리용)
package okx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

const (
	BaseURL = "https://www.okx.com"
)

// Client OKX REST API 클라이언트
type Client struct {
	apiKey     string
	apiSecret  string
	passphrase string
	client     *http.Client
	connected  atomic.Bool
}

// NewClient 새 클라이언트 생성
func NewClient(apiKey, apiSecret, passphrase string) *Client {
	return &Client{
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		passphrase: passphrase,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Connect 연결 테스트
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.publicRequest("/api/v5/public/time")
	if err != nil {
		return fmt.Errorf("OKX connection failed: %w", err)
	}
	c.connected.Store(true)
	return nil
}

// IsConnected 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// Disconnect 연결 해제
func (c *Client) Disconnect() {
	c.connected.Store(false)
}

// FormatSymbol 심볼 형식 변환 (base -> BTC-USDT/BTC-USDT-SWAP)
func (c *Client) FormatSymbol(base, marketType string) string {
	if marketType == "futures" {
		return strings.ToUpper(base) + "-USDT-SWAP"
	}
	return strings.ToUpper(base) + "-USDT"
}

// sign 서명 생성 (BASE64(HMAC_SHA256(timestamp + method + path + body)))
func (c *Client) sign(timestamp, method, path, body string) string {
	message := timestamp + method + path + body
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// publicRequest 공개 API 요청
func (c *Client) publicRequest(endpoint string) ([]byte, error) {
	resp, err := c.client.Get(BaseURL + endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// signedRequest 서명된 API 요청
func (c *Client) signedRequest(method, endpoint string, body string) ([]byte, error) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	signature := c.sign(timestamp, method, endpoint, body)

	var req *http.Request
	var err error

	if method == "GET" {
		req, err = http.NewRequest(method, BaseURL+endpoint, nil)
	} else {
		req, err = http.NewRequest(method, BaseURL+endpoint, strings.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("OK-ACCESS-KEY", c.apiKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.passphrase)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// APIResponse OKX 공통 응답
type APIResponse struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// IsSuccess 성공 여부
func (r *APIResponse) IsSuccess() bool {
	return r.Code == "0"
}

// ========== 현물 API ==========

// GetSpotTicker 현물 티커 조회
func (c *Client) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	endpoint := "/api/v5/market/ticker?instId=" + symbol

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var tickers []struct {
		InstID string `json:"instId"`
		Last   string `json:"last"`
		BidPx  string `json:"bidPx"`
		AskPx  string `json:"askPx"`
		Vol24h string `json:"vol24h"`
	}
	if err := json.Unmarshal(resp.Data, &tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("ticker not found: %s", symbol)
	}

	t := tickers[0]
	price, _ := strconv.ParseFloat(t.Last, 64)
	bid, _ := strconv.ParseFloat(t.BidPx, 64)
	ask, _ := strconv.ParseFloat(t.AskPx, 64)
	volume, _ := strconv.ParseFloat(t.Vol24h, 64)

	return &exchange.Ticker{
		Symbol:    t.InstID,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Timestamp: time.Now(),
	}, nil
}

// GetSpotOrderbook 현물 오더북 조회
func (c *Client) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	endpoint := "/api/v5/market/books?instId=" + symbol + "&sz=20"

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var books []struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(resp.Data, &books); err != nil {
		return nil, err
	}

	if len(books) == 0 {
		return nil, fmt.Errorf("orderbook not found: %s", symbol)
	}

	ob := &exchange.Orderbook{
		Symbol:    symbol,
		Timestamp: time.Now(),
	}
	ob.UpdatedAt = time.Now()

	for _, bid := range books[0].Bids {
		if len(bid) >= 2 {
			price, _ := strconv.ParseFloat(bid[0], 64)
			qty, _ := strconv.ParseFloat(bid[1], 64)
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}

	for _, ask := range books[0].Asks {
		if len(ask) >= 2 {
			price, _ := strconv.ParseFloat(ask[0], 64)
			qty, _ := strconv.ParseFloat(ask[1], 64)
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}

	return ob, nil
}

// GetSpotBalance 현물 잔고 조회
func (c *Client) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	endpoint := "/api/v5/account/balance"
	if currency != "" {
		endpoint += "?ccy=" + currency
	}

	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var accounts []struct {
		Details []struct {
			Ccy       string `json:"ccy"`
			AvailBal  string `json:"availBal"`
			FrozenBal string `json:"frozenBal"`
			CashBal   string `json:"cashBal"`
		} `json:"details"`
	}
	if err := json.Unmarshal(resp.Data, &accounts); err != nil {
		return nil, err
	}

	if len(accounts) == 0 {
		return &exchange.Balance{Currency: currency}, nil
	}

	for _, detail := range accounts[0].Details {
		if detail.Ccy == currency {
			free, _ := strconv.ParseFloat(detail.AvailBal, 64)
			frozen, _ := strconv.ParseFloat(detail.FrozenBal, 64)
			total, _ := strconv.ParseFloat(detail.CashBal, 64)
			return &exchange.Balance{
				Currency: detail.Ccy,
				Free:     free,
				Used:     frozen,
				Total:    total,
			}, nil
		}
	}

	return &exchange.Balance{Currency: currency}, nil
}

// GetAllBalances 전체 잔고 조회
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	data, err := c.signedRequest("GET", "/api/v5/account/balance", "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var accounts []struct {
		Details []struct {
			Ccy       string `json:"ccy"`
			AvailBal  string `json:"availBal"`
			FrozenBal string `json:"frozenBal"`
			CashBal   string `json:"cashBal"`
		} `json:"details"`
	}
	if err := json.Unmarshal(resp.Data, &accounts); err != nil {
		return nil, err
	}

	balances := make(map[string]*exchange.Balance)
	if len(accounts) == 0 {
		return balances, nil
	}

	for _, detail := range accounts[0].Details {
		free, _ := strconv.ParseFloat(detail.AvailBal, 64)
		frozen, _ := strconv.ParseFloat(detail.FrozenBal, 64)
		total, _ := strconv.ParseFloat(detail.CashBal, 64)
		if total > 0 {
			balances[detail.Ccy] = &exchange.Balance{
				Currency: detail.Ccy,
				Free:     free,
				Used:     frozen,
				Total:    total,
			}
		}
	}

	return balances, nil
}

// ========== 선물 API ==========

// GetFuturesTicker 선물 티커 조회
func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	endpoint := "/api/v5/market/ticker?instId=" + symbol

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var tickers []struct {
		InstID string `json:"instId"`
		Last   string `json:"last"`
		BidPx  string `json:"bidPx"`
		AskPx  string `json:"askPx"`
		Vol24h string `json:"vol24h"`
	}
	if err := json.Unmarshal(resp.Data, &tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("ticker not found: %s", symbol)
	}

	t := tickers[0]
	price, _ := strconv.ParseFloat(t.Last, 64)
	bid, _ := strconv.ParseFloat(t.BidPx, 64)
	ask, _ := strconv.ParseFloat(t.AskPx, 64)
	volume, _ := strconv.ParseFloat(t.Vol24h, 64)

	return &exchange.Ticker{
		Symbol:    t.InstID,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Timestamp: time.Now(),
	}, nil
}

// GetFuturesOrderbook 선물 오더북 조회
func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	return c.GetSpotOrderbook(ctx, symbol) // OKX는 동일 API 사용
}

// GetFuturesBalance 선물 USDT 잔고 조회
func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	data, err := c.signedRequest("GET", "/api/v5/account/balance?ccy=USDT", "")
	if err != nil {
		return nil, err
	}
	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}
	var accounts []struct {
		Details []struct {
			Ccy       string `json:"ccy"`
			AvailEq   string `json:"availEq"`
			Eq        string `json:"eq"`
			FrozenBal string `json:"frozenBal"`
		} `json:"details"`
	}
	if err := json.Unmarshal(resp.Data, &accounts); err != nil {
		return nil, err
	}
	for _, account := range accounts {
		for _, detail := range account.Details {
			if detail.Ccy == "USDT" {
				free, _ := strconv.ParseFloat(detail.AvailEq, 64)
				total, _ := strconv.ParseFloat(detail.Eq, 64)
				frozen, _ := strconv.ParseFloat(detail.FrozenBal, 64)
				return &exchange.Balance{
					Currency: "USDT",
					Free:     free,
					Used:     frozen,
					Total:    total,
				}, nil
			}
		}
	}
	return &exchange.Balance{Currency: "USDT"}, nil
}

// GetPosition 선물 포지션 조회
func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	endpoint := "/api/v5/account/positions?instId=" + symbol

	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var positions []struct {
		InstID  string `json:"instId"`
		PosSide string `json:"posSide"`
		Pos     string `json:"pos"`
		AvgPx   string `json:"avgPx"`
		MarkPx  string `json:"markPx"`
		Upl     string `json:"upl"`
		Lever   string `json:"lever"`
	}
	if err := json.Unmarshal(resp.Data, &positions); err != nil {
		return nil, err
	}

	if len(positions) == 0 {
		return &exchange.Position{Symbol: symbol}, nil
	}

	p := positions[0]
	size, _ := strconv.ParseFloat(p.Pos, 64)
	entryPrice, _ := strconv.ParseFloat(p.AvgPx, 64)
	markPrice, _ := strconv.ParseFloat(p.MarkPx, 64)
	unrealizedPL, _ := strconv.ParseFloat(p.Upl, 64)
	leverage, _ := strconv.Atoi(p.Lever)

	side := p.PosSide
	if side == "net" {
		if size >= 0 {
			side = "long"
		} else {
			side = "short"
			size = -size
		}
	}

	return &exchange.Position{
		Symbol:       symbol,
		Side:         side,
		Size:         size,
		EntryPrice:   entryPrice,
		MarkPrice:    markPrice,
		UnrealizedPL: unrealizedPL,
		Leverage:     leverage,
	}, nil
}

// GetAllPositions 전체 선물 포지션 조회 (instType=SWAP, pos!=0인 포지션만 반환)
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	endpoint := "/api/v5/account/positions?instType=SWAP"

	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	var raw []struct {
		InstID  string `json:"instId"`
		PosSide string `json:"posSide"`
		Pos     string `json:"pos"`
		AvgPx   string `json:"avgPx"`
		MarkPx  string `json:"markPx"`
		Upl     string `json:"upl"`
		Lever   string `json:"lever"`
	}
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	for _, r := range raw {
		size, _ := strconv.ParseFloat(r.Pos, 64)
		if size == 0 {
			continue
		}

		side := r.PosSide
		if side == "net" {
			if size >= 0 {
				side = "long"
			} else {
				side = "short"
				size = -size
			}
		} else if side == "long" {
			// hedge mode: pos is always positive, side is explicit
			if size < 0 {
				size = -size
			}
		} else if side == "short" {
			// hedge mode: pos is always positive, side is explicit
			if size < 0 {
				size = -size
			}
		}

		entryPrice, _ := strconv.ParseFloat(r.AvgPx, 64)
		markPrice, _ := strconv.ParseFloat(r.MarkPx, 64)
		unrealizedPL, _ := strconv.ParseFloat(r.Upl, 64)
		leverage, _ := strconv.Atoi(r.Lever)

		positions = append(positions, &exchange.Position{
			Symbol:       r.InstID,
			Side:         side,
			Size:         size,
			EntryPrice:   entryPrice,
			MarkPrice:    markPrice,
			UnrealizedPL: unrealizedPL,
			Leverage:     leverage,
		})
	}

	return positions, nil
}

// getUSDTPrice 코인의 USDT 가격 조회 (stablecoin은 1.0 반환)
func (c *Client) getUSDTPrice(coin string) float64 {
	stablecoins := map[string]bool{
		"USDT": true, "USDC": true, "BUSD": true, "TUSD": true,
		"FDUSD": true, "USD1": true, "USDE": true, "DAI": true, "USDP": true,
	}
	if stablecoins[coin] {
		return 1.0
	}

	data, err := c.publicRequest("/api/v5/market/ticker?instId=" + coin + "-USDT")
	if err != nil {
		return 0
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil || !resp.IsSuccess() {
		return 0
	}

	var tickers []struct {
		Last string `json:"last"`
	}
	if err := json.Unmarshal(resp.Data, &tickers); err != nil || len(tickers) == 0 {
		return 0
	}

	price, _ := strconv.ParseFloat(tickers[0].Last, 64)
	return price
}

// GetEarnBalance Earn(Savings) 잔고 조회
func (c *Client) GetEarnBalance() (float64, error) {
	data, err := c.signedRequest("GET", "/api/v5/finance/savings/balance", "")
	if err != nil {
		return 0, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if !resp.IsSuccess() {
		return 0, fmt.Errorf("OKX earn balance error: %s", resp.Msg)
	}

	var items []struct {
		Ccy string `json:"ccy"`
		Amt string `json:"amt"`
	}
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return 0, err
	}

	totalUSD := 0.0
	for _, item := range items {
		amount, _ := strconv.ParseFloat(item.Amt, 64)
		if amount <= 0 {
			continue
		}
		price := c.getUSDTPrice(item.Ccy)
		val := amount * price
		if val > 0.01 {
			totalUSD += val
		}
	}

	return totalUSD, nil
}

// GetFundingBalance Funding 잔고 조회
func (c *Client) GetFundingBalance() (float64, error) {
	data, err := c.signedRequest("GET", "/api/v5/asset/balances", "")
	if err != nil {
		return 0, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if !resp.IsSuccess() {
		return 0, fmt.Errorf("OKX funding balance error: %s", resp.Msg)
	}

	var items []struct {
		Ccy      string `json:"ccy"`
		AvailBal string `json:"availBal"`
	}
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return 0, err
	}

	totalUSD := 0.0
	for _, item := range items {
		amount, _ := strconv.ParseFloat(item.AvailBal, 64)
		if amount <= 0 {
			continue
		}
		price := c.getUSDTPrice(item.Ccy)
		val := amount * price
		if val > 0.01 {
			totalUSD += val
		}
	}

	return totalUSD, nil
}
