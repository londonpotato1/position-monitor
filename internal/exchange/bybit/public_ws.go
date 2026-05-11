// Package bybit - Bybit V5 Public WebSocket (spot + linear 공개 시장 데이터)
package bybit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/londonpotato1/position-monitor/internal/gap"
	"github.com/londonpotato1/position-monitor/pkg/logger"

	"github.com/gorilla/websocket"
)

const (
	spotPublicWSURL   = "wss://stream.bybit.com/v5/public/spot"
	linearPublicWSURL = "wss://stream.bybit.com/v5/public/linear"
	pingIntervalPub   = 20 * time.Second
)

// obState orderbook.50 snapshot/delta 로컬 상태.
// price string → qty float64 (qty==0 이면 해당 레벨 삭제).
type obState struct {
	bids map[string]float64
	asks map[string]float64
}

// PublicWSClient Bybit V5 공개 시장 데이터 WS 클라이언트.
// spot(wss://.../spot) + linear(wss://.../linear) 두 연결을 단일 구조체에서 관리.
// gap.PublicWSClient 인터페이스 구현.
type PublicWSClient struct {
	// 쓰기 락: 두 연결에 대한 WriteJSON 직렬화
	spotMu   sync.Mutex
	linearMu sync.Mutex

	spotConn   *websocket.Conn
	linearConn *websocket.Conn

	// 연결 상태 (readLoop 종료 시 false로 세팅)
	spotConnected   bool
	linearConnected bool
	connMu          sync.RWMutex

	// sessionCancel: readLoop + pingLoop 생명주기 제어.
	sessionCancel context.CancelFunc

	// orderbook.50 로컬 state: "spot:BTCUSDT" 또는 "linear:BTCUSDT" → obState
	obStates   map[string]*obState
	obStatesMu sync.Mutex

	// 콜백 (Manager가 Start 전에 SetOn* 으로 주입)
	onTicker    func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)
	onOrderbook func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)
	cbMu        sync.RWMutex

	// 구독 목록 복원용 (marketType 별로 분리)
	spotTickerSubs   map[string]struct{} // symbol
	linearTickerSubs map[string]struct{} // symbol
	spotObSubs       map[string]struct{} // symbol
	linearObSubs     map[string]struct{} // symbol
	subsMu           sync.Mutex
}

// NewPublicWSClient 새 Bybit 공개 WS 클라이언트 생성.
func NewPublicWSClient() *PublicWSClient {
	return &PublicWSClient{
		obStates:         make(map[string]*obState),
		spotTickerSubs:   make(map[string]struct{}),
		linearTickerSubs: make(map[string]struct{}),
		spotObSubs:       make(map[string]struct{}),
		linearObSubs:     make(map[string]struct{}),
	}
}

// Name gap.PublicWSClient 인터페이스 구현. Manager의 clients map 키.
func (c *PublicWSClient) Name() string { return "bybit" }

// Connect spot + linear 두 연결 시작 및 readLoop + pingLoop 구동.
func (c *PublicWSClient) Connect(ctx context.Context) error {
	c.connMu.Lock()
	if c.sessionCancel != nil {
		c.sessionCancel()
		c.sessionCancel = nil
	}
	if c.spotConn != nil {
		_ = c.spotConn.Close()
		c.spotConn = nil
	}
	if c.linearConn != nil {
		_ = c.linearConn.Close()
		c.linearConn = nil
	}
	c.spotConnected = false
	c.linearConnected = false
	c.connMu.Unlock()

	// obStates 초기화 (재연결 시 서버에서 snapshot 재전송)
	c.obStatesMu.Lock()
	c.obStates = make(map[string]*obState)
	c.obStatesMu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	spotConn, _, err := websocket.DefaultDialer.DialContext(dialCtx, spotPublicWSURL, http.Header{})
	if err != nil {
		return fmt.Errorf("[Bybit PublicWS] spot 연결 실패: %w", err)
	}

	dialCtx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()

	linearConn, _, err := websocket.DefaultDialer.DialContext(dialCtx2, linearPublicWSURL, http.Header{})
	if err != nil {
		_ = spotConn.Close()
		return fmt.Errorf("[Bybit PublicWS] linear 연결 실패: %w", err)
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)

	c.connMu.Lock()
	c.spotConn = spotConn
	c.linearConn = linearConn
	c.spotConnected = true
	c.linearConnected = true
	c.sessionCancel = sessionCancel
	c.connMu.Unlock()

	logger.Info("[Bybit PublicWS] spot + linear 연결됨")

	go c.readLoop(spotConn, "spot", sessionCtx)
	go c.readLoop(linearConn, "linear", sessionCtx)
	go c.pingLoop(sessionCtx)

	return nil
}

