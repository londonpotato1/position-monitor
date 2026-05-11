// Package bitget provides Bitget exchange API client
package bitget

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
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

const (
	BaseURL = "https://api.bitget.com"
)

// Client Bitget REST API 클라이언트
type Client struct {
	apiKey     string
	apiSecret  string
	passphrase string
	client     *http.Client
	connected  bool
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
	_, err := c.publicRequest("/api/v2/public/time")
	if err != nil {
		return fmt.Errorf("Bitget 연결 실패: %w", err)
	}
	c.connected = true
	return nil
}

// IsConnected 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.connected
}

// Disconnect 연결 해제
func (c *Client) Disconnect() {
	c.connected = false
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
	return io.ReadAll(resp.Body)
}

// signedRequest 서명된 API 요청
func (c *Client) signedRequest(method, endpoint string, body string) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
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

	req.Header.Set("ACCESS-KEY", c.apiKey)
	req.Header.Set("ACCESS-SIGN", signature)
	req.Header.Set("ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("locale", "en-US")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// APIResponse Bitget 공통 응답
type APIResponse struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (r *APIResponse) IsSuccess() bool {
	return r.Code == "00000"
}

// ========== 현물 API ==========

// GetSpotTicker 현물 티커 조회
func (c *Client) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	endpoint := "/api/v2/spot/market/tickers?symbol=" + symbol

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var tickers []struct {
		Symbol    string `json:"symbol"`
		LastPr    string `json:"lastPr"`
		BidPr     string `json:"bidPr"`
		AskPr     string `json:"askPr"`
		BaseVol   string `json:"baseVolume"`
		Change24h string `json:"change24h"`
	}
	if err := json.Unmarshal(resp.Data, &tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("티커를 찾을 수 없음: %s", symbol)
	}

	t := tickers[0]
	price, _ := strconv.ParseFloat(t.LastPr, 64)
	bid, _ := strconv.ParseFloat(t.BidPr, 64)
	ask, _ := strconv.ParseFloat(t.AskPr, 64)
	volume, _ := strconv.ParseFloat(t.BaseVol, 64)
	change, _ := strconv.ParseFloat(t.Change24h, 64)

	return &exchange.Ticker{
		Symbol:    t.Symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Change24h: change * 100,
		Timestamp: time.Now(),
	}, nil
}

// GetSpotOrderbook 현물 오더북 조회
func (c *Client) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	endpoint := "/api/v2/spot/market/orderbook?symbol=" + symbol + "&limit=20"

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var result struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, err
	}

	ob := &exchange.Orderbook{
		Symbol:    symbol,
		Timestamp: time.Now(),
	}
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

// GetSpotBalance 현물 잔고 조회
func (c *Client) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	endpoint := "/api/v2/spot/account/assets"
	if currency != "" {
		endpoint += "?coin=" + currency
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
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var assets []struct {
		Coin      string `json:"coin"`
		Available string `json:"available"`
		Frozen    string `json:"frozen"`
	}
	if err := json.Unmarshal(resp.Data, &assets); err != nil {
		return nil, err
	}

	for _, a := range assets {
		if a.Coin == currency {
			free, _ := strconv.ParseFloat(a.Available, 64)
			frozen, _ := strconv.ParseFloat(a.Frozen, 64)
			return &exchange.Balance{
				Currency: a.Coin,
				Free:     free,
				Used:     frozen,
				Total:    free + frozen,
			}, nil
		}
	}

	return &exchange.Balance{Currency: currency}, nil
}

// ========== 선물 API ==========

// GetFuturesTicker 선물 티커 조회
func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	endpoint := "/api/v2/mix/market/ticker?productType=USDT-FUTURES&symbol=" + symbol

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var tickers []struct {
		Symbol    string `json:"symbol"`
		LastPr    string `json:"lastPr"`
		BidPr     string `json:"bidPr"`
		AskPr     string `json:"askPr"`
		BaseVol   string `json:"baseVolume"`
		Change24h string `json:"change24h"`
	}
	if err := json.Unmarshal(resp.Data, &tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("티커를 찾을 수 없음: %s", symbol)
	}

	t := tickers[0]
	price, _ := strconv.ParseFloat(t.LastPr, 64)
	bid, _ := strconv.ParseFloat(t.BidPr, 64)
	ask, _ := strconv.ParseFloat(t.AskPr, 64)
	volume, _ := strconv.ParseFloat(t.BaseVol, 64)
	change, _ := strconv.ParseFloat(t.Change24h, 64)

	return &exchange.Ticker{
		Symbol:    t.Symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Change24h: change * 100,
		Timestamp: time.Now(),
	}, nil
}

