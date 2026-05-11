// Package lighter provides Lighter DEX API client
// Lighter는 zkSync 기반의 탈중앙화 거래소 (현물 + 선물)
package lighter

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"

	"github.com/ethereum/go-ethereum/crypto"
)

const (
	MainnetURL = "https://mainnet.zklighter.elliot.ai"
	TestnetURL = "https://testnet.zklighter.elliot.ai"
)

// Client Lighter REST API 클라이언트
type Client struct {
	privateKey   *ecdsa.PrivateKey
	address      string
	accountIndex int
	apiKeyIndex  int
	baseURL      string
	client       *http.Client
	connected    atomic.Bool
	marketCache  map[string]MarketInfo // coin -> market info (perp)
	spotCache    map[string]MarketInfo // coin -> market info (spot)
}

// MarketInfo 마켓 정보
type MarketInfo struct {
	MarketID      int
	Symbol        string
	SizeDecimals  int
	PriceDecimals int
	MarketType    string // "spot" or "perp"
}

// NewClient 새 클라이언트 생성
// privateKey: 이더리움 개인키 (0x 접두사 포함 또는 미포함)
// accountIndex: Lighter 계정 인덱스 (기본 0)
// testnet: true면 테스트넷 사용
func NewClient(privateKey string, accountIndex int, testnet bool) (*Client, error) {
	// 0x 접두사 제거
	privateKey = strings.TrimPrefix(privateKey, "0x")

	// 개인키 파싱
	privKey, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("개인키 파싱 실패: %w", err)
	}

	// 지갑 주소 추출
	address := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	baseURL := MainnetURL
	if testnet {
		baseURL = TestnetURL
	}

	return &Client{
		privateKey:   privKey,
		address:      address,
		accountIndex: accountIndex,
		apiKeyIndex:  0,
		baseURL:      baseURL,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		marketCache: make(map[string]MarketInfo),
		spotCache:   make(map[string]MarketInfo),
	}, nil
}

// Connect 연결 및 마켓 정보 로드
func (c *Client) Connect(ctx context.Context) error {
	if err := c.loadMarkets(); err != nil {
		return fmt.Errorf("Lighter 연결 실패: %w", err)
	}
	c.connected.Store(true)
	return nil
}

