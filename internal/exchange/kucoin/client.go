// Package kucoin provides KuCoin exchange API client
package kucoin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

const (
	SpotBaseURL    = "https://api.kucoin.com"
	FuturesBaseURL = "https://api-futures.kucoin.com"
)

// Client KuCoin REST API 클라이언트
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
	_, err := c.publicRequest(SpotBaseURL, "/api/v1/timestamp")
	if err != nil {
		return fmt.Errorf("KuCoin 연결 실패: %w", err)
	}
	c.connected.Store(true)
	return nil
}

// IsConnected 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// sign KuCoin 서명 생성
func (c *Client) sign(timestamp, method, endpoint, body string) string {
	signStr := timestamp + method + endpoint + body
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(signStr))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// signPassphrase Passphrase 서명
func (c *Client) signPassphrase(timestamp string) string {
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(c.passphrase))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// publicRequest 공개 API 요청
func (c *Client) publicRequest(baseURL, endpoint string) ([]byte, error) {
	resp, err := c.client.Get(baseURL + endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return body, nil
	}

	if apiResp.Code != "200000" {
		return nil, fmt.Errorf("API error: %s - %s", apiResp.Code, apiResp.Msg)
	}

	return apiResp.Data, nil
}

// signedRequest 서명된 API 요청
func (c *Client) signedRequest(baseURL, method, endpoint string, params url.Values, jsonBody string) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	requestPath := endpoint
	if len(params) > 0 && method == "GET" {
		requestPath = endpoint + "?" + params.Encode()
	}

	signature := c.sign(timestamp, method, requestPath, jsonBody)
	signedPassphrase := c.signPassphrase(timestamp)

	fullURL := baseURL + requestPath

	var req *http.Request
	var err error

	if method == "GET" || method == "DELETE" {
		req, err = http.NewRequest(method, fullURL, nil)
	} else {
		req, err = http.NewRequest(method, fullURL, strings.NewReader(jsonBody))
		if err == nil && jsonBody != "" {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("KC-API-KEY", c.apiKey)
	req.Header.Set("KC-API-SIGN", signature)
	req.Header.Set("KC-API-TIMESTAMP", timestamp)
	req.Header.Set("KC-API-PASSPHRASE", signedPassphrase)
	req.Header.Set("KC-API-KEY-VERSION", "2")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}

	if apiResp.Code != "200000" {
		return nil, fmt.Errorf("API error: %s - %s", apiResp.Code, apiResp.Msg)
	}

	return apiResp.Data, nil
}

// ========== 현물 API ==========

func (c *Client) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	data, err := c.publicRequest(SpotBaseURL, "/api/v1/market/orderbook/level1?symbol="+symbol)
	if err != nil {
		return nil, err
	}

	var result struct {
		Price   string `json:"price"`
		BestBid string `json:"bestBid"`
		BestAsk string `json:"bestAsk"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	price, _ := strconv.ParseFloat(result.Price, 64)
	bid, _ := strconv.ParseFloat(result.BestBid, 64)
	ask, _ := strconv.ParseFloat(result.BestAsk, 64)

	return &exchange.Ticker{
		Symbol:    symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Timestamp: time.Now(),
	}, nil
}

func (c *Client) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	data, err := c.publicRequest(SpotBaseURL, "/api/v1/market/orderbook/level2_20?symbol="+symbol)
	if err != nil {
		return nil, err
	}

	var result struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	ob := &exchange.Orderbook{Symbol: symbol, Timestamp: time.Now()}
	ob.UpdatedAt = time.Now()
	for _, bid := range result.Bids {
		if len(bid) >= 2 {
			price, _ := strconv.ParseFloat(bid[0], 64)
			qty, _ := strconv.ParseFloat(bid[1], 64)
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}
	for _, ask := range result.Asks {
		if len(ask) >= 2 {
			price, _ := strconv.ParseFloat(ask[0], 64)
			qty, _ := strconv.ParseFloat(ask[1], 64)
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}
	return ob, nil
}

func (c *Client) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/api/v1/accounts", url.Values{"currency": {currency}, "type": {"trade"}}, "")
	if err != nil {
		return nil, err
	}

	var accounts []struct {
		Currency  string `json:"currency"`
		Balance   string `json:"balance"`
		Available string `json:"available"`
		Holds     string `json:"holds"`
	}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}

	for _, a := range accounts {
		if a.Currency == currency {
			total, _ := strconv.ParseFloat(a.Balance, 64)
			free, _ := strconv.ParseFloat(a.Available, 64)
			used, _ := strconv.ParseFloat(a.Holds, 64)
			return &exchange.Balance{Currency: a.Currency, Free: free, Used: used, Total: total}, nil
		}
	}
	return &exchange.Balance{Currency: currency}, nil
}

// ========== 선물 API ==========

func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	data, err := c.publicRequest(FuturesBaseURL, "/api/v1/ticker?symbol="+symbol)
	if err != nil {
		return nil, err
	}

	var result struct {
		Symbol  string `json:"symbol"`
		Price   string `json:"price"`
		BestBid string `json:"bestBidPrice"`
		BestAsk string `json:"bestAskPrice"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	price, _ := strconv.ParseFloat(result.Price, 64)
	bid, _ := strconv.ParseFloat(result.BestBid, 64)
	ask, _ := strconv.ParseFloat(result.BestAsk, 64)

	return &exchange.Ticker{Symbol: symbol, Price: price, Bid: bid, Ask: ask, Timestamp: time.Now()}, nil
}