// Disconnect 세션 goroutine 종료 + 두 연결 닫기.
func (c *PublicWSClient) Disconnect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.sessionCancel != nil {
		c.sessionCancel()
		c.sessionCancel = nil
	}
	if c.spotConn != nil {
		_ = c.spotConn.Close()
		c.spotConn = nil
	}
	if c.linearConn != nil {
		_ = c.linearConn.Close()
		c.linearConn = nil
	}
	c.spotConnected = false
	c.linearConnected = false
	return nil
}

// IsConnected spot AND linear 모두 연결 중일 때만 true.
func (c *PublicWSClient) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.spotConnected && c.linearConnected
}

// SetOnTicker 티커 콜백 설정.
func (c *PublicWSClient) SetOnTicker(fn func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)) {
	c.cbMu.Lock()
	c.onTicker = fn
	c.cbMu.Unlock()
}

// SetOnOrderbook 오더북 콜백 설정.
func (c *PublicWSClient) SetOnOrderbook(fn func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)) {
	c.cbMu.Lock()
	c.onOrderbook = fn
	c.cbMu.Unlock()
}

// toNative coin → Bybit native ("ETH" → "ETHUSDT"). spot/linear 동일.
// TODO: 1000x 계열 (SHIB, PEPE 등) 은 symbol_registry 연동 검토.
func toNative(coin string) string { return strings.ToUpper(coin) + "USDT" }

// fromNative native → coin ("ETHUSDT" → "ETH").
func fromNative(s string) string {
	return strings.TrimSuffix(strings.ToUpper(s), "USDT")
}

// SubscribeTicker orderbook.1.{native} 구독. marketType 에 따라 spot 또는 linear 연결.
// symbol 은 coin 단위 (예: "ETH"). 내부에서 "ETHUSDT" 로 변환.
func (c *PublicWSClient) SubscribeTicker(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	topic := fmt.Sprintf("orderbook.1.%s", toNative(coin))
	connType := connTypeOf(marketType)
	if err := c.sendSub(connType, topic); err != nil {
		return err
	}
	c.subsMu.Lock()
	if marketType == gap.MarketSpot {
		c.spotTickerSubs[coin] = struct{}{}
	} else {
		c.linearTickerSubs[coin] = struct{}{}
	}
	c.subsMu.Unlock()
	return nil
}

// UnsubscribeTicker orderbook.1.{native} 구독 해제.
func (c *PublicWSClient) UnsubscribeTicker(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	topic := fmt.Sprintf("orderbook.1.%s", toNative(coin))
	connType := connTypeOf(marketType)
	_ = c.sendUnsub(connType, topic)
	c.subsMu.Lock()
	if marketType == gap.MarketSpot {
		delete(c.spotTickerSubs, coin)
	} else {
		delete(c.linearTickerSubs, coin)
	}
	c.subsMu.Unlock()
	return nil
}

// SubscribeOrderbook orderbook.50.{native} 구독. marketType 에 따라 해당 연결.
func (c *PublicWSClient) SubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	topic := fmt.Sprintf("orderbook.50.%s", toNative(coin))
	connType := connTypeOf(marketType)
	if err := c.sendSub(connType, topic); err != nil {
		return err
	}
	c.subsMu.Lock()
	if marketType == gap.MarketSpot {
		c.spotObSubs[coin] = struct{}{}
	} else {
		c.linearObSubs[coin] = struct{}{}
	}
	c.subsMu.Unlock()
	return nil
}

