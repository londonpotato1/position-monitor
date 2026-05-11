// Package mexc provides MEXC exchange API client
package mexc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	SpotBaseURL    = "https://api.mexc.com"
	FuturesBaseURL = "https://contract.mexc.com"
)

// Client MEXC REST API 클라이언트
type Client struct {
	apiKey    string
	apiSecret string
	client    *http.Client
	connected atomic.Bool
}

func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		apiKey: apiKey, apiSecret: apiSecret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Connect(ctx context.Context) error {
	_, err := c.publicRequest(SpotBaseURL, "/api/v3/time")
	if err != nil {
		return fmt.Errorf("MEXC 연결 실패: %w", err)
	}
	c.connected.Store(true)
	return nil
}

func (c *Client) IsConnected() bool { return c.connected.Load() }

func (c *Client) sign(queryString string) string {
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(queryString))
	return hex.EncodeToString(h.Sum(nil))
}

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
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	var apiErr struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
		return nil, fmt.Errorf("API error: code=%d msg=%s", apiErr.Code, apiErr.Msg)
	}
	return body, nil
}

func (c *Client) signedRequest(baseURL, method, endpoint string, params url.Values) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	params.Set("timestamp", timestamp)
	params.Set("recvWindow", "5000")

	queryString := params.Encode()
	signature := c.sign(queryString)
	queryString += "&signature=" + signature

	var req *http.Request
	var err error

	if method == "GET" || method == "DELETE" {
		req, err = http.NewRequest(method, baseURL+endpoint+"?"+queryString, nil)
	} else {
		req, err = http.NewRequest(method, baseURL+endpoint, strings.NewReader(queryString))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MEXC-APIKEY", c.apiKey)

	resp, err := c.client.Do(req)
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
	var apiErr struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
		return nil, fmt.Errorf("API error: code=%d msg=%s", apiErr.Code, apiErr.Msg)
	}
	return body, nil
}