// IsConnected 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// loadMarkets 마켓 정보 로드
func (c *Client) loadMarkets() error {
	resp, err := c.publicRequest("/api/v1/order_books")
	if err != nil {
		return err
	}

	var result struct {
		OrderBooks []struct {
			MarketID               int    `json:"market_id"`
			Symbol                 string `json:"symbol"`
			MarketType             string `json:"market_type"`
			SupportedSizeDecimals  int    `json:"supported_size_decimals"`
			SupportedPriceDecimals int    `json:"supported_price_decimals"`
		} `json:"order_books"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return err
	}

	for _, ob := range result.OrderBooks {
		coin := strings.Split(ob.Symbol, "/")[0]
		coin = strings.Split(coin, "-")[0]
		coin = strings.ToUpper(coin)

		info := MarketInfo{
			MarketID:      ob.MarketID,
			Symbol:        ob.Symbol,
			SizeDecimals:  ob.SupportedSizeDecimals,
			PriceDecimals: ob.SupportedPriceDecimals,
			MarketType:    ob.MarketType,
		}

		if ob.MarketType == "spot" {
			c.spotCache[coin] = info
		} else {
			c.marketCache[coin] = info
		}
	}

	return nil
}

// publicRequest 공개 API 요청
func (c *Client) publicRequest(endpoint string) ([]byte, error) {
	resp, err := c.client.Get(c.baseURL + endpoint)
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
func (c *Client) signedRequest(method, endpoint string, payload map[string]interface{}) ([]byte, error) {
	timestamp := time.Now().UnixMilli()

	if payload == nil {
		payload = make(map[string]interface{})
	}
	payload["timestamp"] = timestamp
	payload["account_index"] = c.accountIndex

	body, _ := json.Marshal(payload)

	// 서명 생성
	hash := crypto.Keccak256Hash(body)
	sig, err := crypto.Sign(hash.Bytes(), c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("서명 실패: %w", err)
	}

	req, err := http.NewRequest(method, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", "0x"+hex.EncodeToString(sig))
	req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-Account-Index", strconv.Itoa(c.accountIndex))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// ========== 선물 API ==========

// GetFuturesTicker 선물 티커 조회
func (c *Client) GetFuturesTicker(ctx context.Context, symbol string) (*exchange.Ticker, error) {
	coin := formatCoin(symbol)

	info, ok := c.marketCache[coin]
	if !ok {
		return nil, fmt.Errorf("선물 마켓을 찾을 수 없음: %s", coin)
	}

	ob, err := c.getOrderbook(info.MarketID)
	if err != nil {
		return nil, err
	}

	bid, ask := 0.0, 0.0
	if len(ob.Bids) > 0 {
		bid = ob.Bids[0].Price
	}
	if len(ob.Asks) > 0 {
		ask = ob.Asks[0].Price
	}

	mid := (bid + ask) / 2

	return &exchange.Ticker{
		Symbol:    coin,
		Price:     mid,
		Bid:       bid,
		Ask:       ask,
		Timestamp: time.Now(),
	}, nil
}

// GetFuturesOrderbook 선물 오더북 조회
func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	coin := formatCoin(symbol)

	info, ok := c.marketCache[coin]
	if !ok {
		return nil, fmt.Errorf("선물 마켓을 찾을 수 없음: %s", coin)
	}

	return c.getOrderbook(info.MarketID)
}

// GetFuturesBalance 선물 잔고 조회 (USDC)
func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	balances, err := c.getAllBalances()
	if err != nil {
		return nil, err
	}

	if bal, ok := balances["USDC"]; ok {
		return bal, nil
	}

	if bal, ok := balances["TOTAL_VALUE"]; ok {
		return &exchange.Balance{
			Currency: "USDC",
			Free:     bal.Free,
			Used:     bal.Used,
			Total:    bal.Total,
		}, nil
	}

	return &exchange.Balance{Currency: "USDC"}, nil
}

// GetPosition 선물 포지션 조회
func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	coin := formatCoin(symbol)

	resp, err := c.signedRequest("GET", "/api/v1/account", map[string]interface{}{
		"by":    "index",
		"value": strconv.Itoa(c.accountIndex),
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Accounts []struct {
			Positions []struct {
				Symbol        string `json:"symbol"`
				Size          string `json:"size"`
				EntryPrice    string `json:"entry_price"`
				UnrealizedPnl string `json:"unrealized_pnl"`
			} `json:"positions"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	if len(result.Accounts) > 0 {
		for _, pos := range result.Accounts[0].Positions {
			posCoin := strings.Split(pos.Symbol, "/")[0]
			posCoin = strings.Split(posCoin, "-")[0]
			posCoin = strings.ToUpper(posCoin)

			if posCoin == coin {
				size, parseErr := strconv.ParseFloat(pos.Size, 64)
				if parseErr != nil {
					log.Printf("[WARN] Lighter ParseFloat failed for position size %q (coin=%s): %v", pos.Size, coin, parseErr)
				}
				entryPrice, parseErr := strconv.ParseFloat(pos.EntryPrice, 64)
				if parseErr != nil {
					log.Printf("[WARN] Lighter ParseFloat failed for entryPrice %q (coin=%s): %v", pos.EntryPrice, coin, parseErr)
				}
				unrealizedPL, parseErr := strconv.ParseFloat(pos.UnrealizedPnl, 64)
				if parseErr != nil {
					log.Printf("[WARN] Lighter ParseFloat failed for unrealizedPnl %q (coin=%s): %v", pos.UnrealizedPnl, coin, parseErr)
				}

				if size == 0 {
					continue
				}

				side := "long"
				if size < 0 {
					side = "short"
					size = -size
				}

				// 현재가 조회
				ticker, _ := c.GetFuturesTicker(ctx, coin)
				markPrice := entryPrice
				if ticker != nil {
					markPrice = ticker.Price
				}

				return &exchange.Position{
					Symbol:       coin,
					Side:         side,
					Size:         size,
					EntryPrice:   entryPrice,
					MarkPrice:    markPrice,
					UnrealizedPL: unrealizedPL,
				}, nil
			}
		}
	}

	return &exchange.Position{Symbol: coin}, nil
}

// GetAllPositions 전체 선물 포지션 조회
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	resp, err := c.signedRequest("GET", "/api/v1/account", map[string]interface{}{
		"by":    "index",
		"value": strconv.Itoa(c.accountIndex),
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Accounts []struct {
			Positions []struct {
				Symbol        string `json:"symbol"`
				Size          string `json:"size"`
				EntryPrice    string `json:"entry_price"`
				UnrealizedPnl string `json:"unrealized_pnl"`
			} `json:"positions"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	if len(result.Accounts) > 0 {
		for _, pos := range result.Accounts[0].Positions {
			size, parseErr := strconv.ParseFloat(pos.Size, 64)
			if parseErr != nil {
				log.Printf("[WARN] Lighter GetAllPositions ParseFloat failed for size %q (symbol=%s): %v", pos.Size, pos.Symbol, parseErr)
				continue
			}
			if size == 0 {
				continue
			}

			entryPrice, parseErr := strconv.ParseFloat(pos.EntryPrice, 64)
			if parseErr != nil {
				log.Printf("[WARN] Lighter GetAllPositions ParseFloat failed for entryPrice %q (symbol=%s): %v", pos.EntryPrice, pos.Symbol, parseErr)
			}
			unrealizedPL, parseErr := strconv.ParseFloat(pos.UnrealizedPnl, 64)
			if parseErr != nil {
				log.Printf("[WARN] Lighter GetAllPositions ParseFloat failed for unrealizedPnl %q (symbol=%s): %v", pos.UnrealizedPnl, pos.Symbol, parseErr)
			}

			// Lighter symbols are already bare coin names (e.g. "BTC"). Defensive split
			// in case a future API version adds "/USD" or similar suffixes.
			coin := strings.Split(pos.Symbol, "/")[0]
			coin = strings.Split(coin, "-")[0]
			coin = strings.ToUpper(coin)

			side := "long"
			if size < 0 {
				side = "short"
				size = -size
			}

			positions = append(positions, &exchange.Position{
				Symbol:     coin,
				Side:       side,
				Size:       size,
				EntryPrice: entryPrice,
				// MarkPrice: 0  // Lighter /api/v1/account does not return mark price
				// in the positions payload; left zero to avoid N+1 ticker fetches.
				UnrealizedPL: unrealizedPL,
				// Leverage: 0  // Lighter /api/v1/account does not expose per-position
				// leverage — left zero by design.
			})
		}
	}

	if positions == nil {
		positions = []*exchange.Position{}
	}
	return positions, nil
}

// GetAllBalances 전체 잔고 조회 (public wrapper)
func (c *Client) GetAllBalances(ctx context.Context) (map[string]*exchange.Balance, error) {
	return c.getAllBalances()
}

// ========== 내부 함수 ==========

// getOrderbook 오더북 조회
func (c *Client) getOrderbook(marketID int) (*exchange.Orderbook, error) {
	endpoint := fmt.Sprintf("/api/v1/order_book_orders?market_id=%d&limit=20", marketID)

	resp, err := c.publicRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var result struct {
		Bids []struct {
			Price               string `json:"price"`
			RemainingBaseAmount string `json:"remaining_base_amount"`
		} `json:"bids"`
		Asks []struct {
			Price               string `json:"price"`
			RemainingBaseAmount string `json:"remaining_base_amount"`
		} `json:"asks"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	ob := &exchange.Orderbook{
		Timestamp: time.Now(),
	}
	ob.UpdatedAt = time.Now()

	for _, bid := range result.Bids {
		price, _ := strconv.ParseFloat(bid.Price, 64)
		qty, _ := strconv.ParseFloat(bid.RemainingBaseAmount, 64)
		qty /= 1e6 // 6 decimals
		ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: price, Quantity: qty})
	}

	for _, ask := range result.Asks {
		price, _ := strconv.ParseFloat(ask.Price, 64)
		qty, _ := strconv.ParseFloat(ask.RemainingBaseAmount, 64)
		qty /= 1e6 // 6 decimals
		ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: price, Quantity: qty})
	}

	return ob, nil
}

