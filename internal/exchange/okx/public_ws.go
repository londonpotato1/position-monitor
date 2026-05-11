// Package okx - OKX V5 Public WebSocket (spot + SWAP 공개 시장 데이터)
package okx

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
	publicWSURL    = "wss://ws.okx.com:8443/ws/v5/public"
	pubPingInterval = 20 * time.Second
)

// obState books 채널 snapshot/update 로컬 상태.
// price string → qty float64 (qty==0 이면 해당 레벨 삭제).
type obState struct {
	bids map[string]float64
	asks map[string]float64
}

// PublicWSClient OKX V5 공개 시장 데이터 WS 클라이언트.
// 단일 URL에서 spot(instType=SPOT) + SWAP(instType=SWAP) 동시 처리.
// gap.PublicWSClient 인터페이스 구현.
type PublicWSClient struct {
	writeMu sync.Mutex
	conn    *websocket.Conn

	connected     bool
	sessionCancel context.CancelFunc
	connMu        sync.RWMutex

	// books 채널 로컬 state: "BTC-USDT" 또는 "BTC-USDT-SWAP" → obState
	obStates   map[string]*obState
	obStatesMu sync.Mutex

	// 콜백 (Manager가 Start 전에 SetOn* 으로 주입)
	onTicker    func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)
	onOrderbook func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)
	cbMu        sync.RWMutex
}

// NewPublicWSClient 새 OKX 공개 WS 클라이언트 생성.
func NewPublicWSClient() *PublicWSClient {
	return &PublicWSClient{
		obStates: make(map[string]*obState),
	}
}

// Name gap.PublicWSClient 인터페이스 구현. Manager의 clients map 키.
func (c *PublicWSClient) Name() string { return "okx" }

// Connect WS 연결 시작 및 readLoop + pingLoop 구동.
func (c *PublicWSClient) Connect(ctx context.Context) error {
	c.connMu.Lock()
	if c.sessionCancel != nil {
		c.sessionCancel()
		c.sessionCancel = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	c.connMu.Unlock()

	// 재연결 시 서버에서 snapshot 재전송하므로 로컬 상태 초기화
	c.obStatesMu.Lock()
	c.obStates = make(map[string]*obState)
	c.obStatesMu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, publicWSURL, http.Header{})
	if err != nil {
		return fmt.Errorf("[OKX PublicWS] 연결 실패: %w", err)
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)

	c.connMu.Lock()
	c.conn = conn
	c.connected = true
	c.sessionCancel = sessionCancel
	c.connMu.Unlock()

	logger.Info("[OKX PublicWS] 연결됨")

	go c.readLoop(conn, sessionCtx)
	go c.pingLoop(sessionCtx)

	return nil
}

// Disconnect 세션 goroutine 종료 + 연결 닫기.
func (c *PublicWSClient) Disconnect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.sessionCancel != nil {
		c.sessionCancel()
		c.sessionCancel = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	return nil
}

// IsConnected 연결 중이면 true.
func (c *PublicWSClient) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.connected
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

// toNative coin → OKX native instId. spot "ETH-USDT", futures "ETH-USDT-SWAP".
// TODO: 1000x 계열 은 symbol_registry 연동 검토.
func toNative(coin string, mt gap.MarketType) string {
	base := strings.ToUpper(coin) + "-USDT"
	if mt == gap.MarketFutures {
		return base + "-SWAP"
	}
	return base
}

// fromNative native instId → coin ("ETH-USDT" → "ETH", "ETH-USDT-SWAP" → "ETH").
func fromNative(instID string) string {
	s := strings.ToUpper(instID)
	s = strings.TrimSuffix(s, "-SWAP")
	s = strings.TrimSuffix(s, "-USDT")
	return s
}

// SubscribeTicker books5 채널 구독 (5레벨 snapshot → best bid/ask 추출).
// symbol 은 coin 단위 (예: "ETH"). 내부에서 marketType 에 따라 "ETH-USDT" / "ETH-USDT-SWAP" 로 변환.
func (c *PublicWSClient) SubscribeTicker(symbol string, mt gap.MarketType) error {
	return c.sendOp("subscribe", "books5", toNative(symbol, mt))
}

// UnsubscribeTicker books5 채널 해제.
func (c *PublicWSClient) UnsubscribeTicker(symbol string, mt gap.MarketType) error {
	return c.sendOp("unsubscribe", "books5", toNative(symbol, mt))
}

// SubscribeOrderbook books 채널 구독 (400레벨 snapshot+update, 로컬 merge).
func (c *PublicWSClient) SubscribeOrderbook(symbol string, mt gap.MarketType) error {
	return c.sendOp("subscribe", "books", toNative(symbol, mt))
}

// UnsubscribeOrderbook books 채널 해제.
func (c *PublicWSClient) UnsubscribeOrderbook(symbol string, mt gap.MarketType) error {
	return c.sendOp("unsubscribe", "books", toNative(symbol, mt))
}

// ─── 내부 메서드 ────────────────────────────────────────────────────────────

