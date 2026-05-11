// Package bithumb provides Bithumb exchange API client.
// 빗썸 API 구현 - KRW 마켓 거래 전용
// 빗썸 v1 API (업비트 호환) 사용
package bithumb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

const (
	BaseURL = "https://api.bithumb.com/v1"
)

// Client 빗썸 API 클라이언트
type Client struct {
	apiKey    string
	apiSecret string
	client    *http.Client
	logger    *logger.Logger
	connected atomic.Bool

	// 마켓 정보 캐시
	markets map[string]MarketInfo
	mu      sync.RWMutex
}

// MarketInfo 마켓 정보
type MarketInfo struct {
	Market      string
	KoreanName  string
	EnglishName string
}

// NewClient 새 빗썸 클라이언트 생성
func NewClient(apiKey, apiSecret string, lg *logger.Logger) *Client {
	return &Client{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger:  lg,
		markets: make(map[string]MarketInfo),
	}
}

// base64UrlEncode Base64 URL 인코딩 (패딩 제거)
func base64UrlEncode(data []byte) string {
	encoded := base64.URLEncoding.EncodeToString(data)
	return strings.TrimRight(encoded, "=")
}

// generateJWT JWT 토큰 생성
func (c *Client) generateJWT(payload map[string]interface{}) string {
	header := map[string]string{"typ": "JWT", "alg": "HS256"}

	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(payload)

	headerEncoded := base64UrlEncode(headerJSON)
	payloadEncoded := base64UrlEncode(payloadJSON)

	// HMAC-SHA256 서명
	message := headerEncoded + "." + payloadEncoded
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(message))
	signature := base64UrlEncode(h.Sum(nil))

	return headerEncoded + "." + payloadEncoded + "." + signature
}

// createAuthHeaders 인증 헤더 생성
func (c *Client) createAuthHeaders(params map[string]string) http.Header {
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	query := values.Encode()

	// SHA512 해시
	h := sha512.New()
	h.Write([]byte(query))
	queryHash := hex.EncodeToString(h.Sum(nil))

	// JWT payload
	payload := map[string]interface{}{
		"access_key":     c.apiKey,
		"nonce":          uuid.New().String(),
		"timestamp":      time.Now().UnixMilli(),
		"query_hash":     queryHash,
		"query_hash_alg": "SHA512",
	}

	token := c.generateJWT(payload)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Content-Type", "application/json; charset=utf-8")
	return headers
}

// Connect 연결 및 초기화
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected.Load() && len(c.markets) > 0 {
		return nil
	}

	// 마켓 정보 로드
	if err := c.loadMarkets(ctx); err != nil {
		return fmt.Errorf("마켓 정보 로드 실패: %w", err)
	}

	// 인증 검증 (키가 있을 경우)
	if c.apiKey != "" && c.apiSecret != "" {
		if _, err := c.GetAllBalances(ctx); err != nil {
			c.log(fmt.Sprintf("인증 검증 실패: %s", err.Error()), "warning")
		} else {
			c.log("인증 검증 성공", "info")
		}
	}

	c.connected.Store(true)
	c.log("빗썸 연결 성공", "info")
	return nil
}

// loadMarkets 마켓 정보 로드 (빗썸 v1 API)
func (c *Client) loadMarkets(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", BaseURL+"/market/all", nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var markets []struct {
		Market      string `json:"market"`
		KoreanName  string `json:"korean_name"`
		EnglishName string `json:"english_name"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&markets); err != nil {
		return err
	}

	for _, m := range markets {
		if strings.HasPrefix(m.Market, "KRW-") {
			rawSymbol := strings.TrimPrefix(m.Market, "KRW-")
			// Bithumb 티커 정규화 (WAXL → AXL) + 원본도 보존
			symbol := NormalizeBithumbSymbol(rawSymbol)
			info := MarketInfo{
				Market:      m.Market,
				KoreanName:  m.KoreanName,
				EnglishName: m.EnglishName,
			}
			c.markets[symbol] = info
			if symbol != rawSymbol {
				c.markets[rawSymbol] = info
			}
		}
	}

	c.log(fmt.Sprintf("빗썸 마켓 로드: %d개", len(c.markets)), "info")
	return nil
}

// FormatSymbol 빗썸 심볼 포맷 (KRW-BTC 형식)
// 표준 심볼 → Bithumb 전용 심볼 역변환 (AXL → WAXL)
func FormatSymbol(base string) string {
	return "KRW-" + strings.ToUpper(ToBithumbSymbol(base))
}

// GetTicker 티커 조회
func (c *Client) GetTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	market := FormatSymbol(symbol)
	req, err := http.NewRequestWithContext(ctx, "GET", BaseURL+"/ticker?markets="+market, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var tickers []struct {
		Market           string  `json:"market"`
		TradePrice       float64 `json:"trade_price"`
		AccTradeVolume24 float64 `json:"acc_trade_volume_24h"`
		SignedChangeRate float64 `json:"signed_change_rate"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tickers); err != nil {
		return nil, err
	}

	if len(tickers) == 0 {
		return nil, fmt.Errorf("no ticker data for %s", symbol)
	}

	t := tickers[0]
	return &exchange.Ticker{
		Symbol:    symbol,
		Price:     t.TradePrice,
		Volume24h: t.AccTradeVolume24,
		Change24h: t.SignedChangeRate * 100,
		Timestamp: time.Now(),
	}, nil
}

