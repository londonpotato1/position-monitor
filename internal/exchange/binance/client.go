// Package binance provides Binance exchange API client.
// 바이낸스 REST API 클라이언트 (포지션 관리용)
package binance

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
	SpotBaseURL    = "https://api.binance.com"
	FuturesBaseURL = "https://fapi.binance.com"
)

// Client Binance REST API 클라이언트
type Client struct {
	apiKey    string
	apiSecret string
	client    *http.Client
	connected atomic.Bool
}

// NewClient 새 클라이언트 생성
func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Connect 연결 테스트
// 공용 엔드포인트(time)로 네트워크 확인 후,
// signed probe(fapi/v2/balance)로 API 키/시크릿/IP whitelist/선물 권한을 즉시 검증한다.
// 없을 경우 첫 청산 시점까지 인증 실패가 지연되어 운영 리스크.
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.publicRequest(SpotBaseURL, "/api/v3/time")
	if err != nil {
		return fmt.Errorf("Binance 연결 실패: %w", err)
	}
	if _, err := c.signedRequest(FuturesBaseURL, "GET", "/fapi/v2/balance", url.Values{}); err != nil {
		return fmt.Errorf("Binance 인증 실패 (futures balance probe): %w", err)
	}
	c.connected.Store(true)
	return nil
}

// IsConnected 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// Disconnect 연결 상태 초기화
func (c *Client) Disconnect() {
	c.connected.Store(false)
}

// ResolveFuturesBase 현물 base를 선물 base로 변환 (중앙 레지스트리 위임)
func ResolveFuturesBase(base string) string {
	return exchange.ResolveFuturesSymbol("binance", base)
}

// FormatSymbol 심볼 형식 변환 (base -> BASEUSDT)
func (c *Client) FormatSymbol(base, marketType string) string {
	if marketType == "futures" || marketType == "linear" {
		return exchange.ResolveFuturesSymbol("binance", base) + "USDT"
	}
	return base + "USDT"
}

// sign HMAC-SHA256 서명 생성
func (c *Client) sign(queryString string) string {
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(queryString))
	return hex.EncodeToString(h.Sum(nil))
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

// signedRequest 서명된 API 요청
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

	req.Header.Set("X-MBX-APIKEY", c.apiKey)

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

	return body, nil
}

// ========== 현물 API ==========

// GetSpotTicker 현물 티커 조회
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
		Symbol:    result.Symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Change24h: change,
		Timestamp: time.Now(),
	}, nil
}

// GetSpotOrderbook 현물 오더북 조회
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
			return &exchange.Balance{
				Currency: b.Asset,
				Free:     free,
				Used:     locked,
				Total:    free + locked,
			}, nil
		}
	}

	return &exchange.Balance{Currency: currency}, nil
}

// GetAllBalances 전체 현물 잔고 조회
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
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

	balances := make(map[string]*exchange.Balance)
	for _, b := range result.Balances {
		free, _ := strconv.ParseFloat(b.Free, 64)
		locked, _ := strconv.ParseFloat(b.Locked, 64)
		total := free + locked
		if total > 0 {
			balances[b.Asset] = &exchange.Balance{
				Currency: b.Asset,
				Free:     free,
				Used:     locked,
				Total:    total,
			}
		}
	}

	return balances, nil
}

// ========== 선물 API ==========

// GetFuturesTicker 선물 티커 조회
func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	data, err := c.publicRequest(FuturesBaseURL, "/fapi/v1/ticker/24hr?symbol="+symbol)
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
		Symbol:    result.Symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume24h: volume,
		Change24h: change,
		Timestamp: time.Now(),
	}, nil
}

