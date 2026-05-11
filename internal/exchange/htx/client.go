// Package htx provides HTX (Huobi) exchange API client
package htx

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
	SpotBaseURL    = "https://api.huobi.pro"
	FuturesBaseURL = "https://api.hbdm.com"
)

type Client struct {
	apiKey    string
	apiSecret string
	client    *http.Client
	connected atomic.Bool
	accountID string
}

func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		apiKey: apiKey, apiSecret: apiSecret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Connect(ctx context.Context) error {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/v1/account/accounts", url.Values{})
	if err != nil {
		return fmt.Errorf("HTX 연결 실패: %w", err)
	}
	var result struct {
		Status string `json:"status"`
		Data   []struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.Status != "ok" {
		return fmt.Errorf("HTX 계정 조회 실패")
	}
	for _, acc := range result.Data {
		if acc.Type == "spot" {
			c.accountID = strconv.FormatInt(acc.ID, 10)
			break
		}
	}
	if c.accountID == "" {
		return fmt.Errorf("HTX 현물 계정을 찾을 수 없음")
	}
	c.connected.Store(true)
	return nil
}

func (c *Client) IsConnected() bool { return c.connected.Load() }

func (c *Client) sign(method, host, path, queryString string) string {
	payload := fmt.Sprintf("%s\n%s\n%s\n%s", method, host, path, queryString)
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (c *Client) getTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}

func (c *Client) publicRequest(baseURL, endpoint string, params url.Values) ([]byte, error) {
	reqURL := baseURL + endpoint
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}
	resp, err := c.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Client) signedRequest(baseURL, method, endpoint string, params url.Values) ([]byte, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")

	authParams := url.Values{}
	authParams.Set("AccessKeyId", c.apiKey)
	authParams.Set("SignatureMethod", "HmacSHA256")
	authParams.Set("SignatureVersion", "2")
	authParams.Set("Timestamp", c.getTimestamp())

	var req *http.Request
	var err error

	if method == "GET" {
		for k, v := range params {
			authParams[k] = v
		}
		queryString := authParams.Encode()
		signature := c.sign(method, host, endpoint, queryString)
		authParams.Set("Signature", signature)
		finalURL := baseURL + endpoint + "?" + authParams.Encode()
		req, err = http.NewRequest("GET", finalURL, nil)
	} else {
		queryString := authParams.Encode()
		signature := c.sign(method, host, endpoint, queryString)
		authParams.Set("Signature", signature)
		finalURL := baseURL + endpoint + "?" + authParams.Encode()

		var body io.Reader
		if len(params) > 0 {
			jsonMap := make(map[string]string, len(params))
			for k, vs := range params {
				if len(vs) > 0 {
					jsonMap[k] = vs[0]
				}
			}
			jsonBody, _ := json.Marshal(jsonMap)
			body = strings.NewReader(string(jsonBody))
		}
		req, err = http.NewRequest("POST", finalURL, body)
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// ========== 현물 ==========

func (c *Client) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	params := url.Values{}
	params.Set("symbol", strings.ToLower(symbol))
	data, err := c.publicRequest(SpotBaseURL, "/market/detail/merged", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Tick   struct {
			Close  float64   `json:"close"`
			Bid    []float64 `json:"bid"`
			Ask    []float64 `json:"ask"`
			Volume float64   `json:"vol"`
		} `json:"tick"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("ticker error")
	}
	bid, ask := 0.0, 0.0
	if len(result.Tick.Bid) > 0 {
		bid = result.Tick.Bid[0]
	}
	if len(result.Tick.Ask) > 0 {
		ask = result.Tick.Ask[0]
	}
	return &exchange.Ticker{
		Symbol: symbol, Price: result.Tick.Close, Bid: bid, Ask: ask,
		Volume24h: result.Tick.Volume, Timestamp: time.Now(),
	}, nil
}

func (c *Client) GetSpotOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	params := url.Values{}
	params.Set("symbol", strings.ToLower(symbol))
	params.Set("type", "step0")
	params.Set("depth", "20")
	data, err := c.publicRequest(SpotBaseURL, "/market/depth", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Tick   struct {
			Bids [][]float64 `json:"bids"`
			Asks [][]float64 `json:"asks"`
		} `json:"tick"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 오더북 조회 실패")
	}
	ob := &exchange.Orderbook{Symbol: symbol, Timestamp: time.Now()}
	ob.UpdatedAt = time.Now()
	for _, bid := range result.Tick.Bids {
		if len(bid) >= 2 {
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: bid[0], Quantity: bid[1]})
		}
	}
	for _, ask := range result.Tick.Asks {
		if len(ask) >= 2 {
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: ask[0], Quantity: ask[1]})
		}
	}
	return ob, nil
}