func (c *PublicWSClient) sendOp(op, channel, instId string) error {
	msg := map[string]interface{}{
		"op": op,
		"args": []map[string]string{
			{"channel": channel, "instId": instId},
		},
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("[OKX PublicWS] 연결 없음")
	}
	return conn.WriteJSON(msg)
}

// readLoop 메시지 수신 루프.
func (c *PublicWSClient) readLoop(conn *websocket.Conn, ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				logger.Warnf("[OKX PublicWS] 읽기 오류: %v", err)
			}
			c.connMu.Lock()
			c.connected = false
			c.connMu.Unlock()
			return
		}

		// OKX raw text "pong"
		if string(raw) == "pong" {
			continue
		}

		c.handleMessage(raw)
	}
}

// pingLoop 20초 간격으로 raw text "ping" 전송.
func (c *PublicWSClient) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pubPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.writeMu.Lock()
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()
			if conn != nil {
				if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
					logger.Warnf("[OKX PublicWS] ping 실패: %v", err)
				}
			}
			c.writeMu.Unlock()
		}
	}
}

// okxWsMsg OKX 공개 WS 공통 메시지 구조.
type okxWsMsg struct {
	Arg struct {
		Channel string `json:"channel"`
		InstID  string `json:"instId"`
	} `json:"arg"`
	Action string            `json:"action"` // books: "snapshot" | "update"
	Data   []json.RawMessage `json:"data"`
	Event  string            `json:"event"` // "subscribe" | "error" 등
}

// handleMessage 수신 메시지 분기 처리.
func (c *PublicWSClient) handleMessage(raw []byte) {
	var msg okxWsMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// event 메시지(subscribe 확인, error 등)는 무시
	if msg.Event != "" {
		return
	}

	if len(msg.Data) == 0 || msg.Arg.InstID == "" {
		return
	}

	instID := msg.Arg.InstID
	mt := gap.MarketSpot
	if strings.HasSuffix(instID, "-SWAP") {
		mt = gap.MarketFutures
	}

	switch msg.Arg.Channel {
	case "books5":
		c.handleBooks5(instID, mt, msg.Data[0])
	case "books":
		c.handleBooks(instID, mt, msg.Action, msg.Data[0])
	}
}

// okxBookData OKX books/books5 data 항목.
type okxBookData struct {
	Bids [][]string `json:"bids"` // [[price, qty, deprecated, numOrders], ...]
	Asks [][]string `json:"asks"`
	Ts   string     `json:"ts"` // 밀리초 타임스탬프
}

// handleBooks5 books5 메시지 처리: 매번 전체 5레벨 snapshot → best bid/ask → onTicker 발사.
func (c *PublicWSClient) handleBooks5(instID string, mt gap.MarketType, rawData json.RawMessage) {
	var d okxBookData
	if err := json.Unmarshal(rawData, &d); err != nil {
		return
	}

	var bid, ask float64
	if len(d.Bids) > 0 && len(d.Bids[0]) >= 1 {
		bid, _ = strconv.ParseFloat(d.Bids[0][0], 64)
	}
	if len(d.Asks) > 0 && len(d.Asks[0]) >= 1 {
		ask, _ = strconv.ParseFloat(d.Asks[0][0], 64)
	}
	if bid <= 0 && ask <= 0 {
		return
	}

	tsMs, _ := strconv.ParseInt(d.Ts, 10, 64)
	ts := time.UnixMilli(tsMs)

	// native("ETH-USDT" / "ETH-USDT-SWAP") → coin("ETH") 역변환
	coin := fromNative(instID)
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

// handleBooks books 채널 snapshot/update merge → onOrderbook 발사.
func (c *PublicWSClient) handleBooks(instID string, mt gap.MarketType, action string, rawData json.RawMessage) {
	var d okxBookData
	if err := json.Unmarshal(rawData, &d); err != nil {
		return
	}

	c.obStatesMu.Lock()

	state, exists := c.obStates[instID]
	if action == "snapshot" || !exists {
		state = &obState{
			bids: make(map[string]float64),
			asks: make(map[string]float64),
		}
		for _, b := range d.Bids {
			if len(b) >= 2 {
				qty, _ := strconv.ParseFloat(b[1], 64)
				if qty > 0 {
					state.bids[b[0]] = qty
				}
			}
		}
		for _, a := range d.Asks {
			if len(a) >= 2 {
				qty, _ := strconv.ParseFloat(a[1], 64)
				if qty > 0 {
					state.asks[a[0]] = qty
				}
			}
		}
		c.obStates[instID] = state
	} else {
		// update: qty==0 → 삭제, qty>0 → 추가/갱신
		for _, b := range d.Bids {
			if len(b) >= 2 {
				qty, _ := strconv.ParseFloat(b[1], 64)
				if qty == 0 {
					delete(state.bids, b[0])
				} else {
					state.bids[b[0]] = qty
				}
			}
		}
		for _, a := range d.Asks {
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

	tsMs, _ := strconv.ParseInt(d.Ts, 10, 64)
	ts := time.UnixMilli(tsMs)

	// native("ETH-USDT" / "ETH-USDT-SWAP") → coin("ETH") 역변환
	coin := fromNative(instID)
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