// UnsubscribeOrderbook orderbook.50.{native} 구독 해제.
func (c *PublicWSClient) UnsubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	topic := fmt.Sprintf("orderbook.50.%s", toNative(coin))
	connType := connTypeOf(marketType)
	_ = c.sendUnsub(connType, topic)
	c.subsMu.Lock()
	if marketType == gap.MarketSpot {
		delete(c.spotObSubs, coin)
	} else {
		delete(c.linearObSubs, coin)
	}
	c.subsMu.Unlock()
	return nil
}

// connTypeOf MarketType → connType 문자열 변환.
func connTypeOf(mt gap.MarketType) string {
	if mt == gap.MarketFutures {
		return "linear"
	}
	return "spot"
}

// marketTypeOf connType 문자열 → MarketType 변환.
func marketTypeOf(connType string) gap.MarketType {
	if connType == "linear" {
		return gap.MarketFutures
	}
	return gap.MarketSpot
}

// ─── 내부 메서드 ────────────────────────────────────────────────────────────

func (c *PublicWSClient) sendSub(connType, topic string) error {
	return c.writeJSON(connType, map[string]interface{}{
		"op":   "subscribe",
		"args": []string{topic},
	})
}

func (c *PublicWSClient) sendUnsub(connType, topic string) error {
	return c.writeJSON(connType, map[string]interface{}{
		"op":   "unsubscribe",
		"args": []string{topic},
	})
}

// writeJSON 해당 연결에 JSON 직렬화 전송.
func (c *PublicWSClient) writeJSON(connType string, v interface{}) error {
	if connType == "spot" {
		c.spotMu.Lock()
		defer c.spotMu.Unlock()
		c.connMu.RLock()
		conn := c.spotConn
		c.connMu.RUnlock()
		if conn == nil {
			return fmt.Errorf("[Bybit PublicWS] spot 연결 없음")
		}
		return conn.WriteJSON(v)
	}
	c.linearMu.Lock()
	defer c.linearMu.Unlock()
	c.connMu.RLock()
	conn := c.linearConn
	c.connMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("[Bybit PublicWS] linear 연결 없음")
	}
	return conn.WriteJSON(v)
}

// readLoop 메시지 수신 루프.
func (c *PublicWSClient) readLoop(conn *websocket.Conn, connType string, ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				logger.Warnf("[Bybit PublicWS %s] 읽기 오류: %v", connType, err)
			}
			c.connMu.Lock()
			if connType == "spot" {
				c.spotConnected = false
			} else {
				c.linearConnected = false
			}
			c.connMu.Unlock()
			return
		}

		c.handleMessage(msg, connType)
	}
}

// pingLoop 20초 간격으로 spot + linear 양쪽에 ping 전송.
func (c *PublicWSClient) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingIntervalPub)
	defer ticker.Stop()

	ping := map[string]string{"op": "ping"}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.writeJSON("spot", ping); err != nil {
				logger.Warnf("[Bybit PublicWS spot] ping 실패: %v", err)
			}
			if err := c.writeJSON("linear", ping); err != nil {
				logger.Warnf("[Bybit PublicWS linear] ping 실패: %v", err)
			}
		}
	}
}

// obMsg Bybit orderbook 메시지 공통 구조체.
type obMsg struct {
	Topic string `json:"topic"`
	Type  string `json:"type"` // "snapshot" | "delta"
	Ts    int64  `json:"ts"`
	Data  struct {
		S string     `json:"s"` // symbol
		B [][]string `json:"b"` // bids [[price, qty], ...]
		A [][]string `json:"a"` // asks [[price, qty], ...]
		U int64      `json:"u"` // update id
	} `json:"data"`
}

// handleMessage 수신 메시지 분기 처리.
func (c *PublicWSClient) handleMessage(raw []byte, connType string) {
	var base struct {
		Op      string `json:"op"`
		Topic   string `json:"topic"`
		Success bool   `json:"success"`
	}
	if err := json.Unmarshal(raw, &base); err != nil {
		return
	}
	if base.Op == "pong" || base.Success {
		return
	}

	if !strings.HasPrefix(base.Topic, "orderbook.") {
		return
	}

	var msg obMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Debugf("[Bybit PublicWS %s] 파싱 오류: %v", connType, err)
		return
	}
	if msg.Data.S == "" {
		return
	}

	mt := marketTypeOf(connType)

	depth := extractDepth(msg.Topic)
	switch depth {
	case 1:
		c.handleTicker(&msg, mt)
	case 50:
		c.handleOrderbook(&msg, connType, mt)
	}
}