func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	data, err := c.publicRequest(FuturesBaseURL, "/api/v1/level2/depth20?symbol="+symbol)
	if err != nil {
		return nil, err
	}

	var result struct {
		Bids [][]interface{} `json:"bids"`
		Asks [][]interface{} `json:"asks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	ob := &exchange.Orderbook{Symbol: symbol, Timestamp: time.Now()}
	ob.UpdatedAt = time.Now()
	for _, bid := range result.Bids {
		if len(bid) >= 2 {
			price, _ := strconv.ParseFloat(fmt.Sprint(bid[0]), 64)
			qty, _ := strconv.ParseFloat(fmt.Sprint(bid[1]), 64)
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}
	for _, ask := range result.Asks {
		if len(ask) >= 2 {
			price, _ := strconv.ParseFloat(fmt.Sprint(ask[0]), 64)
			qty, _ := strconv.ParseFloat(fmt.Sprint(ask[1]), 64)
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}
	return ob, nil
}

func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	data, err := c.signedRequest(FuturesBaseURL, "GET", "/api/v1/account-overview", url.Values{"currency": {"USDT"}}, "")
	if err != nil {
		return nil, err
	}

	var result struct {
		AccountEquity    float64 `json:"accountEquity"`
		AvailableBalance float64 `json:"availableBalance"`
		MarginBalance    float64 `json:"marginBalance"`
		Currency         string  `json:"currency"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return &exchange.Balance{
		Currency: result.Currency, Free: result.AvailableBalance,
		Used: result.MarginBalance - result.AvailableBalance, Total: result.AccountEquity,
	}, nil
}

func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	data, err := c.signedRequest(FuturesBaseURL, "GET", "/api/v1/position", url.Values{"symbol": {symbol}}, "")
	if err != nil {
		return nil, err
	}

	var result struct {
		Symbol        string  `json:"symbol"`
		CurrentQty    int64   `json:"currentQty"`
		AvgEntryPrice float64 `json:"avgEntryPrice"`
		MarkPrice     float64 `json:"markPrice"`
		UnrealisedPnl float64 `json:"unrealisedPnl"`
		RealLeverage  float64 `json:"realLeverage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	side := "long"
	size := float64(result.CurrentQty)
	if result.CurrentQty < 0 {
		side = "short"
		size = float64(-result.CurrentQty)
	}

	return &exchange.Position{
		Symbol: result.Symbol, Side: side, Size: size,
		EntryPrice: result.AvgEntryPrice, MarkPrice: result.MarkPrice,
		UnrealizedPL: result.UnrealisedPnl, Leverage: int(result.RealLeverage),
	}, nil
}

// GetAllPositions KuCoin 선물 전체 포지션 조회
// currentQty는 계약 단위(contracts)로 반환됨. 심볼별 계약 크기 변환은 매칭 엔진이 담당.
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	data, err := c.signedRequest(FuturesBaseURL, "GET", "/api/v1/positions", nil, "")
	if err != nil {
		return nil, err
	}

	var items []struct {
		Symbol        string  `json:"symbol"`
		CurrentQty    int64   `json:"currentQty"`
		AvgEntryPrice float64 `json:"avgEntryPrice"`
		MarkPrice     float64 `json:"markPrice"`
		UnrealisedPnl float64 `json:"unrealisedPnl"`
		RealLeverage  float64 `json:"realLeverage"`
		IsOpen        bool    `json:"isOpen"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	for _, item := range items {
		if !item.IsOpen || item.CurrentQty == 0 {
			continue
		}

		side := "long"
		size := float64(item.CurrentQty)
		if item.CurrentQty < 0 {
			side = "short"
			size = float64(-item.CurrentQty)
		}

		positions = append(positions, &exchange.Position{
			Symbol:       item.Symbol,
			Side:         side,
			Size:         size,
			EntryPrice:   item.AvgEntryPrice,
			MarkPrice:    item.MarkPrice,
			UnrealizedPL: item.UnrealisedPnl,
			Leverage:     int(item.RealLeverage),
		})
	}

	return positions, nil
}

func (c *Client) GetAllSpotBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/api/v1/accounts", url.Values{"type": {"trade"}}, "")
	if err != nil {
		return nil, err
	}

	var accounts []struct {
		Currency  string `json:"currency"`
		Balance   string `json:"balance"`
		Available string `json:"available"`
		Holds     string `json:"holds"`
	}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}

	result := make(map[string]*exchange.Balance)
	for _, a := range accounts {
		total, _ := strconv.ParseFloat(a.Balance, 64)
		free, _ := strconv.ParseFloat(a.Available, 64)
		used, _ := strconv.ParseFloat(a.Holds, 64)
		if total > 0 {
			result[a.Currency] = &exchange.Balance{Currency: a.Currency, Free: free, Used: used, Total: total}
		}
	}
	return result, nil
}

// FormatSpotSymbol 현물 심볼 포맷 (BTC -> BTC-USDT)
func FormatSpotSymbol(base string) string {
	return strings.ToUpper(base) + "-USDT"
}

// FormatFuturesSymbol 선물 심볼 포맷 (BTC -> XBTUSDTM, ETH -> ETHUSDTM)
func FormatFuturesSymbol(base string) string {
	base = strings.ToUpper(base)
	if base == "BTC" {
		return "XBTUSDTM"
	}
	return base + "USDTM"
}