// GetFuturesOrderbook 선물 오더북 조회
func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	endpoint := "/api/v2/mix/market/depth?productType=USDT-FUTURES&symbol=" + symbol + "&limit=20"

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var result struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, err
	}

	ob := &exchange.Orderbook{
		Symbol:    symbol,
		Timestamp: time.Now(),
	}
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

// GetFuturesBalance 선물 USDT 잔고 조회
func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	endpoint := "/api/v2/mix/account/account?productType=USDT-FUTURES&marginCoin=USDT"

	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var result struct {
		MarginCoin string `json:"marginCoin"`
		Available  string `json:"available"`
		Frozen     string `json:"frozen"`
		Equity     string `json:"equity"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, err
	}

	free, _ := strconv.ParseFloat(result.Available, 64)
	frozen, _ := strconv.ParseFloat(result.Frozen, 64)
	total, _ := strconv.ParseFloat(result.Equity, 64)

	return &exchange.Balance{
		Currency: result.MarginCoin,
		Free:     free,
		Used:     frozen,
		Total:    total,
	}, nil
}

// GetPosition 선물 포지션 조회
func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	endpoint := "/api/v2/mix/position/single-position?productType=USDT-FUTURES&symbol=" + symbol

	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API 오류: %s", resp.Msg)
	}

	var positions []struct {
		Symbol       string `json:"symbol"`
		HoldSide     string `json:"holdSide"`
		Total        string `json:"total"`
		OpenPriceAvg string `json:"openPriceAvg"`
		MarkPrice    string `json:"markPrice"`
		UnrealisedPL string `json:"unrealisedPL"`
		Leverage     string `json:"leverage"`
	}
	if err := json.Unmarshal(resp.Data, &positions); err != nil {
		return nil, err
	}

	if len(positions) == 0 {
		return &exchange.Position{Symbol: symbol}, nil
	}

	p := positions[0]
	size, _ := strconv.ParseFloat(p.Total, 64)
	entryPrice, _ := strconv.ParseFloat(p.OpenPriceAvg, 64)
	markPrice, _ := strconv.ParseFloat(p.MarkPrice, 64)
	unrealizedPL, _ := strconv.ParseFloat(p.UnrealisedPL, 64)
	leverage, _ := strconv.Atoi(p.Leverage)

	return &exchange.Position{
		Symbol:       p.Symbol,
		Side:         p.HoldSide,
		Size:         size,
		EntryPrice:   entryPrice,
		MarkPrice:    markPrice,
		UnrealizedPL: unrealizedPL,
		Leverage:     leverage,
	}, nil
}

// GetAllPositions USDT-M 선물 전체 포지션 조회
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	endpoint := "/api/v2/mix/position/all-position?productType=USDT-FUTURES&marginCoin=USDT"

	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("포지션 조회 실패: %s", resp.Msg)
	}

	var items []struct {
		Symbol       string `json:"symbol"`
		HoldSide     string `json:"holdSide"`
		Total        string `json:"total"`
		OpenPriceAvg string `json:"openPriceAvg"`
		MarkPrice    string `json:"markPrice"`
		UnrealisedPL string `json:"unrealisedPL"`
		Leverage     string `json:"leverage"`
	}
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	for _, item := range items {
		size, _ := strconv.ParseFloat(item.Total, 64)
		if size <= 0 {
			continue
		}
		entryPrice, _ := strconv.ParseFloat(item.OpenPriceAvg, 64)
		markPrice, _ := strconv.ParseFloat(item.MarkPrice, 64)
		unrealizedPL, _ := strconv.ParseFloat(item.UnrealisedPL, 64)
		leverage, _ := strconv.Atoi(item.Leverage)

		positions = append(positions, &exchange.Position{
			Symbol:       item.Symbol,
			Side:         item.HoldSide,
			Size:         size,
			EntryPrice:   entryPrice,
			MarkPrice:    markPrice,
			UnrealizedPL: unrealizedPL,
			Leverage:     leverage,
		})
	}

	return positions, nil
}

// GetAllBalances 전체 잔고 조회
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	result := make(map[string]*exchange.Balance)

	endpoint := "/api/v2/spot/account/assets"
	data, err := c.signedRequest("GET", endpoint, "")
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("잔고 조회 실패: %s", resp.Msg)
	}

	var assets []struct {
		Coin      string `json:"coin"`
		Available string `json:"available"`
		Frozen    string `json:"frozen"`
	}
	if err := json.Unmarshal(resp.Data, &assets); err != nil {
		return nil, err
	}

	for _, a := range assets {
		free, _ := strconv.ParseFloat(a.Available, 64)
		frozen, _ := strconv.ParseFloat(a.Frozen, 64)
		total := free + frozen

		if total > 0 {
			result[a.Coin] = &exchange.Balance{
				Currency: a.Coin,
				Free:     free,
				Used:     frozen,
				Total:    total,
			}
		}
	}

	return result, nil
}