func (c *Client) GetSpotBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	if c.accountID == "" {
		return nil, fmt.Errorf("account ID not set")
	}
	data, err := c.signedRequest(SpotBaseURL, "GET", "/v1/account/accounts/"+c.accountID+"/balance", url.Values{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			List []struct {
				Currency string `json:"currency"`
				Type     string `json:"type"`
				Balance  string `json:"balance"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 잔고 조회 실패")
	}
	var free, locked float64
	for _, item := range result.Data.List {
		if strings.EqualFold(item.Currency, currency) {
			bal, _ := strconv.ParseFloat(item.Balance, 64)
			if item.Type == "trade" {
				free = bal
			} else if item.Type == "frozen" {
				locked = bal
			}
		}
	}
	return &exchange.Balance{Currency: currency, Free: free, Used: locked, Total: free + locked}, nil
}

// ========== 선물 ==========

func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	params := url.Values{}
	params.Set("contract_code", strings.ToUpper(symbol))
	data, err := c.publicRequest(FuturesBaseURL, "/linear-swap-ex/market/detail/merged", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Tick   struct {
			Close  float64   `json:"close"`
			Bid    []float64 `json:"bid"`
			Ask    []float64 `json:"ask"`
			Volume float64   `json:"vol"`
		} `json:"tick"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 선물 티커 조회 실패")
	}
	bid, ask := 0.0, 0.0
	if len(result.Tick.Bid) > 0 {
		bid = result.Tick.Bid[0]
	}
	if len(result.Tick.Ask) > 0 {
		ask = result.Tick.Ask[0]
	}
	return &exchange.Ticker{
		Symbol: symbol, Price: result.Tick.Close, Bid: bid, Ask: ask,
		Volume24h: result.Tick.Volume, Timestamp: time.Now(),
	}, nil
}

func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	params := url.Values{}
	params.Set("contract_code", strings.ToUpper(symbol))
	params.Set("type", "step0")
	data, err := c.publicRequest(FuturesBaseURL, "/linear-swap-ex/market/depth", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Tick   struct {
			Bids [][]float64 `json:"bids"`
			Asks [][]float64 `json:"asks"`
		} `json:"tick"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 선물 오더북 조회 실패")
	}
	ob := &exchange.Orderbook{Symbol: symbol, Timestamp: time.Now()}
	ob.UpdatedAt = time.Now()
	for _, bid := range result.Tick.Bids {
		if len(bid) >= 2 {
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: bid[0], Quantity: bid[1]})
		}
	}
	for _, ask := range result.Tick.Asks {
		if len(ask) >= 2 {
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: ask[0], Quantity: ask[1]})
		}
	}
	return ob, nil
}

func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	params := url.Values{}
	params.Set("margin_account", "USDT")
	data, err := c.signedRequest(FuturesBaseURL, "POST", "/linear-swap-api/v1/swap_cross_account_info", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data   []struct {
			MarginBalance   float64 `json:"margin_balance"`
			MarginAvailable float64 `json:"margin_available"`
			MarginFrozen    float64 `json:"margin_frozen"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 선물 잔고 조회 실패")
	}
	if len(result.Data) > 0 {
		return &exchange.Balance{
			Currency: "USDT", Free: result.Data[0].MarginAvailable,
			Used: result.Data[0].MarginFrozen, Total: result.Data[0].MarginBalance,
		}, nil
	}
	return &exchange.Balance{Currency: "USDT"}, nil
}

