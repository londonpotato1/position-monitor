// Package bybit provides Bybit exchange API client.
// 바이빗 V5 REST API 클라이언트 (포지션 관리용)
package bybit

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
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

const (
	BaseURL = "https://api.bybit.com"
)

// Client Bybit REST 클라이언트
type Client struct {
	apiKey    string
	apiSecret string
	client    *http.Client
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

// ResolveFuturesBase 현물 base를 선물 base로 변환 (중앙 레지스트리 위임)
func ResolveFuturesBase(base string) string {
	return exchange.ResolveFuturesSymbol("bybit", base)
}

// FormatSymbol 심볼 형식 변환 (base -> BASEUSDT)
func (c *Client) FormatSymbol(base, marketType string) string {
	if marketType == "futures" || marketType == "linear" {
		return exchange.ResolveFuturesSymbol("bybit", base) + "USDT"
	}
	return base + "USDT"
}

// signPayload 서명 생성 (timestamp + apiKey + recvWindow + payload)
func (c *Client) signPayload(timestamp, payload string) string {
	signStr := timestamp + c.apiKey + "5000" + payload
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(signStr))
	return hex.EncodeToString(h.Sum(nil))
}

// request HTTP 요청 (인증)
func (c *Client) request(method, endpoint string, params interface{}) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	reqURL := BaseURL + endpoint
	var req *http.Request
	var err error
	var sign string

	if method == "GET" {
		var queryString string
		if p, ok := params.(map[string]string); ok && len(p) > 0 {
			values := url.Values{}
			for k, v := range p {
				values.Add(k, v)
			}
			queryString = values.Encode()
			reqURL += "?" + queryString
		}
		sign = c.signPayload(timestamp, queryString)
		req, err = http.NewRequest(method, reqURL, nil)
	} else {
		jsonBody, marshalErr := json.Marshal(params)
		if marshalErr != nil {
			return nil, marshalErr
		}
		bodyStr := string(jsonBody)
		sign = c.signPayload(timestamp, bodyStr)
		req, err = http.NewRequest(method, reqURL, strings.NewReader(bodyStr))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
	}

	if err != nil {
		return nil, err
	}

	req.Header.Set("X-BAPI-API-KEY", c.apiKey)
	req.Header.Set("X-BAPI-SIGN", sign)
	req.Header.Set("X-BAPI-TIMESTAMP", timestamp)
	req.Header.Set("X-BAPI-RECV-WINDOW", "5000")

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

// publicRequest 인증 없는 public API 요청
func (c *Client) publicRequest(method, endpoint string, params map[string]string) ([]byte, error) {
	reqURL := BaseURL + endpoint

	if len(params) > 0 {
		values := url.Values{}
		for k, v := range params {
			values.Add(k, v)
		}
		reqURL += "?" + values.Encode()
	}

	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		return nil, err
	}

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

// APIResponse 공통 응답
type APIResponse struct {
	RetCode int             `json:"retCode"`
	RetMsg  string          `json:"retMsg"`
	Result  json.RawMessage `json:"result"`
	Time    int64           `json:"time"`
}

// PositionInfo 포지션 정보
type PositionInfo struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Size          string `json:"size"`
	AvgPrice      string `json:"avgPrice"`
	MarkPrice     string `json:"markPrice"`
	PositionValue string `json:"positionValue"`
	Leverage      string `json:"leverage"`
	UnrealisedPnl string `json:"unrealisedPnl"`
}

// WalletBalance 지갑 잔고 (coin 레벨)
type WalletBalance struct {
	Coin                string `json:"coin"`
	Equity              string `json:"equity"`
	WalletBalance       string `json:"walletBalance"`
	Free                string `json:"free"`
	Locked              string `json:"locked"`
	AvailableToWithdraw string `json:"availableToWithdraw"`
	TotalPositionIM     string `json:"totalPositionIM"`
	TotalOrderIM        string `json:"totalOrderIM"`
}

// AccountBalance account 레벨 잔고
type AccountBalance struct {
	TotalAvailableBalance string          `json:"totalAvailableBalance"`
	TotalEquity           string          `json:"totalEquity"`
	TotalWalletBalance    string          `json:"totalWalletBalance"`
	TotalMarginBalance    string          `json:"totalMarginBalance"`
	AccountType           string          `json:"accountType"`
	Coins                 []WalletBalance `json:"coin"`
}

// GetPositions 포지션 조회
func (c *Client) GetPositions(symbol string) ([]PositionInfo, error) {
	params := map[string]string{
		"category": "linear",
		"symbol":   symbol,
	}

	data, err := c.request("GET", "/v5/position/list", params)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("API error (%d): %s", resp.RetCode, resp.RetMsg)
	}

	var result struct {
		List []PositionInfo `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	return result.List, nil
}

// GetAllPositions 전체 포지션 조회 (심볼 없이)
func (c *Client) GetAllPositions() ([]PositionInfo, error) {
	params := map[string]string{
		"category":   "linear",
		"settleCoin": "USDT",
		"limit":      "200",
	}

	data, err := c.request("GET", "/v5/position/list", params)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("API error (%d): %s", resp.RetCode, resp.RetMsg)
	}

	var result struct {
		List []PositionInfo `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	return result.List, nil
}

