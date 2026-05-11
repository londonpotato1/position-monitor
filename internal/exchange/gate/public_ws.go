// Gate.io 공개 시장 데이터 WS 클라이언트 (gap.PublicWSClient 구현).
// Spot + Futures 두 WebSocket 연결을 단일 struct에서 관리한다.
//
// 심볼 규약: Subscribe/콜백 emit 모두 coin 단위 (예: "BTC").
// 내부에서 "BTC_USDT" 포맷으로 변환하여 Gate WS에 전송하고,
// 수신 시 "_USDT" suffix를 제거해 coin 기준으로 emit한다.
package gate

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/londonpotato1/position-monitor/internal/gap"
	"github.com/londonpotato1/position-monitor/pkg/logger"
)

const (
	gateSpotWSURL    = "wss://api.gateio.ws/ws/v4/"
	gateFuturesWSURL = "wss://fx-ws.gateio.ws/v4/ws/usdt"
	gatePingInterval = 15 * time.Second
)

// PublicWSClient gap.PublicWSClient 구현체.
// 내부적으로 Spot/Futures 두 WS 연결을 유지.
type PublicWSClient struct {
	onTicker    func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)
	onOrderbook func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)
	cbMu        sync.RWMutex

	// 재연결 복원용 구독 목록 (coin 단위, 예: "BTC")
	spotTickers    map[string]bool
	futTickers     map[string]bool
	spotOrderbooks map[string]bool
	futOrderbooks  map[string]bool
	subMu          sync.RWMutex

	spotConn    *websocket.Conn
	futuresConn *websocket.Conn
	connMu      sync.Mutex

	spotUp    atomic.Bool
	futuresUp atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc
	ctxMu  sync.Mutex

	running atomic.Bool
}

// NewPublicWSClient 새 클라이언트 생성.
func NewPublicWSClient() *PublicWSClient {
	return &PublicWSClient{
		spotTickers:    make(map[string]bool),
		futTickers:     make(map[string]bool),
		spotOrderbooks: make(map[string]bool),
		futOrderbooks:  make(map[string]bool),
	}
}

// Name gap.PublicWSClient 구현.
func (c *PublicWSClient) Name() string { return "gate" }

// SetOnTicker 콜백 등록.
func (c *PublicWSClient) SetOnTicker(fn func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)) {
	c.cbMu.Lock()
	c.onTicker = fn
	c.cbMu.Unlock()
}

// SetOnOrderbook 콜백 등록.
func (c *PublicWSClient) SetOnOrderbook(fn func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)) {
	c.cbMu.Lock()
	c.onOrderbook = fn
	c.cbMu.Unlock()
}

// IsConnected client 생애주기 상태. Connect 직후 true (비동기 dial 과 무관).
// Manager.runClient 의 폴링이 dial 완료 전에 평가되는 race 를 회피.
// 실제 dial up/down 복구는 connectLoop 의 3초 재시도가 담당.
func (c *PublicWSClient) IsConnected() bool {
	return c.running.Load()
}

// Connect spot + futures 고루틴 시작. 이미 실행 중이면 no-op.
func (c *PublicWSClient) Connect(ctx context.Context) error {
	c.ctxMu.Lock()
	if c.running.Load() {
		c.ctxMu.Unlock()
		return nil
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.running.Store(true)
	c.ctxMu.Unlock()

	go c.connectLoop("spot", gateSpotWSURL)
	go c.connectLoop("futures", gateFuturesWSURL)
	return nil
}

// Disconnect 연결 종료. Manager 재연결 사이클에서 호출.
func (c *PublicWSClient) Disconnect() error {
	c.ctxMu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.running.Store(false)
	c.ctxMu.Unlock()

	c.connMu.Lock()
	if c.spotConn != nil {
		_ = c.spotConn.Close()
		c.spotConn = nil
	}
	if c.futuresConn != nil {
		_ = c.futuresConn.Close()
		c.futuresConn = nil
	}
	c.connMu.Unlock()

	c.spotUp.Store(false)
	c.futuresUp.Store(false)
	return nil
}

// SubscribeTicker book_ticker 구독. symbol은 coin 단위 (예: "BTC").
func (c *PublicWSClient) SubscribeTicker(symbol string, marketType gap.MarketType) error {
	pair := symbol + "_USDT"
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		c.spotTickers[symbol] = true
		c.subMu.Unlock()
		c.writeMsg("spot", "spot.book_ticker", "subscribe", []string{pair})
	} else {
		c.futTickers[symbol] = true
		c.subMu.Unlock()
		c.writeMsg("futures", "futures.book_ticker", "subscribe", []string{pair})
	}
	return nil
}