func (c *Client) signedFuturesRequest(method, endpoint string, params url.Values, jsonBody string) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	var signStr string
	if jsonBody != "" {
		signStr = c.apiKey + timestamp + jsonBody
	} else {
		signStr = c.apiKey + timestamp
		if len(params) > 0 {
			signStr += params.Encode()
		}
	}
	signature := c.sign(signStr)

	fullURL := FuturesBaseURL + endpoint
	if method == "GET" && len(params) > 0 {
		fullURL += "?" + params.Encode()
	}

	var req *http.Request
	var err error
	if method == "GET" || method == "DELETE" {
		req, err = http.NewRequest(method, fullURL, nil)
	} else {
		if jsonBody != "" {
			req, err = http.NewRequest(method, fullURL, strings.NewReader(jsonBody))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req, err = http.NewRequest(method, fullURL, strings.NewReader(params.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("ApiKey", c.apiKey)
	req.Header.Set("Request-Time", timestamp)
	req.Header.Set("Signature", signature)

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
		Success bool            `json:"success"`
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
	if !apiResp.Success && apiResp.Code != 0 {
		return nil, fmt.Errorf("API error %d: %s", apiResp.Code, apiResp.Message)
	}
	return apiResp.Data, nil
}

// ========== 현물 ==========

func (c *Client) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	data, err := c.publicRequest(SpotBaseURL, "/api/v3/ticker/24hr?symbol="+symbol)
	if err != nil {
		return nil, err
	}
	var result struct {
		Symbol    string `json:"symbol"`
		LastPrice string `json:"lastPrice"`
		BidPrice  string `json:"bidPrice"`
		AskPrice  string `json:"askPrice"`
		Volume    string `json:"volume"`
		Change    string `json:"priceChangePercent"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	price, _ := strconv.ParseFloat(result.LastPrice, 64)
	bid, _ := strconv.ParseFloat(result.BidPrice, 64)
	ask, _ := strconv.ParseFloat(result.AskPrice, 64)
	volume, _ := strconv.ParseFloat(result.Volume, 64)
	change, _ := strconv.ParseFloat(result.Change, 64)
	return &exchange.Ticker{
		Symbol: result.Symbol, Price: price, Bid: bid, Ask: ask,
		Volume24h: volume, Change24h: change, Timestamp: time.Now(),
	}, nil
}

func (c *Client) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	data, err := c.publicRequest(SpotBaseURL, "/api/v3/depth?symbol="+symbol+"&limit=20")
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
			p, _ := strconv.ParseFloat(bid[0], 64)
			q, _ := strconv.ParseFloat(bid[1], 64)
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: p, Quantity: q})
		}
	}
	for _, ask := range result.Asks {
		if len(ask) >= 2 {
			p, _ := strconv.ParseFloat(ask[0], 64)
			q, _ := strconv.ParseFloat(ask[1], 64)
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: p, Quantity: q})
		}
	}
	return ob, nil
}

func (c *Client) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/api/v3/account", url.Values{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	for _, b := range result.Balances {
		if b.Asset == currency {
			free, _ := strconv.ParseFloat(b.Free, 64)
			locked, _ := strconv.ParseFloat(b.Locked, 64)
			return &exchange.Balance{Currency: b.Asset, Free: free, Used: locked, Total: free + locked}, nil
		}
	}
	return &exchange.Balance{Currency: currency}, nil
}

// ========== 선물 ==========

func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	data, err := c.publicRequest(FuturesBaseURL, "/api/v1/contract/ticker?symbol="+symbol)
	if err != nil {
		return nil, err
	}
	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Symbol    string  `json:"symbol"`
			LastPrice float64 `json:"lastPrice"`
			Bid1      float64 `json:"bid1"`
			Ask1      float64 `json:"ask1"`
			Volume24  float64 `json:"volume24"`
			RiseFall  float64 `json:"riseFallRate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("MEXC 선물 티커 조회 실패")
	}
	return &exchange.Ticker{
		Symbol: apiResp.Data.Symbol, Price: apiResp.Data.LastPrice,
		Bid: apiResp.Data.Bid1, Ask: apiResp.Data.Ask1,
		Volume24h: apiResp.Data.Volume24, Change24h: apiResp.Data.RiseFall * 100,
		Timestamp: time.Now(),
	}, nil
}

func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	data, err := c.publicRequest(FuturesBaseURL, "/api/v1/contract/depth/"+symbol+"?limit=20")
	if err != nil {
		return nil, err
	}
	var apiResp struct {
		Success bool `json:"success"`
		Data    struct {
			Bids [][]float64 `json:"bids"`
			Asks [][]float64 `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("MEXC 선물 오더북 조회 실패")
	}
	ob := &exchange.Orderbook{Symbol: symbol, Timestamp: time.Now()}
	ob.UpdatedAt = time.Now()
	for _, bid := range apiResp.Data.Bids {
		if len(bid) >= 2 {
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: bid[0], Quantity: bid[1]})
		}
	}
	for _, ask := range apiResp.Data.Asks {
		if len(ask) >= 2 {
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: ask[0], Quantity: ask[1]})
		}
	}
	return ob, nil
}

func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	data, err := c.signedFuturesRequest("GET", "/api/v1/private/account/assets", url.Values{}, "")
	if err != nil {
		return nil, err
	}
	var assets []struct {
		Currency         string  `json:"currency"`
		PositionMargin   float64 `json:"positionMargin"`
		AvailableBalance float64 `json:"availableBalance"`
		CashBalance      float64 `json:"cashBalance"`
	}
	if err := json.Unmarshal(data, &assets); err != nil {
		return nil, err
	}
	for _, a := range assets {
		if a.Currency == "USDT" {
			return &exchange.Balance{Currency: "USDT", Free: a.AvailableBalance, Used: a.PositionMargin, Total: a.CashBalance}, nil
		}
	}
	return &exchange.Balance{Currency: "USDT"}, nil
}

func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	data, err := c.signedFuturesRequest("GET", "/api/v1/private/position/open_positions", params, "")
	if err != nil {
		return nil, err
	}
	var positions []struct {
		Symbol        string  `json:"symbol"`
		HoldVol       float64 `json:"holdVol"`
		OpenAvgPrice  float64 `json:"openAvgPrice"`
		MarkPrice     float64 `json:"fairPrice"`
		UnrealisedPnl float64 `json:"unrealisedPnl"`
		Leverage      int     `json:"leverage"`
		PositionType  int     `json:"positionType"`
	}
	if err := json.Unmarshal(data, &positions); err != nil {
		return nil, err
	}
	for _, p := range positions {
		if p.Symbol == symbol && p.HoldVol > 0 {
			side := "long"
			if p.PositionType == 2 {
				side = "short"
			}
			return &exchange.Position{
				Symbol: p.Symbol, Side: side, Size: p.HoldVol,
				EntryPrice: p.OpenAvgPrice, MarkPrice: p.MarkPrice,
				UnrealizedPL: p.UnrealisedPnl, Leverage: p.Leverage,
			}, nil
		}
	}
	return &exchange.Position{Symbol: symbol}, nil
}

func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	data, err := c.signedFuturesRequest("GET", "/api/v1/private/position/open_positions", url.Values{}, "")
	if err != nil {
		return nil, err
	}
	var positions []struct {
		Symbol        string  `json:"symbol"`
		HoldVol       float64 `json:"holdVol"`
		OpenAvgPrice  float64 `json:"openAvgPrice"`
		MarkPrice     float64 `json:"fairPrice"`
		UnrealisedPnl float64 `json:"unrealisedPnl"`
		Leverage      int     `json:"leverage"`
		PositionType  int     `json:"positionType"`
	}
	if err := json.Unmarshal(data, &positions); err != nil {
		return nil, err
	}
	var result []*exchange.Position
	for _, p := range positions {
		if p.HoldVol <= 0 {
			continue
		}
		side := "long"
		if p.PositionType == 2 {
			side = "short"
		}
		result = append(result, &exchange.Position{
			Symbol:       p.Symbol,
			Side:         side,
			Size:         p.HoldVol,
			EntryPrice:   p.OpenAvgPrice,
			MarkPrice:    p.MarkPrice,
			UnrealizedPL: p.UnrealisedPnl,
			Leverage:     p.Leverage,
		})
	}
	return result, nil
}

func (c *Client) GetAllSpotBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/api/v3/account", url.Values{})
	if err != nil {
		return nil, err
	}
	var account struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, err
	}
	result := make(map[string]*exchange.Balance)
	for _, b := range account.Balances {
		free, _ := strconv.ParseFloat(b.Free, 64)
		locked, _ := strconv.ParseFloat(b.Locked, 64)
		total := free + locked
		if total > 0 {
			result[b.Asset] = &exchange.Balance{Currency: b.Asset, Free: free, Used: locked, Total: total}
		}
	}
	return result, nil
}

func FormatSpotSymbol(base string) string {
	return strings.ToUpper(base) + "USDT"
}

func FormatFuturesSymbol(base string) string {
	return strings.ToUpper(base) + "_USDT"
}