// extractDepth "orderbook.1.BTCUSDT" → 1, "orderbook.50.BTCUSDT" → 50
func extractDepth(topic string) int {
	parts := strings.SplitN(topic, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	d, _ := strconv.Atoi(parts[1])
	return d
}

// handleTicker orderbook.1 메시지에서 best bid/ask 추출 → onTicker 발사.
func (c *PublicWSClient) handleTicker(msg *obMsg, mt gap.MarketType) {
	var bid, ask float64
	if len(msg.Data.B) > 0 && len(msg.Data.B[0]) >= 1 {
		bid, _ = strconv.ParseFloat(msg.Data.B[0][0], 64)
	}
	if len(msg.Data.A) > 0 && len(msg.Data.A[0]) >= 1 {
		ask, _ = strconv.ParseFloat(msg.Data.A[0][0], 64)
	}
	if bid <= 0 && ask <= 0 {
		return
	}

	ts := time.UnixMilli(msg.Ts)
	// native("ETHUSDT") → coin("ETH") 역변환
	coin := fromNative(msg.Data.S)
	if coin == "" {
		return
	}

	c.cbMu.RLock()
	fn := c.onTicker
	c.cbMu.RUnlock()

	if fn != nil {
		fn(coin, mt, bid, ask, ts)
	}
}

// handleOrderbook orderbook.50 snapshot/delta merge → onOrderbook 발사.
func (c *PublicWSClient) handleOrderbook(msg *obMsg, connType string, mt gap.MarketType) {
	key := connType + ":" + msg.Data.S

	c.obStatesMu.Lock()

	state, exists := c.obStates[key]
	if msg.Type == "snapshot" || !exists {
		state = &obState{
			bids: make(map[string]float64),
			asks: make(map[string]float64),
		}
		for _, b := range msg.Data.B {
			if len(b) >= 2 {
				qty, _ := strconv.ParseFloat(b[1], 64)
				if qty > 0 {
					state.bids[b[0]] = qty
				}
			}
		}
		for _, a := range msg.Data.A {
			if len(a) >= 2 {
				qty, _ := strconv.ParseFloat(a[1], 64)
				if qty > 0 {
					state.asks[a[0]] = qty
				}
			}
		}
		c.obStates[key] = state
	} else {
		// delta: qty==0 → 삭제, qty>0 → 추가/갱신
		for _, b := range msg.Data.B {
			if len(b) >= 2 {
				qty, _ := strconv.ParseFloat(b[1], 64)
				if qty == 0 {
					delete(state.bids, b[0])
				} else {
					state.bids[b[0]] = qty
				}
			}
		}
		for _, a := range msg.Data.A {
			if len(a) >= 2 {
				qty, _ := strconv.ParseFloat(a[1], 64)
				if qty == 0 {
					delete(state.asks, a[0])
				} else {
					state.asks[a[0]] = qty
				}
			}
		}
	}

	bids := make([][2]float64, 0, len(state.bids))
	for priceStr, qty := range state.bids {
		price, _ := strconv.ParseFloat(priceStr, 64)
		bids = append(bids, [2]float64{price, qty})
	}
	asks := make([][2]float64, 0, len(state.asks))
	for priceStr, qty := range state.asks {
		price, _ := strconv.ParseFloat(priceStr, 64)
		asks = append(asks, [2]float64{price, qty})
	}

	c.obStatesMu.Unlock()

	sort.Slice(bids, func(i, j int) bool { return bids[i][0] > bids[j][0] })
	sort.Slice(asks, func(i, j int) bool { return asks[i][0] < asks[j][0] })

	ts := time.UnixMilli(msg.Ts)
	// native("ETHUSDT") → coin("ETH") 역변환
	coin := fromNative(msg.Data.S)
	if coin == "" {
		return
	}

	c.cbMu.RLock()
	fn := c.onOrderbook
	c.cbMu.RUnlock()

	if fn != nil {
		fn(coin, mt, bids, asks, ts)
	}
}

// compile-time 인터페이스 체크
var _ gap.PublicWSClient = (*PublicWSClient)(nil)