// SwitchPositionMode 포지션 모드 전환 (0=원웨이, 3=헷지)
func (c *Client) SwitchPositionMode(mode int, symbol string) error {
	body := map[string]interface{}{
		"category": "linear",
		"mode":     mode,
	}
	if symbol != "" {
		body["symbol"] = symbol
	}
	data, err := c.request("POST", "/v5/position/switch-mode", body)
	if err != nil {
		return err
	}
	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return err
	}
	if resp.RetCode != 0 && resp.RetCode != 34036 && resp.RetCode != 110025 {
		return fmt.Errorf("position mode switch failed: retCode=%d msg=%s", resp.RetCode, resp.RetMsg)
	}
	return nil
}

// GetWalletBalance 지갑 잔고 조회 (coin 레벨만 반환)
func (c *Client) GetWalletBalance(accountType string) ([]WalletBalance, error) {
	acct, err := c.GetAccountBalance(accountType)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, nil
	}
	return acct.Coins, nil
}

// GetAccountBalance account 레벨 잔고 조회
func (c *Client) GetAccountBalance(accountType string) (*AccountBalance, error) {
	params := map[string]string{
		"accountType": accountType,
	}

	data, err := c.request("GET", "/v5/account/wallet-balance", params)
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("balance error (%d): %s", resp.RetCode, resp.RetMsg)
	}

	var result struct {
		List []AccountBalance `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	if len(result.List) > 0 {
		return &result.List[0], nil
	}

	return nil, nil
}

// GetAllBalances 전체 잔고 조회
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	result := make(map[string]*exchange.Balance)

	walletBalances, err := c.GetWalletBalance("UNIFIED")
	if err != nil {
		return nil, fmt.Errorf("balance query failed: %w", err)
	}

	for _, bal := range walletBalances {
		total, _ := strconv.ParseFloat(bal.WalletBalance, 64)
		free, _ := strconv.ParseFloat(bal.AvailableToWithdraw, 64)

		if total > 0 {
			result[bal.Coin] = &exchange.Balance{
				Currency: bal.Coin,
				Free:     free,
				Used:     total - free,
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

	data, err := c.publicRequest("GET", "/v5/market/tickers", map[string]string{
		"category": "spot",
		"symbol":   coin + "USDT",
	})
	if err != nil {
		return 0
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil || resp.RetCode != 0 {
		return 0
	}

	var tickerResult struct {
		List []struct {
			LastPrice string `json:"lastPrice"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &tickerResult); err != nil || len(tickerResult.List) == 0 {
		return 0
	}

	price, _ := strconv.ParseFloat(tickerResult.List[0].LastPrice, 64)
	return price
}

// GetFundingWalletBalance Funding 지갑 잔고 조회
func (c *Client) GetFundingWalletBalance() (float64, error) {
	data, err := c.request("GET", "/v5/asset/transfer/query-account-coins-balance", map[string]string{
		"accountType": "FUND",
	})
	if err != nil {
		return 0, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if resp.RetCode != 0 {
		return 0, fmt.Errorf("funding balance error (%d): %s", resp.RetCode, resp.RetMsg)
	}

	var fundResult struct {
		Balance []struct {
			Coin          string `json:"coin"`
			WalletBalance string `json:"walletBalance"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(resp.Result, &fundResult); err != nil {
		return 0, err
	}

	totalUSD := 0.0
	for _, item := range fundResult.Balance {
		bal, _ := strconv.ParseFloat(item.WalletBalance, 64)
		if bal <= 0 {
			continue
		}
		price := c.getUSDTPrice(item.Coin)
		val := bal * price
		if val > 0.01 {
			totalUSD += val
		}
	}

	return totalUSD, nil
}

// GetFundingWalletCoins Funding 지갑 코인별 잔고 조회 (헷지 담보 후보용)
func (c *Client) GetFundingWalletCoins(ctx context.Context) (map[string]*exchange.Balance, error) {
	data, err := c.request("GET", "/v5/asset/transfer/query-account-coins-balance", map[string]string{
		"accountType": "FUND",
	})
	if err != nil {
		return nil, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.RetCode != 0 {
		return nil, fmt.Errorf("funding coins error (%d): %s", resp.RetCode, resp.RetMsg)
	}

	var fundResult struct {
		Balance []struct {
			Coin          string `json:"coin"`
			WalletBalance string `json:"walletBalance"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(resp.Result, &fundResult); err != nil {
		return nil, err
	}

	result := make(map[string]*exchange.Balance)
	for _, item := range fundResult.Balance {
		bal, _ := strconv.ParseFloat(item.WalletBalance, 64)
		if bal <= 0 {
			continue
		}
		result[item.Coin] = &exchange.Balance{
			Currency: item.Coin,
			Free:     bal,
			Used:     0,
			Total:    bal,
		}
	}
	return result, nil
}

// GetLoanBalance Crypto Loan 부채 합산 (모든 통화)
func (c *Client) GetLoanBalance() (float64, error) {
	data, err := c.request("GET", "/v5/crypto-loan/ongoing-orders", map[string]string{})
	if err != nil {
		return 0, err
	}

	var resp APIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if resp.RetCode != 0 {
		return 0, fmt.Errorf("loan balance error (%d): %s", resp.RetCode, resp.RetMsg)
	}

	var loanResult struct {
		List []struct {
			LoanCurrency string `json:"loanCurrency"`
			LoanAmount   string `json:"loanAmount"`
		} `json:"list"`
	}
	if err := json.Unmarshal(resp.Result, &loanResult); err != nil {
		return 0, err
	}

	totalLoanUSD := 0.0
	for _, item := range loanResult.List {
		amount, _ := strconv.ParseFloat(item.LoanAmount, 64)
		if amount <= 0 {
			continue
		}
		price := c.getUSDTPrice(item.LoanCurrency)
		totalLoanUSD += amount * price
	}

	return totalLoanUSD, nil
}