// getAllBalances 전체 잔고 조회
func (c *Client) getAllBalances() (map[string]*exchange.Balance, error) {
	resp, err := c.signedRequest("GET", "/api/v1/account", map[string]interface{}{
		"by":    "index",
		"value": strconv.Itoa(c.accountIndex),
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Accounts []struct {
			Assets []struct {
				Symbol        string `json:"symbol"`
				Balance       string `json:"balance"`
				LockedBalance string `json:"locked_balance"`
			} `json:"assets"`
			AvailableBalance string `json:"available_balance"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}

	balances := make(map[string]*exchange.Balance)

	if len(result.Accounts) > 0 {
		account := result.Accounts[0]

		for _, asset := range account.Assets {
			balance, err := strconv.ParseFloat(asset.Balance, 64)
			if err != nil {
				log.Printf("[WARN] Lighter ParseFloat failed for balance %q (asset=%s): %v", asset.Balance, asset.Symbol, err)
			}
			locked, err := strconv.ParseFloat(asset.LockedBalance, 64)
			if err != nil {
				log.Printf("[WARN] Lighter ParseFloat failed for lockedBalance %q (asset=%s): %v", asset.LockedBalance, asset.Symbol, err)
			}

			balances[strings.ToUpper(asset.Symbol)] = &exchange.Balance{
				Currency: strings.ToUpper(asset.Symbol),
				Free:     balance - locked,
				Used:     locked,
				Total:    balance,
			}
		}

		if account.AvailableBalance != "" {
			totalValue, err := strconv.ParseFloat(account.AvailableBalance, 64)
			if err != nil {
				log.Printf("[WARN] Lighter ParseFloat failed for availableBalance %q: %v", account.AvailableBalance, err)
			}
			balances["TOTAL_VALUE"] = &exchange.Balance{
				Currency: "TOTAL_VALUE",
				Free:     totalValue,
				Total:    totalValue,
			}
		}
	}

	return balances, nil
}

// ========== 유틸리티 함수 ==========

// formatCoin 심볼을 Lighter 코인 형식으로 변환 (BTCUSDT -> BTC)
func formatCoin(symbol string) string {
	symbol = strings.ToUpper(symbol)
	symbol = strings.Replace(symbol, "USDT", "", -1)
	symbol = strings.Replace(symbol, "USDC", "", -1)
	symbol = strings.Replace(symbol, "/", "", -1)
	symbol = strings.Replace(symbol, "-PERP", "", -1)
	return symbol
}

