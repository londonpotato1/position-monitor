// Package gate provides Gate.io exchange API client
package gate

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
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
	BaseURL = "https://api.gateio.ws"
)

// Client Gate.io REST API 클라이언트
type Client struct {
	apiKey    string
	apiSecret string
	client    *http.Client
	connected bool
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
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.publicRequest("/api/v4/spot/time")
	if err != nil {
		return fmt.Errorf("Gate.io 연결 실패: %w", err)
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

// sign 서명 생성
// Gate.io v4 API: HexEncode(HMAC_SHA512(secret, request_path + method + timestamp + hash(body)))
func (c *Client) sign(method, path, query, body string, timestamp int64) string {
	// Body hash (SHA512)
	h := sha512.New()
	h.Write([]byte(body))
	bodyHash := hex.EncodeToString(h.Sum(nil))

	// Sign string
	signStr := fmt.Sprintf("%s\n%s\n%s\n%s\n%d", method, path, query, bodyHash, timestamp)

	// HMAC-SHA512
	mac := hmac.New(sha512.New, []byte(c.apiSecret))
	mac.Write([]byte(signStr))
	return hex.EncodeToString(mac.Sum(nil))
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
func (c *Client) signedRequest(method, path, query, body string) ([]byte, error) {
	timestamp := time.Now().Unix()
	signature := c.sign(method, path, query, body, timestamp)

	fullURL := BaseURL + path
	if query != "" {
		fullURL += "?" + query
	}

	var req *http.Request
	var err error

	if method == "GET" || method == "DELETE" {
		req, err = http.NewRequest(method, fullURL, nil)
	} else {
		req, err = http.NewRequest(method, fullURL, strings.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("KEY", c.apiKey)
	req.Header.Set("SIGN", signature)
	req.Header.Set("Timestamp", strconv.FormatInt(timestamp, 10))

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

// ========== 현물 API ==========

// GetSpotTicker 현물 티커 조회
func (c *Client) GetSpotTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	endpoint := "/api/v4/spot/tickers?currency_pair=" + symbol

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var tickers []struct {
		CurrencyPair string `json:"currency_pair"`
		Last         string `json:"last"`
		LowestAsk    string `json:"lowest_ask"`
		HighestBid   string `json:"highest_bid"`
		BaseVolume   string `json:"base_volume"`
		ChangePerc   string `json:"change_percentage"`
	}
	if err := json.Unmarshal(data, &tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("티커를 찾을 수 없음: %s", symbol)
	}

	t := tickers[0]
	price, _ := strconv.ParseFloat(t.Last, 64)
	bid, _ := strconv.ParseFloat(t.HighestBid, 64)
	ask, _ := strconv.ParseFloat(t.LowestAsk, 64)
	volume, _ := strconv.ParseFloat(t.BaseVolume, 64)
	change, _ := strconv.ParseFloat(t.ChangePerc, 64)

	return &exchange.Ticker{
		Symbol:    t.CurrencyPair,
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
	endpoint := "/api/v4/spot/order_book?currency_pair=" + symbol + "&limit=20"

	data, err := c.publicRequest(endpoint)
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
	path := "/api/v4/spot/accounts"
	query := ""
	if currency != "" {
		query = "currency=" + currency
	}

	data, err := c.signedRequest("GET", path, query, "")
	if err != nil {
		return nil, err
	}

	var accounts []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}

	for _, a := range accounts {
		if a.Currency == currency {
			free, _ := strconv.ParseFloat(a.Available, 64)
			locked, _ := strconv.ParseFloat(a.Locked, 64)
			return &exchange.Balance{
				Currency: a.Currency,
				Free:     free,
				Used:     locked,
				Total:    free + locked,
			}, nil
		}
	}

	return &exchange.Balance{Currency: currency}, nil
}

// ========== 선물 API ==========

// GetFuturesTicker 선물 티커 조회
func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	endpoint := "/api/v4/futures/usdt/tickers?contract=" + symbol

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var tickers []struct {
		Contract   string `json:"contract"`
		Last       string `json:"last"`
		LowestAsk  string `json:"lowest_ask"`
		HighestBid string `json:"highest_bid"`
		Volume24h  string `json:"volume_24h"`
		ChangePerc string `json:"change_percentage"`
	}
	if err := json.Unmarshal(data, &tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("티커를 찾을 수 없음: %s", symbol)
	}

	t := tickers[0]
	price, _ := strconv.ParseFloat(t.Last, 64)
	bid, _ := strconv.ParseFloat(t.HighestBid, 64)
	ask, _ := strconv.ParseFloat(t.LowestAsk, 64)
	volume, _ := strconv.ParseFloat(t.Volume24h, 64)
	change, _ := strconv.ParseFloat(t.ChangePerc, 64)

	return &exchange.Ticker{
		Symbol:    t.Contract,
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
	endpoint := "/api/v4/futures/usdt/order_book?contract=" + symbol + "&limit=20"

	data, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var result struct {
		Bids []struct {
			P string `json:"p"`
			S int64  `json:"s"`
		} `json:"bids"`
		Asks []struct {
			P string `json:"p"`
			S int64  `json:"s"`
		} `json:"asks"`
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
		price, _ := strconv.ParseFloat(bid.P, 64)
		ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: price, Quantity: float64(bid.S)})
	}

	for _, ask := range result.Asks {
		price, _ := strconv.ParseFloat(ask.P, 64)
		ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: price, Quantity: float64(ask.S)})
	}

	return ob, nil
}

// GetFuturesBalance 선물 USDT 잔고 조회
func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	path := "/api/v4/futures/usdt/accounts"

	data, err := c.signedRequest("GET", path, "", "")
	if err != nil {
		return nil, err
	}

	var result struct {
		Total         string `json:"total"`
		Available     string `json:"available"`
		UnrealisedPnl string `json:"unrealised_pnl"`
		Currency      string `json:"currency"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	total, _ := strconv.ParseFloat(result.Total, 64)
	free, _ := strconv.ParseFloat(result.Available, 64)

	return &exchange.Balance{
		Currency: result.Currency,
		Free:     free,
		Used:     total - free,
		Total:    total,
	}, nil
}

// GetPosition 선물 포지션 조회
func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	path := fmt.Sprintf("/api/v4/futures/usdt/positions/%s", symbol)

	data, err := c.signedRequest("GET", path, "", "")
	if err != nil {
		return nil, err
	}

	var result struct {
		Contract      string `json:"contract"`
		Size          int64  `json:"size"`
		EntryPrice    string `json:"entry_price"`
		MarkPrice     string `json:"mark_price"`
		UnrealisedPnl string `json:"unrealised_pnl"`
		Leverage      string `json:"leverage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	entryPrice, _ := strconv.ParseFloat(result.EntryPrice, 64)
	markPrice, _ := strconv.ParseFloat(result.MarkPrice, 64)
	unrealizedPL, _ := strconv.ParseFloat(result.UnrealisedPnl, 64)
	leverage, _ := strconv.Atoi(result.Leverage)

	side := "long"
	size := float64(result.Size)
	if result.Size < 0 {
		side = "short"
		size = float64(-result.Size)
	}

	return &exchange.Position{
		Symbol:       result.Contract,
		Side:         side,
		Size:         size,
		EntryPrice:   entryPrice,
		MarkPrice:    markPrice,
		UnrealizedPL: unrealizedPL,
		Leverage:     leverage,
	}, nil
}

// GetAllPositions 전체 선물 포지션 조회 (size!=0인 포지션만 반환)
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	path := "/api/v4/futures/usdt/positions"

	data, err := c.signedRequest("GET", path, "", "")
	if err != nil {
		return nil, err
	}

	var raw []struct {
		Contract      string `json:"contract"`
		Size          int64  `json:"size"`
		EntryPrice    string `json:"entry_price"`
		MarkPrice     string `json:"mark_price"`
		UnrealisedPnl string `json:"unrealised_pnl"`
		Leverage      string `json:"leverage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	for _, r := range raw {
		if r.Size == 0 {
			continue
		}

		side := "long"
		size := float64(r.Size)
		if r.Size < 0 {
			side = "short"
			size = float64(-r.Size)
		}

		entryPrice, _ := strconv.ParseFloat(r.EntryPrice, 64)
		markPrice, _ := strconv.ParseFloat(r.MarkPrice, 64)
		unrealizedPL, _ := strconv.ParseFloat(r.UnrealisedPnl, 64)
		leverage, _ := strconv.Atoi(r.Leverage)

		positions = append(positions, &exchange.Position{
			Symbol:       r.Contract,
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

// GetAllBalances 전체 잔고 조회
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	result := make(map[string]*exchange.Balance)

	path := "/api/v4/spot/accounts"
	data, err := c.signedRequest("GET", path, "", "")
	if err != nil {
		return nil, err
	}

	var accounts []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}

	for _, a := range accounts {
		free, _ := strconv.ParseFloat(a.Available, 64)
		locked, _ := strconv.ParseFloat(a.Locked, 64)
		total := free + locked

		if total > 0 {
			result[a.Currency] = &exchange.Balance{
				Currency: a.Currency,
				Free:     free,
				Used:     locked,
				Total:    total,
			}
		}
	}

	return result, nil
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

	data, err := c.publicRequest("/api/v4/spot/tickers?currency_pair=" + coin + "_USDT")
	if err != nil {
		return 0
	}

	var tickers []struct {
		Last string `json:"last"`
	}
	if err := json.Unmarshal(data, &tickers); err != nil || len(tickers) == 0 {
		return 0
	}

	price, _ := strconv.ParseFloat(tickers[0].Last, 64)
	return price
}

// GetCrossMarginBalance Cross Margin 잔고 조회
func (c *Client) GetCrossMarginBalance() (float64, error) {
	data, err := c.signedRequest("GET", "/api/v4/margin/cross/accounts", "", "")
	if err != nil {
		return 0, err
	}

	var resp struct {
		Total string `json:"total"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}

	total, _ := strconv.ParseFloat(resp.Total, 64)
	return total, nil
}