// GetFuturesOrderbook 선물 오더북 조회
func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	data, err := c.publicRequest(FuturesBaseURL, "/fapi/v1/depth?symbol="+symbol+"&limit=20")
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
	data, err := c.signedRequest(FuturesBaseURL, "GET", "/fapi/v2/balance", url.Values{})
	if err != nil {
		return nil, err
	}

	var result []struct {
		Asset            string `json:"asset"`
		Balance          string `json:"balance"`
		AvailableBalance string `json:"availableBalance"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	for _, b := range result {
		if b.Asset == "USDT" {
			total, _ := strconv.ParseFloat(b.Balance, 64)
			free, _ := strconv.ParseFloat(b.AvailableBalance, 64)
			return &exchange.Balance{
				Currency: "USDT",
				Free:     free,
				Used:     total - free,
				Total:    total,
			}, nil
		}
	}

	return &exchange.Balance{Currency: "USDT"}, nil
}

// GetPosition 선물 포지션 조회
func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	params := url.Values{}
	params.Set("symbol", symbol)

	data, err := c.signedRequest(FuturesBaseURL, "GET", "/fapi/v2/positionRisk", params)
	if err != nil {
		return nil, err
	}

	var result []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		MarkPrice        string `json:"markPrice"`
		UnRealizedProfit string `json:"unRealizedProfit"`
		Leverage         string `json:"leverage"`
		PositionSide     string `json:"positionSide"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	var bestPos *exchange.Position
	for _, p := range result {
		if p.Symbol != symbol {
			continue
		}
		size, _ := strconv.ParseFloat(p.PositionAmt, 64)
		entryPrice, _ := strconv.ParseFloat(p.EntryPrice, 64)
		markPrice, _ := strconv.ParseFloat(p.MarkPrice, 64)
		unrealizedPL, _ := strconv.ParseFloat(p.UnRealizedProfit, 64)
		leverage, _ := strconv.Atoi(p.Leverage)

		side := "long"
		if size < 0 {
			side = "short"
			size = -size
		}

		pos := &exchange.Position{
			Symbol:       symbol,
			Side:         side,
			Size:         size,
			EntryPrice:   entryPrice,
			MarkPrice:    markPrice,
			UnrealizedPL: unrealizedPL,
			Leverage:     leverage,
		}

		if size > 0 {
			return pos, nil
		}
		if bestPos == nil {
			bestPos = pos
		}
	}

	if bestPos != nil {
		return bestPos, nil
	}
	return &exchange.Position{Symbol: symbol}, nil
}

// GetAllPositions 전체 선물 포지션 조회
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	data, err := c.signedRequest(FuturesBaseURL, "GET", "/fapi/v2/positionRisk", url.Values{})
	if err != nil {
		return nil, err
	}

	var result []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		MarkPrice        string `json:"markPrice"`
		UnRealizedProfit string `json:"unRealizedProfit"`
		Leverage         string `json:"leverage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	for _, p := range result {
		size, _ := strconv.ParseFloat(p.PositionAmt, 64)
		if size == 0 {
			continue
		}
		entryPrice, _ := strconv.ParseFloat(p.EntryPrice, 64)
		markPrice, _ := strconv.ParseFloat(p.MarkPrice, 64)
		unrealizedPL, _ := strconv.ParseFloat(p.UnRealizedProfit, 64)
		leverage, _ := strconv.Atoi(p.Leverage)

		side := "long"
		if size < 0 {
			side = "short"
			size = -size
		}

		positions = append(positions, &exchange.Position{
			Symbol:       p.Symbol,
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

	data, err := c.publicRequest(SpotBaseURL, "/api/v3/ticker/price?symbol="+coin+"USDT")
	if err != nil {
		return 0
	}
	var result struct {
		Price string `json:"price"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0
	}
	price, _ := strconv.ParseFloat(result.Price, 64)
	return price
}