// GetOrderbook 호가창 조회
func (c *Client) GetOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	market := FormatSymbol(symbol)
	req, err := http.NewRequestWithContext(ctx, "GET", BaseURL+"/orderbook?markets="+market, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var orderbooks []struct {
		Market         string `json:"market"`
		OrderbookUnits []struct {
			BidPrice float64 `json:"bid_price"`
			BidSize  float64 `json:"bid_size"`
			AskPrice float64 `json:"ask_price"`
			AskSize  float64 `json:"ask_size"`
		} `json:"orderbook_units"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&orderbooks); err != nil {
		return nil, err
	}

	if len(orderbooks) == 0 {
		return nil, fmt.Errorf("no orderbook data for %s", symbol)
	}

	ob := &exchange.Orderbook{
		Symbol: symbol,
		Bids:   make([]exchange.OrderbookEntry, 0),
		Asks:   make([]exchange.OrderbookEntry, 0),
	}
	ob.UpdatedAt = time.Now()

	for _, unit := range orderbooks[0].OrderbookUnits {
		ob.Bids = append(ob.Bids, exchange.OrderbookEntry{
			Price:    unit.BidPrice,
			Quantity: unit.BidSize,
		})
		ob.Asks = append(ob.Asks, exchange.OrderbookEntry{
			Price:    unit.AskPrice,
			Quantity: unit.AskSize,
		})
	}

	return ob, nil
}

// GetBalance 특정 코인 잔액 조회
func (c *Client) GetBalance(ctx context.Context, currency string) (*exchange.Balance, error) {
	balances, err := c.GetAllBalances(ctx)
	if err != nil {
		return nil, err
	}

	if bal, ok := balances[strings.ToUpper(currency)]; ok {
		return bal, nil
	}

	return &exchange.Balance{
		Currency: strings.ToUpper(currency),
		Free:     0,
		Used:     0,
		Total:    0,
	}, nil
}

// GetAllBalances 전체 잔액 조회
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	params := map[string]string{}
	headers := c.createAuthHeaders(params)

	req, err := http.NewRequestWithContext(ctx, "GET", BaseURL+"/accounts", nil)
	if err != nil {
		return nil, err
	}
	req.Header = headers

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var accounts []struct {
		Currency string `json:"currency"`
		Balance  string `json:"balance"`
		Locked   string `json:"locked"`
	}

	if err := json.Unmarshal(body, &accounts); err != nil {
		return nil, fmt.Errorf("parse error: %w, body: %s", err, string(body))
	}

	result := make(map[string]*exchange.Balance)
	for _, acc := range accounts {
		balance, err := strconv.ParseFloat(acc.Balance, 64)
		if err != nil {
			log.Printf("[WARN] Bithumb ParseFloat failed for balance %q (currency=%s): %v", acc.Balance, acc.Currency, err)
		}
		locked, err := strconv.ParseFloat(acc.Locked, 64)
		if err != nil {
			log.Printf("[WARN] Bithumb ParseFloat failed for locked %q (currency=%s): %v", acc.Locked, acc.Currency, err)
		}
		currency := strings.ToUpper(acc.Currency)
		// Bithumb 티커 정규화 (WAXL → AXL) + 원본 currency 키도 보존
		standard := NormalizeBithumbSymbol(currency)

		bal := &exchange.Balance{
			Currency: standard,
			Free:     balance,
			Used:     locked,
			Total:    balance + locked,
		}
		result[standard] = bal
		if standard != currency {
			result[currency] = bal
		}
	}

	return result, nil
}

// GetAvailableMarkets 사용 가능한 KRW 마켓 심볼 리스트
func (c *Client) GetAvailableMarkets() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	markets := make([]string, 0, len(c.markets))
	for symbol := range c.markets {
		markets = append(markets, symbol)
	}
	return markets
}

// IsConnected 연결 상태
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected.Load()
}

// log 로그 출력
func (c *Client) log(msg, level string, args ...interface{}) {
	if c.logger == nil {
		return
	}
	switch level {
	case "info":
		c.logger.Info(msg, args...)
	case "warning":
		c.logger.Warn(msg, args...)
	case "error":
		c.logger.Error(msg, args...)
	}
}

