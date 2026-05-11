// Package hyperliquid provides Hyperliquid DEX API client
// Hyperliquid는 Arbitrum 기반의 탈중앙화 선물 거래소 (현물 없음)
package hyperliquid

import (
	"bytes"
	"context"
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
	MainnetURL = "https://api.hyperliquid.xyz"
	TestnetURL = "https://api.hyperliquid-testnet.xyz"
)

// Client Hyperliquid REST API 클라이언트
type Client struct {
	address   string // wallet address (from privateKey or explicitly set)
	baseURL   string
	client    *http.Client
	connected atomic.Bool
	metaCache map[string]struct{} // coin -> exists
}

// NewClient 새 클라이언트 생성
// walletAddress: 지갑 주소 (선택, 빈 문자열이면 privateKey에서 추출)
// privateKey: 이더리움 개인키 (0x 접두사 포함 또는 미포함)
// testnet: true면 테스트넷 사용
func NewClient(walletAddress string, privateKey string, testnet bool) (*Client, error) {
	// 0x 접두사 제거
	privateKey = strings.TrimPrefix(privateKey, "0x")

	// 개인키 파싱 → address 추출에만 사용
	privKey, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("개인키 파싱 실패: %w", err)
	}

	// 지갑 주소: 명시적으로 주어지면 사용, 아니면 privateKey에서 추출
	address := walletAddress
	if address == "" {
		address = crypto.PubkeyToAddress(privKey.PublicKey).Hex()
	}

	baseURL := MainnetURL
	if testnet {
		baseURL = TestnetURL
	}

	return &Client{
		address: address,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		metaCache: make(map[string]struct{}),
	}, nil
}

// Connect 연결 및 메타 정보 로드
func (c *Client) Connect(ctx context.Context) error {
	// 메타 정보 로드
	if err := c.loadMeta(); err != nil {
		return fmt.Errorf("Hyperliquid 연결 실패: %w", err)
	}
	c.connected.Store(true)
	return nil
}

// IsConnected 연결 상태 확인
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// loadMeta 메타 정보 로드 (네이티브 perps + HIP-3 builder-deployed perps)
func (c *Client) loadMeta() error {
	// 1. 네이티브 perps 메타 로드
	resp, err := c.infoRequest(map[string]interface{}{
		"type": "meta",
	})
	if err != nil {
		return err
	}

	var meta struct {
		Universe []struct {
			Name         string `json:"name"`
			SzDecimals   int    `json:"szDecimals"`
			MaxLeverage  int    `json:"maxLeverage"`
			OnlyIsolated bool   `json:"onlyIsolated"`
		} `json:"universe"`
	}
	if err := json.Unmarshal(resp, &meta); err != nil {
		return err
	}

	// universe 배열 순서가 곧 asset index
	for _, asset := range meta.Universe {
		c.metaCache[asset.Name] = struct{}{}
	}

	// 2. HIP-3 (builder-deployed perps) 로드 — 실패해도 네이티브 마켓은 정상 작동.
	// #C3 fix: 기존 `_ = err`는 silent drop. HIP-3 메타 로드 실패는 해당 perp 심볼 청산 시
	// 잘못된 marketIndex 사용 위험 → 최소 Warn 로그로 visible 처리.
	if err := c.loadHIP3Meta(); err != nil {
		log.Printf("[WARN] Hyperliquid HIP3 메타 로드 실패 — 네이티브 마켓만 사용, HIP-3 perp 청산 시 잘못된 인덱스 위험: %v", err)
	}

	return nil
}