// UnsubscribeTicker book_ticker 구독 해제.
func (c *PublicWSClient) UnsubscribeTicker(symbol string, marketType gap.MarketType) error {
	pair := symbol + "_USDT"
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		delete(c.spotTickers, symbol)
		c.subMu.Unlock()
		c.writeMsg("spot", "spot.book_ticker", "unsubscribe", []string{pair})
	} else {
		delete(c.futTickers, symbol)
		c.subMu.Unlock()
		c.writeMsg("futures", "futures.book_ticker", "unsubscribe", []string{pair})
	}
	return nil
}

// SubscribeOrderbook order_book 구독. symbol은 coin 단위 (예: "BTC").
func (c *PublicWSClient) SubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	pair := symbol + "_USDT"
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		c.spotOrderbooks[symbol] = true
		c.subMu.Unlock()
		// payload: [pair, levels, interval]
		c.writeMsg("spot", "spot.order_book", "subscribe", []string{pair, "20", "100ms"})
	} else {
		c.futOrderbooks[symbol] = true
		c.subMu.Unlock()
		c.writeMsg("futures", "futures.order_book", "subscribe", []string{pair, "20", "0"})
	}
	return nil
}

// UnsubscribeOrderbook order_book 구독 해제.
func (c *PublicWSClient) UnsubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	pair := symbol + "_USDT"
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		delete(c.spotOrderbooks, symbol)
		c.subMu.Unlock()
		c.writeMsg("spot", "spot.order_book", "unsubscribe", []string{pair, "20", "100ms"})
	} else {
		delete(c.futOrderbooks, symbol)
		c.subMu.Unlock()
		c.writeMsg("futures", "futures.order_book", "unsubscribe", []string{pair, "20", "0"})
	}
	return nil
}

// connectLoop 단일 market 연결. 끊기면 3초 후 재시도.
func (c *PublicWSClient) connectLoop(market, wsURL string) {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		c.singleConnect(market, wsURL)
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// singleConnect 1회 연결 시도 + readLoop. 끊기면 return.
func (c *PublicWSClient) singleConnect(market, wsURL string) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(c.ctx, wsURL, nil)
	if err != nil {
		logger.Warnf("[Gate WS %s] 연결 실패: %v", market, err)
		return
	}

	c.connMu.Lock()
	if market == "spot" {
		c.spotConn = conn
		c.spotUp.Store(true)
	} else {
		c.futuresConn = conn
		c.futuresUp.Store(true)
	}
	c.connMu.Unlock()

	logger.Infof("[Gate WS %s] 연결됨", market)

	// 연결 직후 pending 구독 복원
	c.resubscribeAll(market)

	c.readLoop(conn, market)

	c.connMu.Lock()
	if market == "spot" {
		c.spotConn = nil
		c.spotUp.Store(false)
	} else {
		c.futuresConn = nil
		c.futuresUp.Store(false)
	}
	c.connMu.Unlock()

	logger.Warnf("[Gate WS %s] 연결 끊김", market)
}

// resubscribeAll 연결 후 기존 구독 목록 재전송.
func (c *PublicWSClient) resubscribeAll(market string) {
	c.subMu.RLock()
	defer c.subMu.RUnlock()

	if market == "spot" {
		for coin := range c.spotTickers {
			c.writeMsg(market, "spot.book_ticker", "subscribe", []string{coin + "_USDT"})
		}
		for coin := range c.spotOrderbooks {
			c.writeMsg(market, "spot.order_book", "subscribe", []string{coin + "_USDT", "20", "100ms"})
		}
	} else {
		for coin := range c.futTickers {
			c.writeMsg(market, "futures.book_ticker", "subscribe", []string{coin + "_USDT"})
		}
		for coin := range c.futOrderbooks {
			c.writeMsg(market, "futures.order_book", "subscribe", []string{coin + "_USDT", "20", "0"})
		}
	}
}

// readLoop 메시지 수신 + 앱레벨 핑 유지. ctx 취소 또는 에러 시 return.
func (c *PublicWSClient) readLoop(conn *websocket.Conn, market string) {
	mt := gap.MarketSpot
	if market == "futures" {
		mt = gap.MarketFutures
	}

	pingCh := make(chan struct{})
	go func() {
		t := time.NewTicker(gatePingInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				pingMsg, _ := json.Marshal(map[string]any{
					"time":    time.Now().Unix(),
					"channel": market + ".ping",
				})
				c.connMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, pingMsg)
				c.connMu.Unlock()
				if err != nil {
					logger.Warnf("[Gate WS %s] ping 실패: %v", market, err)
					_ = conn.Close()
					return
				}
			case <-pingCh:
				return
			case <-c.ctx.Done():
				return
			}
		}
	}()
	defer close(pingCh)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Debugf("[Gate WS %s] 읽기 오류: %v", market, err)
			}
			return
		}

		c.dispatch(raw, mt)
	}
}

// gateMsg Gate WS 공통 래퍼.
type gateMsg struct {
	Channel string          `json:"channel"`
	Event   string          `json:"event"`
	Time    int64           `json:"time"`
	Result  json.RawMessage `json:"result"`
}