// GetEarnBalance Earn(Uni Lending) 잔고 조회
func (c *Client) GetEarnBalance() (float64, error) {
	data, err := c.signedRequest("GET", "/api/v4/earn/uni/lends", "", "")
	if err != nil {
		return 0, err
	}

	var items []struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return 0, err
	}

	totalUSD := 0.0
	for _, item := range items {
		amount, _ := strconv.ParseFloat(item.Amount, 64)
		if amount <= 0 {
			continue
		}
		price := c.getUSDTPrice(item.Currency)
		val := amount * price
		if val > 0.01 {
			totalUSD += val
		}
	}

	return totalUSD, nil
}

// GetCollateralBalance Collateral 잔고 조회 (단일 담보 + 다중 담보, 담보 - 대출)
func (c *Client) GetCollateralBalance() (float64, error) {
	totalUSD := 0.0

	// 1. 단일 담보 대출 (single collateral)
	data, err := c.signedRequest("GET", "/api/v4/loan/collateral/orders", "status=repaying&limit=100", "")
	if err == nil {
		var orders []struct {
			CollateralCurrency string `json:"collateral_currency"`
			CollateralAmount   string `json:"collateral_amount"`
			BorrowCurrency     string `json:"borrow_currency"`
			LeftRepayPrincipal string `json:"left_repay_principal"`
		}
		if json.Unmarshal(data, &orders) == nil {
			for _, order := range orders {
				collateralAmt, _ := strconv.ParseFloat(order.CollateralAmount, 64)
				if collateralAmt > 0 {
					totalUSD += collateralAmt * c.getUSDTPrice(order.CollateralCurrency)
				}
				repay, _ := strconv.ParseFloat(order.LeftRepayPrincipal, 64)
				if repay > 0 {
					totalUSD -= repay * c.getUSDTPrice(order.BorrowCurrency)
				}
			}
		}
	}

	// 2. 다중 담보 대출 (multi collateral)
	data2, err := c.signedRequest("GET", "/api/v4/loan/multi_collateral/orders", "status=repaying&limit=100", "")
	if err == nil {
		var multiOrders []struct {
			CollateralCurrencies []struct {
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
			} `json:"collateral_currencies"`
			BorrowCurrency     string `json:"borrow_currency"`
			LeftRepayPrincipal string `json:"left_repay_principal"`
		}
		if json.Unmarshal(data2, &multiOrders) == nil {
			for _, order := range multiOrders {
				for _, col := range order.CollateralCurrencies {
					amt, _ := strconv.ParseFloat(col.Amount, 64)
					if amt > 0 {
						totalUSD += amt * c.getUSDTPrice(col.Currency)
					}
				}
				repay, _ := strconv.ParseFloat(order.LeftRepayPrincipal, 64)
				if repay > 0 {
					totalUSD -= repay * c.getUSDTPrice(order.BorrowCurrency)
				}
			}
		}
	}

	return totalUSD, nil
}