// loadHIP3Meta HIP-3 builder-deployed perps 메타 정보 로드
func (c *Client) loadHIP3Meta() error {
	// 1. allMids 조회하여 HIP-3 마켓 인덱스 수집 ("@숫자" 형식)
	midsResp, err := c.infoRequest(map[string]interface{}{
		"type": "allMids",
	})
	if err != nil {
		return fmt.Errorf("allMids 조회 실패: %w", err)
	}

	var allMids map[string]string
	if err := json.Unmarshal(midsResp, &allMids); err != nil {
		return fmt.Errorf("allMids 파싱 실패: %w", err)
	}

	// "@숫자" 형식 키 수집 = HIP-3 마켓
	hip3Indices := make(map[int]string) // spotTokenIndex -> midPrice
	for coin, price := range allMids {
		if strings.HasPrefix(coin, "@") {
			idxStr := coin[1:]
			idx, err := strconv.Atoi(idxStr)
			if err == nil {
				hip3Indices[idx] = price
			}
		}
	}

	if len(hip3Indices) == 0 {
		return nil
	}

	// 2. spotMeta 조회하여 토큰 정보 매핑
	spotResp, err := c.infoRequest(map[string]interface{}{
		"type": "spotMeta",
	})
	if err != nil {
		return fmt.Errorf("spotMeta 조회 실패: %w", err)
	}

	var spotMeta struct {
		Tokens []struct {
			Name  string `json:"name"`
			Index int    `json:"index"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(spotResp, &spotMeta); err != nil {
		return fmt.Errorf("spotMeta 파싱 실패: %w", err)
	}

	indexMap := make(map[int]string) // tokenIndex -> name
	for _, token := range spotMeta.Tokens {
		indexMap[token.Index] = token.Name
	}

	// 3. HIP-3 마켓을 metaCache에 추가
	for idx := range hip3Indices {
		name, ok := indexMap[idx]
		if !ok {
			continue
		}
		deployerCoin := "xyz:" + name
		c.metaCache[deployerCoin] = struct{}{}
	}

	return nil
}

// infoRequest Info API 요청 (서명 불필요)
func (c *Client) infoRequest(payload map[string]interface{}) ([]byte, error) {
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", c.baseURL+"/info", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

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
	coin := c.ResolveCoin(symbol)

	// 1. allMids에서 가격 조회 시도
	resp, err := c.infoRequest(map[string]interface{}{
		"type": "allMids",
	})

	var price float64
	if err == nil {
		var mids map[string]string
		if err := json.Unmarshal(resp, &mids); err == nil {
			if priceStr, ok := mids[coin]; ok {
				var parseErr error
				price, parseErr = strconv.ParseFloat(priceStr, 64)
				if parseErr != nil {
					log.Printf("[WARN] Hyperliquid ParseFloat failed for mid price %q (coin=%s): %v", priceStr, coin, parseErr)
				}
			}
		}
	}

	// 2. allMids에 없으면 l2Book에서 직접 가격 조회
	if price == 0 {
		ob, err := c.GetFuturesOrderbook(ctx, coin)
		if err != nil {
			return nil, fmt.Errorf("티커를 찾을 수 없음: %s (allMids 및 l2Book 모두 실패)", coin)
		}
		bid := ob.BestBid()
		ask := ob.BestAsk()
		if bid > 0 && ask > 0 {
			price = (bid + ask) / 2
		} else if bid > 0 {
			price = bid
		} else if ask > 0 {
			price = ask
		}
		return &exchange.Ticker{
			Symbol:    coin,
			Price:     price,
			Bid:       bid,
			Ask:       ask,
			Timestamp: time.Now(),
		}, nil
	}

	// 3. allMids에서 가격을 찾았으면, 오더북에서 bid/ask도 조회
	ob, err := c.GetFuturesOrderbook(ctx, coin)
	if err != nil {
		return &exchange.Ticker{
			Symbol:    coin,
			Price:     price,
			Timestamp: time.Now(),
		}, nil
	}

	return &exchange.Ticker{
		Symbol:    coin,
		Price:     price,
		Bid:       ob.BestBid(),
		Ask:       ob.BestAsk(),
		Timestamp: time.Now(),
	}, nil
}

// GetFuturesOrderbook 선물 오더북 조회
func (c *Client) GetFuturesOrderbook(ctx context.Context, symbol string) (*exchange.Orderbook, error) {
	coin := c.ResolveCoin(symbol)

	resp, err := c.infoRequest(map[string]interface{}{
		"type": "l2Book",
		"coin": coin,
	})
	if err != nil {
		return nil, err
	}

	var book struct {
		Levels [][]struct {
			Px string `json:"px"`
			Sz string `json:"sz"`
			N  int    `json:"n"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(resp, &book); err != nil {
		return nil, err
	}

	ob := &exchange.Orderbook{
		Symbol:    coin,
		Timestamp: time.Now(),
	}
	ob.UpdatedAt = time.Now()

	if len(book.Levels) >= 2 {
		for _, level := range book.Levels[0] {
			price, _ := strconv.ParseFloat(level.Px, 64)
			qty, _ := strconv.ParseFloat(level.Sz, 64)
			ob.Bids = append(ob.Bids, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
		for _, level := range book.Levels[1] {
			price, _ := strconv.ParseFloat(level.Px, 64)
			qty, _ := strconv.ParseFloat(level.Sz, 64)
			ob.Asks = append(ob.Asks, exchange.OrderbookEntry{Price: price, Quantity: qty})
		}
	}

	return ob, nil
}

// GetFuturesBalance 선물 잔고 조회 (USDC)
func (c *Client) GetFuturesBalance(ctx context.Context) (*exchange.Balance, error) {
	resp, err := c.infoRequest(map[string]interface{}{
		"type": "clearinghouseState",
		"user": c.address,
	})
	if err != nil {
		return nil, err
	}

	var state struct {
		MarginSummary struct {
			AccountValue    string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
		} `json:"marginSummary"`
	}
	if err := json.Unmarshal(resp, &state); err != nil {
		return nil, err
	}

	total, err := strconv.ParseFloat(state.MarginSummary.AccountValue, 64)
	if err != nil {
		log.Printf("[WARN] Hyperliquid ParseFloat failed for accountValue %q: %v", state.MarginSummary.AccountValue, err)
	}
	used, err := strconv.ParseFloat(state.MarginSummary.TotalMarginUsed, 64)
	if err != nil {
		log.Printf("[WARN] Hyperliquid ParseFloat failed for totalMarginUsed %q: %v", state.MarginSummary.TotalMarginUsed, err)
	}

	return &exchange.Balance{
		Currency: "USDC",
		Free:     total - used,
		Used:     used,
		Total:    total,
	}, nil
}

// GetPosition 선물 포지션 조회
func (c *Client) GetPosition(ctx context.Context, symbol string) (*exchange.Position, error) {
	coin := formatCoin(symbol)

	resp, err := c.infoRequest(map[string]interface{}{
		"type": "clearinghouseState",
		"user": c.address,
	})
	if err != nil {
		return nil, err
	}

	var state struct {
		AssetPositions []struct {
			Position struct {
				Coin          string `json:"coin"`
				Szi           string `json:"szi"`
				EntryPx       string `json:"entryPx"`
				PositionValue string `json:"positionValue"`
				UnrealizedPnl string `json:"unrealizedPnl"`
				Leverage      struct {
					Type  string `json:"type"`
					Value int    `json:"value"`
				} `json:"leverage"`
				LiquidationPx string `json:"liquidationPx"`
			} `json:"position"`
		} `json:"assetPositions"`
	}
	if err := json.Unmarshal(resp, &state); err != nil {
		return nil, err
	}

	for _, ap := range state.AssetPositions {
		if ap.Position.Coin == coin {
			size, parseErr := strconv.ParseFloat(ap.Position.Szi, 64)
			if parseErr != nil {
				log.Printf("[WARN] Hyperliquid ParseFloat failed for position size %q (coin=%s): %v", ap.Position.Szi, coin, parseErr)
			}
			entryPrice, parseErr := strconv.ParseFloat(ap.Position.EntryPx, 64)
			if parseErr != nil {
				log.Printf("[WARN] Hyperliquid ParseFloat failed for entryPx %q (coin=%s): %v", ap.Position.EntryPx, coin, parseErr)
			}
			unrealizedPL, parseErr := strconv.ParseFloat(ap.Position.UnrealizedPnl, 64)
			if parseErr != nil {
				log.Printf("[WARN] Hyperliquid ParseFloat failed for unrealizedPnl %q (coin=%s): %v", ap.Position.UnrealizedPnl, coin, parseErr)
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
				Leverage:     ap.Position.Leverage.Value,
			}, nil
		}
	}

	return &exchange.Position{Symbol: coin}, nil
}

// GetAllPositions 전체 선물 포지션 조회
func (c *Client) GetAllPositions(ctx context.Context) ([]*exchange.Position, error) {
	resp, err := c.infoRequest(map[string]interface{}{
		"type": "clearinghouseState",
		"user": c.address,
	})
	if err != nil {
		return nil, err
	}

	var state struct {
		AssetPositions []struct {
			Position struct {
				Coin          string `json:"coin"`
				Szi           string `json:"szi"`
				EntryPx       string `json:"entryPx"`
				PositionValue string `json:"positionValue"`
				UnrealizedPnl string `json:"unrealizedPnl"`
				Leverage      struct {
					Type  string `json:"type"`
					Value int    `json:"value"`
				} `json:"leverage"`
			} `json:"position"`
		} `json:"assetPositions"`
	}
	if err := json.Unmarshal(resp, &state); err != nil {
		return nil, err
	}

	var positions []*exchange.Position
	for _, ap := range state.AssetPositions {
		size, parseErr := strconv.ParseFloat(ap.Position.Szi, 64)
		if parseErr != nil {
			log.Printf("[WARN] Hyperliquid GetAllPositions ParseFloat failed for szi %q (coin=%s): %v", ap.Position.Szi, ap.Position.Coin, parseErr)
			continue
		}
		if size == 0 {
			continue
		}

		entryPrice, parseErr := strconv.ParseFloat(ap.Position.EntryPx, 64)
		if parseErr != nil {
			log.Printf("[WARN] Hyperliquid GetAllPositions ParseFloat failed for entryPx %q (coin=%s): %v", ap.Position.EntryPx, ap.Position.Coin, parseErr)
		}
		unrealizedPL, parseErr := strconv.ParseFloat(ap.Position.UnrealizedPnl, 64)
		if parseErr != nil {
			log.Printf("[WARN] Hyperliquid GetAllPositions ParseFloat failed for unrealizedPnl %q (coin=%s): %v", ap.Position.UnrealizedPnl, ap.Position.Coin, parseErr)
		}

		side := "long"
		if size < 0 {
			side = "short"
			size = -size
		}

		// mark = positionValue / abs(szi)
		markPrice := entryPrice
		posVal, parseErr := strconv.ParseFloat(ap.Position.PositionValue, 64)
		if parseErr == nil && size > 0 {
			markPrice = posVal / size
		}

		positions = append(positions, &exchange.Position{
			Symbol:       ap.Position.Coin,
			Side:         side,
			Size:         size,
			EntryPrice:   entryPrice,
			MarkPrice:    markPrice,
			UnrealizedPL: unrealizedPL,
			Leverage:     ap.Position.Leverage.Value,
		})
	}

	if positions == nil {
		positions = []*exchange.Position{}
	}
	return positions, nil
}

// ========== 유틸리티 함수 ==========

// formatCoin 심볼을 Hyperliquid 코인 형식으로 변환 (BTCUSDT -> BTC)
func formatCoin(symbol string) string {
	if strings.Contains(symbol, ":") {
		return symbol
	}
	symbol = strings.ToUpper(symbol)
	symbol = strings.Replace(symbol, "USDT", "", -1)
	symbol = strings.Replace(symbol, "USDC", "", -1)
	symbol = strings.Replace(symbol, "/", "", -1)
	symbol = strings.Replace(symbol, ":USDT", "", -1)
	symbol = strings.Replace(symbol, "-PERP", "", -1)
	return symbol
}

// ResolveCoin 심볼을 metaCache에서 resolve (HIP-3 자동 매핑 포함)
func (c *Client) ResolveCoin(symbol string) string {
	coin := formatCoin(symbol)

	// 1. 정확히 매칭
	if _, ok := c.metaCache[coin]; ok {
		return coin
	}

	// 2. HIP-3 검색
	suffix := ":" + strings.ToUpper(coin)
	for key := range c.metaCache {
		if strings.HasSuffix(strings.ToUpper(key), suffix) {
			return key
		}
	}

	// 3. 알려진 deployer 패턴으로 l2Book 검증
	knownDeployers := []string{"xyz"}
	for _, deployer := range knownDeployers {
		candidate := deployer + ":" + coin
		resp, err := c.infoRequest(map[string]interface{}{
			"type": "l2Book",
			"coin": candidate,
		})
		if err != nil {
			continue
		}
		var book struct {
			Levels [][]json.RawMessage `json:"levels"`
		}
		if err := json.Unmarshal(resp, &book); err != nil {
			continue
		}
		if len(book.Levels) >= 2 && (len(book.Levels[0]) > 0 || len(book.Levels[1]) > 0) {
			c.metaCache[candidate] = struct{}{}
			return candidate
		}
	}

	return coin
}

