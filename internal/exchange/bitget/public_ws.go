// Package bitget - Bitget V2 Public WebSocket (spot + USDT-M futures 공개 시장 데이터)
// 단일 URL: wss://ws.bitget.com/v2/ws/public
// instType 으로 SPOT / USDT-FUTURES 구분.
package bitget

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
	publicWSURL     = "wss://ws.bitget.com/v2/ws/public"
	pingIntervalWS  = 30 * time.Second
	dialTimeoutWS   = 10 * time.Second
)

// instTypeOf MarketType → Bitget V2 instType 문자열.
func instTypeOf(mt gap.MarketType) string {
	if mt == gap.MarketFutures {
		return "USDT-FUTURES"
	}
	return "SPOT"
}

// marketTypeFromInstType instType 문자열 → MarketType.
func marketTypeFromInstType(instType string) gap.MarketType {
	if instType == "USDT-FUTURES" {
		return gap.MarketFutures
	}
	return gap.MarketSpot
}

// obState books15 snapshot/update 로컬 상태.
// price string → qty float64 (qty==0 이면 해당 레벨 삭제).
type obState struct {
	bids map[string]float64
	asks map[string]float64
}

// PublicWSClient Bitget V2 공개 시장 데이터 WS 클라이언트.
// gap.PublicWSClient 인터페이스 구현.
// 단일 WS 연결에서 spot + futures 둘 다 처리.
type PublicWSClient struct {
	writeMu sync.Mutex      // WriteJSON 직렬화
	conn    *websocket.Conn // 단일 연결

	connected     bool
	sessionCancel context.CancelFunc
	connMu        sync.RWMutex

	// books15 로컬 상태: "SPOT:BTCUSDT" 또는 "USDT-FUTURES:BTCUSDT" → obState
	obStates   map[string]*obState
	obStatesMu sync.Mutex

	// 콜백
	onTicker    func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)
	onOrderbook func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)
	cbMu        sync.RWMutex
}

// NewPublicWSClient 새 Bitget 공개 WS 클라이언트 생성.
func NewPublicWSClient() *PublicWSClient {
	return &PublicWSClient{
		obStates: make(map[string]*obState),
	}
}

// Name gap.PublicWSClient 인터페이스 구현.
func (c *PublicWSClient) Name() string { return "bitget" }

// Connect WS 연결 시작 + readLoop + pingLoop 구동.
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

	// 재연결 시 오더북 상태 초기화 (서버에서 snapshot 재전송)
	c.obStatesMu.Lock()
	c.obStates = make(map[string]*obState)
	c.obStatesMu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeoutWS)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, publicWSURL, http.Header{})
	if err != nil {
		return fmt.Errorf("[Bitget PublicWS] 연결 실패: %w", err)
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)

	c.connMu.Lock()
	c.conn = conn
	c.connected = true
	c.sessionCancel = sessionCancel
	c.connMu.Unlock()

	logger.Info("[Bitget PublicWS] 연결됨")

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

// IsConnected 연결 상태 확인.
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

// toNative coin → Bitget native ("ETH" → "ETHUSDT"). SPOT/USDT-FUTURES 동일.
// TODO: 1000x 계열 은 symbol_registry 연동 검토.
func toNative(coin string) string { return strings.ToUpper(coin) + "USDT" }

// fromNative native → coin ("ETHUSDT" → "ETH").
func fromNative(s string) string {
	return strings.TrimSuffix(strings.ToUpper(s), "USDT")
}

// SubscribeTicker ticker 채널 구독. bidPr/askPr 필드 기반 best bid/ask.
// symbol 은 coin 단위 (예: "ETH"). 내부에서 "ETHUSDT" 로 변환.
func (c *PublicWSClient) SubscribeTicker(symbol string, marketType gap.MarketType) error {
	return c.sendSub("ticker", instTypeOf(marketType), toNative(symbol))
}

// UnsubscribeTicker ticker 채널 구독 해제.
func (c *PublicWSClient) UnsubscribeTicker(symbol string, marketType gap.MarketType) error {
	return c.sendUnsub("ticker", instTypeOf(marketType), toNative(symbol))
}

// SubscribeOrderbook books15 채널 구독. snapshot + update 병합.
func (c *PublicWSClient) SubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	return c.sendSub("books15", instTypeOf(marketType), toNative(symbol))
}

// UnsubscribeOrderbook books15 채널 구독 해제.
func (c *PublicWSClient) UnsubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	return c.sendUnsub("books15", instTypeOf(marketType), toNative(symbol))
}

// ─── 내부 메서드 ─────────────────────────────────────────────────────────────

func (c *PublicWSClient) sendSub(channel, instType, instId string) error {
	return c.writeJSON(map[string]interface{}{
		"op": "subscribe",
		"args": []map[string]string{
			{"instType": instType, "channel": channel, "instId": instId},
		},
	})
}