// GetEarnBalance Earn 잔고 조회 (Flexible + Locked)
func (c *Client) GetEarnBalance() (float64, error) {
	totalUSD := 0.0

	// 1. Flexible Earn
	current := 1
	for {
		params := url.Values{}
		params.Set("size", "100")
		params.Set("current", strconv.Itoa(current))
		data, err := c.signedRequest(SpotBaseURL, "GET", "/sapi/v1/simple-earn/flexible/position", params)
		if err != nil {
			break
		}
		var resp struct {
			Rows []struct {
				Asset       string `json:"asset"`
				TotalAmount string `json:"totalAmount"`
			} `json:"rows"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			break
		}
		for _, item := range resp.Rows {
			amount, _ := strconv.ParseFloat(item.TotalAmount, 64)
			if amount <= 0 {
				continue
			}
			price := c.getUSDTPrice(item.Asset)
			val := amount * price
			if val > 0.01 {
				totalUSD += val
			}
		}
		if current*100 >= resp.Total {
			break
		}
		current++
	}

	// Locked Earn은 잔고 대시보드에서도 조회하지 않으므로 생략

	return totalUSD, nil
}

// GetMarginBalance Cross Margin 잔고 조회 (totalNetAssetOfBtc * BTC가격)
func (c *Client) GetMarginBalance() (float64, error) {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/sapi/v1/margin/account", url.Values{})
	if err != nil {
		return 0, err
	}

	var resp struct {
		TotalNetAssetOfBtc string `json:"totalNetAssetOfBtc"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}

	netBTC, _ := strconv.ParseFloat(resp.TotalNetAssetOfBtc, 64)
	if netBTC <= 0 {
		return 0, nil
	}

	btcPrice := c.getUSDTPrice("BTC")
	return netBTC * btcPrice, nil
}

// GetEarnCoins Flexible Earn 코인별 잔고 조회 (헷지 담보 후보용)
// Locked Earn은 비유동적이므로 제외
func (c *Client) GetEarnCoins(ctx context.Context) (map[string]*exchange.Balance, error) {
	result := make(map[string]*exchange.Balance)
	current := 1
	for {
		params := url.Values{}
		params.Set("size", "100")
		params.Set("current", strconv.Itoa(current))
		data, err := c.signedRequest(SpotBaseURL, "GET", "/sapi/v1/simple-earn/flexible/position", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Rows []struct {
				Asset       string `json:"asset"`
				TotalAmount string `json:"totalAmount"`
			} `json:"rows"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		for _, item := range resp.Rows {
			amount, _ := strconv.ParseFloat(item.TotalAmount, 64)
			if amount <= 0 {
				continue
			}
			if existing, ok := result[item.Asset]; ok {
				existing.Total += amount
				existing.Free += amount
			} else {
				result[item.Asset] = &exchange.Balance{
					Currency: item.Asset,
					Free:     amount,
					Used:     0,
					Total:    amount,
				}
			}
		}
		if current*100 >= resp.Total {
			break
		}
		current++
	}
	return result, nil
}

// GetMarginCoins Cross Margin netAsset 코인별 잔고 조회 (헷지 담보 후보용)
// netAsset = free + locked - borrowed; borrowed > free 인 행(순부채) 제외
func (c *Client) GetMarginCoins(ctx context.Context) (map[string]*exchange.Balance, error) {
	data, err := c.signedRequest(SpotBaseURL, "GET", "/sapi/v1/margin/account", url.Values{})
	if err != nil {
		return nil, err
	}

	var resp struct {
		UserAssets []struct {
			Asset    string `json:"asset"`
			Free     string `json:"free"`
			Locked   string `json:"locked"`
			Borrowed string `json:"borrowed"`
			NetAsset string `json:"netAsset"`
		} `json:"userAssets"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	result := make(map[string]*exchange.Balance)
	for _, item := range resp.UserAssets {
		free, _ := strconv.ParseFloat(item.Free, 64)
		locked, _ := strconv.ParseFloat(item.Locked, 64)
		borrowed, _ := strconv.ParseFloat(item.Borrowed, 64)
		netAsset, _ := strconv.ParseFloat(item.NetAsset, 64)
		// 순부채(담보 아님) 및 잔고 없는 항목 제외
		if borrowed > free || netAsset <= 0 {
			continue
		}
		result[item.Asset] = &exchange.Balance{
			Currency: item.Asset,
			Free:     free,
			Used:     locked,
			Total:    netAsset,
		}
	}
	return result, nil
}

// ========== User Data Stream ==========

// StartUserDataStream listenKey 발급 (POST /fapi/v1/listenKey, 서명 불필요 — API key 헤더만)
func (c *Client) StartUserDataStream(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", FuturesBaseURL+"/fapi/v1/listenKey", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("StartUserDataStream API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.ListenKey == "" {
		return "", fmt.Errorf("listenKey가 비어있음: %s", string(body))
	}
	return result.ListenKey, nil
}

// KeepAliveUserDataStream listenKey 연장 (PUT /fapi/v1/listenKey, 서명 불필요)
func (c *Client) KeepAliveUserDataStream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", FuturesBaseURL+"/fapi/v1/listenKey", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("KeepAliveUserDataStream API error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetFundingBalance Funding 잔고 조회
func (c *Client) GetFundingBalance() (float64, error) {
	data, err := c.signedRequest(SpotBaseURL, "POST", "/sapi/v1/asset/get-funding-asset", url.Values{})
	if err != nil {
		return 0, err
	}

	var items []struct {
		Asset  string `json:"asset"`
		Free   string `json:"free"`
		Locked string `json:"locked"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return 0, err
	}

	totalUSD := 0.0
	for _, item := range items {
		free, _ := strconv.ParseFloat(item.Free, 64)
		locked, _ := strconv.ParseFloat(item.Locked, 64)
		amount := free + locked
		if amount <= 0 {
			continue
		}
		price := c.getUSDTPrice(item.Asset)
		val := amount * price
		if val > 0.01 {
			totalUSD += val
		}
	}

	return totalUSD, nil
}
