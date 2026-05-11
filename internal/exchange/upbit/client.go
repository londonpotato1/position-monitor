// Package upbit provides Upbit exchange REST API client.
// 업비트 REST 클라이언트 - KRW 마켓 거래 전용
package upbit

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	BaseURL = "https://api.upbit.com/v1"
)

// Client 업비트 REST 클라이언트
type Client struct {
	accessKey string
	secretKey string
	client    *http.Client
}

// NewClient 새 클라이언트 생성
func NewClient(accessKey, secretKey string) *Client {
	return &Client{
		accessKey: accessKey,
		secretKey: secretKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// generateToken JWT 토큰 생성
func (c *Client) generateToken(queryHash string) (string, error) {
	claims := jwt.MapClaims{
		"access_key": c.accessKey,
		"nonce":      uuid.New().String(),
	}

	if queryHash != "" {
		claims["query_hash"] = queryHash
		claims["query_hash_alg"] = "SHA512"
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(c.secretKey))
}

// hashQuery 쿼리 해시 생성
func hashQuery(query string) string {
	hash := sha512.Sum512([]byte(query))
	return hex.EncodeToString(hash[:])
}

// request HTTP 요청
func (c *Client) request(method, endpoint string, params map[string]string, body interface{}) ([]byte, error) {
	reqURL := BaseURL + endpoint

	var queryString string
	if len(params) > 0 {
		values := url.Values{}
		for k, v := range params {
			values.Add(k, v)
		}
		queryString = values.Encode()
		if method == "GET" {
			reqURL += "?" + queryString
		}
	}

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewBuffer(jsonBody)
		queryString = string(jsonBody)
	}

	req, err := http.NewRequest(method, reqURL, reqBody)
	if err != nil {
		return nil, err
	}

	// JWT 토큰 생성
	var token string
	if queryString != "" {
		token, err = c.generateToken(hashQuery(queryString))
	} else {
		token, err = c.generateToken("")
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

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
		return nil, fmt.Errorf("API 오류 (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// AccountResponse 계좌 정보 응답
type AccountResponse struct {
	Currency            string `json:"currency"`
	Balance             string `json:"balance"`
	Locked              string `json:"locked"`
	AvgBuyPrice         string `json:"avg_buy_price"`
	AvgBuyPriceModified bool   `json:"avg_buy_price_modified"`
	UnitCurrency        string `json:"unit_currency"`
}

// GetAccounts 계좌 조회
func (c *Client) GetAccounts() ([]AccountResponse, error) {
	data, err := c.request("GET", "/accounts", nil, nil)
	if err != nil {
		return nil, err
	}

	var accounts []AccountResponse
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}

	return accounts, nil
}

// TickerResponse 현재가 응답
type TickerResponse struct {
	Market            string  `json:"market"`
	TradePrice        float64 `json:"trade_price"`
	OpeningPrice      float64 `json:"opening_price"`
	HighPrice         float64 `json:"high_price"`
	LowPrice          float64 `json:"low_price"`
	PrevClosingPrice  float64 `json:"prev_closing_price"`
	Change            string  `json:"change"`
	ChangePrice       float64 `json:"change_price"`
	ChangeRate        float64 `json:"change_rate"`
	SignedChangePrice float64 `json:"signed_change_price"`
	SignedChangeRate  float64 `json:"signed_change_rate"`
	TradeVolume       float64 `json:"trade_volume"`
	AccTradePrice     float64 `json:"acc_trade_price"`
	AccTradePrice24h  float64 `json:"acc_trade_price_24h"`
	AccTradeVolume    float64 `json:"acc_trade_volume"`
	AccTradeVolume24h float64 `json:"acc_trade_volume_24h"`
	Timestamp         int64   `json:"timestamp"`
}

// GetTicker 현재가 조회
func (c *Client) GetTicker(markets string) ([]TickerResponse, error) {
	reqURL := BaseURL + "/ticker?markets=" + markets

	resp, err := c.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tickers []TickerResponse
	if err := json.Unmarshal(body, &tickers); err != nil {
		return nil, err
	}

	return tickers, nil
}