// dispatch 채널 분기.
func (c *PublicWSClient) dispatch(raw []byte, mt gap.MarketType) {
	var msg gateMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	// pong / ack 무시
	if msg.Event == "subscribe" || msg.Event == "unsubscribe" {
		return
	}
	if strings.HasSuffix(msg.Channel, ".pong") {
		return
	}
	if msg.Event != "update" {
		return
	}

	switch msg.Channel {
	case "spot.book_ticker", "futures.book_ticker":
		c.handleBookTicker(msg.Result, mt)
	case "spot.order_book", "futures.order_book":
		c.handleOrderbook(msg.Result, mt)
	}
}

// bookTickerResult spot/futures book_ticker result 필드.
// spot:    {t(ms), s, b, B, a, A}
// futures: {t(ms), s or contract, b, B, a, A}  — 거래소에 따라 필드명 다를 수 있음.
type bookTickerResult struct {
	T        int64  `json:"t"`        // timestamp ms
	S        string `json:"s"`        // symbol (BTC_USDT)
	Contract string `json:"contract"` // futures fallback 심볼 필드
	B        string `json:"b"`        // best bid price
	A        string `json:"a"`        // best ask price
}

// handleBookTicker book_ticker 파싱 → onTicker 콜백.
func (c *PublicWSClient) handleBookTicker(raw json.RawMessage, mt gap.MarketType) {
	var r bookTickerResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return
	}
	sym := r.S
	if sym == "" {
		sym = r.Contract
	}
	if sym == "" {
		return
	}
	bid, _ := strconv.ParseFloat(r.B, 64)
	ask, _ := strconv.ParseFloat(r.A, 64)
	if bid <= 0 || ask <= 0 {
		return
	}
	coin := strings.TrimSuffix(sym, "_USDT")
	ts := time.UnixMilli(r.T)

	c.cbMu.RLock()
	fn := c.onTicker
	c.cbMu.RUnlock()
	if fn != nil {
		fn(coin, mt, bid, ask, ts)
	}
}

// obEntry order_book 단일 호가.
// spot:    {"p": "100.0", "s": "0.5"}  — s는 문자열
// futures: {"p": "100.0", "s": 5}      — s는 정수 (계약 수)
// json.Number로 통일하여 defensive 파싱.
type obEntry struct {
	P string      `json:"p"`
	S json.Number `json:"s"`
}

// obResult order_book result 필드.
type obResult struct {
	T        int64     `json:"t"`
	S        string    `json:"s"`        // spot: currency_pair
	Contract string    `json:"contract"` // futures: contract (fallback)
	Bids     []obEntry `json:"bids"`
	Asks     []obEntry `json:"asks"`
}

// handleOrderbook order_book 파싱 → onOrderbook 콜백.
func (c *PublicWSClient) handleOrderbook(raw json.RawMessage, mt gap.MarketType) {
	var r obResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return
	}

	sym := r.S
	if sym == "" {
		sym = r.Contract
	}
	if sym == "" {
		return
	}

	bids := gateParseBook(r.Bids)
	asks := gateParseBook(r.Asks)
	if len(bids) == 0 || len(asks) == 0 {
		return
	}

	coin := strings.TrimSuffix(sym, "_USDT")
	ts := time.UnixMilli(r.T)

	c.cbMu.RLock()
	fn := c.onOrderbook
	c.cbMu.RUnlock()
	if fn != nil {
		fn(coin, mt, bids, asks, ts)
	}
}

// writeMsg Gate WS 메시지 포맷으로 직렬화 후 전송. 미연결 시 no-op.
func (c *PublicWSClient) writeMsg(market, channel, event string, payload []string) {
	msg, err := json.Marshal(map[string]any{
		"time":    time.Now().Unix(),
		"channel": channel,
		"event":   event,
		"payload": payload,
	})
	if err != nil {
		return
	}

	c.connMu.Lock()
	var conn *websocket.Conn
	if market == "spot" && c.spotUp.Load() {
		conn = c.spotConn
	} else if market == "futures" && c.futuresUp.Load() {
		conn = c.futuresConn
	}
	if conn != nil {
		err = conn.WriteMessage(websocket.TextMessage, msg)
	}
	c.connMu.Unlock()

	if conn != nil && err != nil {
		logger.Warnf("[Gate WS %s] %s %s 전송 실패: %v", market, event, channel, err)
	}
}

// gateParseBook []obEntry → [][2]float64
func gateParseBook(entries []obEntry) [][2]float64 {
	out := make([][2]float64, 0, len(entries))
	for _, e := range entries {
		price, err1 := strconv.ParseFloat(e.P, 64)
		qty, err2 := strconv.ParseFloat(e.S.String(), 64)
		if err1 != nil || err2 != nil || price <= 0 {
			continue
		}
		out = append(out, [2]float64{price, qty})
	}
	return out
}

// 컴파일 타임 인터페이스 체크
var _ gap.PublicWSClient = (*PublicWSClient)(nil)