func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	params := url.Values{}
	params.Set("contract_code", strings.ToUpper(symbol))
	data, err := c.signedRequest(FuturesBaseURL, "POST", "/linear-swap-api/v1/swap_cross_position_info", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data   []struct {
			Symbol       string  `json:"contract_code"`
			Volume       float64 `json:"volume"`
			Direction    string  `json:"direction"`
			CostOpen     float64 `json:"cost_open"`
			LastPrice    float64 `json:"last_price"`
			ProfitUnreal float64 `json:"profit_unreal"`
			LeverRate    int     `json:"lever_rate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 포지션 조회 실패")
	}
	for _, p := range result.Data {
		if p.Volume > 0 {
			side := "long"
			if p.Direction == "sell" {
				side = "short"
			}
			return &exchange.Position{
				Symbol: symbol, Side: side, Size: p.Volume,
				EntryPrice: p.CostOpen, MarkPrice: p.LastPrice,
				UnrealizedPL: p.ProfitUnreal, Leverage: p.LeverRate,
			}, nil
		}
	}
	return &exchange.Position{Symbol: symbol}, nil
}

func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	// POST with no contract_code returns all cross-margin positions
	data, err := c.signedRequest(FuturesBaseURL, "POST", "/linear-swap-api/v1/swap_cross_position_info", url.Values{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data   []struct {
			ContractCode string  `json:"contract_code"`
			Volume       float64 `json:"volume"`
			Direction    string  `json:"direction"`
			CostOpen     float64 `json:"cost_open"`
			LastPrice    float64 `json:"last_price"`
			ProfitUnreal float64 `json:"profit_unreal"`
			LeverRate    int     `json:"lever_rate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 전체 포지션 조회 실패: status=%s", result.Status)
	}
	var positions []*exchange.Position
	for _, p := range result.Data {
		if p.Volume <= 0 {
			continue
		}
		side := "long"
		if p.Direction == "sell" {
			side = "short"
		}
		positions = append(positions, &exchange.Position{
			Symbol:       p.ContractCode,
			Side:         side,
			Size:         p.Volume,
			EntryPrice:   p.CostOpen,
			MarkPrice:    p.LastPrice,
			UnrealizedPL: p.ProfitUnreal,
			Leverage:     p.LeverRate,
		})
	}
	return positions, nil
}

func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	if c.accountID == "" {
		return nil, fmt.Errorf("account ID not set")
	}
	data, err := c.signedRequest(SpotBaseURL, "GET", "/v1/account/accounts/"+c.accountID+"/balance", url.Values{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			List []struct {
				Currency string `json:"currency"`
				Type     string `json:"type"`
				Balance  string `json:"balance"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("HTX 전체 잔고 조회 실패")
	}

	balanceMap := make(map[string]*exchange.Balance)
	tempMap := make(map[string]map[string]float64)
	for _, item := range result.Data.List {
		currency := strings.ToUpper(item.Currency)
		if _, exists := tempMap[currency]; !exists {
			tempMap[currency] = map[string]float64{"trade": 0, "frozen": 0}
		}
		bal, _ := strconv.ParseFloat(item.Balance, 64)
		if item.Type == "trade" {
			tempMap[currency]["trade"] = bal
		} else if item.Type == "frozen" {
			tempMap[currency]["frozen"] = bal
		}
	}
	for currency, data := range tempMap {
		total := data["trade"] + data["frozen"]
		if total > 0 {
			balanceMap[currency] = &exchange.Balance{
				Currency: currency, Free: data["trade"], Used: data["frozen"], Total: total,
			}
		}
	}
	return balanceMap, nil
}