func (c *PublicWSClient) sendUnsub(channel, instType, instId string) error {
	return c.writeJSON(map[string]interface{}{
		"op": "unsubscribe",
		"args": []map[string]string{
			{"instType": instType, "channel": channel, "instId": instId},
		},
	})
}

func (c *PublicWSClient) writeJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return fmt.Errorf("[Bitget PublicWS] 연결 없음")
	}
	return conn.WriteJSON(v)
}

// readLoop 메시지 수신 루프.
func (c *PublicWSClient) readLoop(conn *websocket.Conn, ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				logger.Warnf("[Bitget PublicWS] 읽기 오류: %v", err)
			}
			c.connMu.Lock()
			c.connected = false
			c.connMu.Unlock()
			return
		}

		// Bitget V2 ping/pong: 서버가 "pong" 텍스트로 응답
		if string(msg) == "pong" {
			continue
		}

		c.handleMessage(msg)
	}
}

// pingLoop 30초 간격으로 ping 전송.
func (c *PublicWSClient) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingIntervalWS)
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
					logger.Warnf("[Bitget PublicWS] ping 실패: %v", err)
				}
			}
			c.writeMu.Unlock()
		}
	}
}

// v2WsMsg Bitget V2 WS 공통 메시지 구조.
type v2WsMsg struct {
	Action string `json:"action"` // "snapshot" | "update"
	Arg    struct {
		InstType string `json:"instType"`
		Channel  string `json:"channel"`
		InstId   string `json:"instId"`
	} `json:"arg"`
	Data  json.RawMessage `json:"data"`
	Event string          `json:"event"` // 구독 응답 시 비어있지 않음
}

// tickerData ticker 채널 데이터 항목.
type tickerData struct {
	InstId string `json:"instId"`
	BidPr  string `json:"bidPr"`
	AskPr  string `json:"askPr"`
	Ts     string `json:"ts"`
}

// booksData books15 채널 데이터 항목.
type booksData struct {
	Bids [][]string `json:"bids"` // [[price, qty], ...]
	Asks [][]string `json:"asks"`
	Ts   string     `json:"ts"`
}

// handleMessage 수신 메시지 분기.
func (c *PublicWSClient) handleMessage(raw []byte) {
	var msg v2WsMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// 구독 응답, 에러 이벤트 무시
	if msg.Event != "" {
		return
	}
	// action 없으면 무시
	if msg.Action != "snapshot" && msg.Action != "update" {
		return
	}
	if msg.Data == nil {
		return
	}

	mt := marketTypeFromInstType(msg.Arg.InstType)

	switch msg.Arg.Channel {
	case "ticker":
		c.handleTicker(msg.Arg.InstId, mt, msg.Data)
	case "books15":
		c.handleOrderbook(msg.Arg.InstId, msg.Arg.InstType, mt, msg.Action, msg.Data)
	}
}

// handleTicker ticker 채널 처리 → onTicker 발사.
func (c *PublicWSClient) handleTicker(instId string, mt gap.MarketType, rawData json.RawMessage) {
	var items []tickerData
	if err := json.Unmarshal(rawData, &items); err != nil || len(items) == 0 {
		return
	}

	d := items[0]
	native := d.InstId
	if native == "" {
		native = instId
	}

	bid, _ := strconv.ParseFloat(d.BidPr, 64)
	ask, _ := strconv.ParseFloat(d.AskPr, 64)
	if bid <= 0 && ask <= 0 {
		return
	}

	tsMs, _ := strconv.ParseInt(d.Ts, 10, 64)
	var ts time.Time
	if tsMs > 0 {
		ts = time.UnixMilli(tsMs)
	} else {
		ts = time.Now()
	}

	// native("ETHUSDT") → coin("ETH") 역변환
	coin := fromNative(native)
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

// handleOrderbook books15 채널 처리. snapshot/update inline 병합 → onOrderbook 발사.
func (c *PublicWSClient) handleOrderbook(instId, instType string, mt gap.MarketType, action string, rawData json.RawMessage) {
	var items []booksData
	if err := json.Unmarshal(rawData, &items); err != nil || len(items) == 0 {
		return
	}

	d := items[0]
	key := instType + ":" + instId

	c.obStatesMu.Lock()

	state, exists := c.obStates[key]
	if action == "snapshot" || !exists {
		// snapshot: 전체 교체
		state = &obState{
			bids: make(map[string]float64, len(d.Bids)),
			asks: make(map[string]float64, len(d.Asks)),
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
		c.obStates[key] = state
	} else {
		// update: qty==0 이면 삭제, qty>0 이면 추가/갱신
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
	var ts time.Time
	if tsMs > 0 {
		ts = time.UnixMilli(tsMs)
	} else {
		ts = time.Now()
	}

	// native("ETHUSDT") → coin("ETH") 역변환
	coin := fromNative(instId)
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
