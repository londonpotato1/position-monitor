// Binance 공개 시장 데이터 WS 클라이언트 (gap.PublicWSClient 구현).
// Spot + Futures 두 WebSocket 연결을 내부에서 관리한다.
// Name()="binance" 단일 등록. spotEx=futEx="binance" 헷지쌍 구조와 일치.
package binance

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
	wsSpotBase    = "wss://stream.binance.com:9443"
	wsFuturesBase = "wss://fstream.binance.com"
	wsPing        = 20 * time.Second
)

// PublicWSClient gap.PublicWSClient 구현체.
// 내부적으로 Spot/Futures 두 WS 연결을 유지.
// SubscribeTicker/SubscribeOrderbook 호출 시 marketType 에 따라 해당 연결에만 구독.
type PublicWSClient struct {
	onTicker    func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)
	onOrderbook func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)
	cbMu        sync.RWMutex

	// reconnect 복원용 구독 목록 (uppercase symbol)
	spotTickers    map[string]bool
	futTickers     map[string]bool
	spotOrderbooks map[string]bool
	futOrderbooks  map[string]bool
	subMu          sync.RWMutex

	spotConn    *websocket.Conn
	futuresConn *websocket.Conn
	connMu      sync.RWMutex

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
func (c *PublicWSClient) Name() string { return "binance" }

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

// Connect spot + futures goroutine 시작. 이미 실행 중이면 no-op.
func (c *PublicWSClient) Connect(ctx context.Context) error {
	c.ctxMu.Lock()
	if c.running.Load() {
		c.ctxMu.Unlock()
		return nil
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.running.Store(true)
	c.ctxMu.Unlock()

	go c.connectLoop("spot", wsSpotBase)
	go c.connectLoop("futures", wsFuturesBase)
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

// toNative coin → Binance native symbol ("ETH" → "ETHUSDT"). spot/fut 동일.
// TODO: 1000x 계열 (SHIB, PEPE 등) 은 symbol_registry 연동 검토.
func toNative(coin string) string { return strings.ToUpper(coin) + "USDT" }

// fromNative native → coin ("ETHUSDT" → "ETH").
func fromNative(s string) string {
	return strings.TrimSuffix(strings.ToUpper(s), "USDT")
}

// SubscribeTicker bookTicker 구독. marketType 에 따라 spot 또는 futures 연결 재연결 유도.
// symbol 은 coin 단위 (예: "ETH"). 내부에서 "ETHUSDT" 로 변환.
func (c *PublicWSClient) SubscribeTicker(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		if c.spotTickers[coin] {
			c.subMu.Unlock()
			return nil
		}
		c.spotTickers[coin] = true
		c.subMu.Unlock()
		c.reconnectMarket("spot")
	} else {
		if c.futTickers[coin] {
			c.subMu.Unlock()
			return nil
		}
		c.futTickers[coin] = true
		c.subMu.Unlock()
		c.reconnectMarket("futures")
	}
	return nil
}

// UnsubscribeTicker bookTicker 구독 해제.
func (c *PublicWSClient) UnsubscribeTicker(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		if !c.spotTickers[coin] {
			c.subMu.Unlock()
			return nil
		}
		delete(c.spotTickers, coin)
		c.subMu.Unlock()
		c.reconnectMarket("spot")
	} else {
		if !c.futTickers[coin] {
			c.subMu.Unlock()
			return nil
		}
		delete(c.futTickers, coin)
		c.subMu.Unlock()
		c.reconnectMarket("futures")
	}
	return nil
}

// SubscribeOrderbook depth10@100ms 구독. marketType 에 따라 해당 연결 재연결 유도.
func (c *PublicWSClient) SubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		if c.spotOrderbooks[coin] {
			c.subMu.Unlock()
			return nil
		}
		c.spotOrderbooks[coin] = true
		c.subMu.Unlock()
		c.reconnectMarket("spot")
	} else {
		if c.futOrderbooks[coin] {
			c.subMu.Unlock()
			return nil
		}
		c.futOrderbooks[coin] = true
		c.subMu.Unlock()
		c.reconnectMarket("futures")
	}
	return nil
}

// UnsubscribeOrderbook depth 구독 해제.
func (c *PublicWSClient) UnsubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	coin := strings.ToUpper(symbol)
	c.subMu.Lock()
	if marketType == gap.MarketSpot {
		if !c.spotOrderbooks[coin] {
			c.subMu.Unlock()
			return nil
		}
		delete(c.spotOrderbooks, coin)
		c.subMu.Unlock()
		c.reconnectMarket("spot")
	} else {
		if !c.futOrderbooks[coin] {
			c.subMu.Unlock()
			return nil
		}
		delete(c.futOrderbooks, coin)
		c.subMu.Unlock()
		c.reconnectMarket("futures")
	}
	return nil
}

// reconnectMarket 기존 conn 을 닫아 재연결 유도. conn 이 없으면 no-op (다음 재시도 루프가 새 URL 로 연결함).
func (c *PublicWSClient) reconnectMarket(market string) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if market == "spot" && c.spotConn != nil {
		_ = c.spotConn.Close()
	} else if market == "futures" && c.futuresConn != nil {
		_ = c.futuresConn.Close()
	}
}

// hasSubs 해당 market 에 구독이 하나라도 있으면 true.
func (c *PublicWSClient) hasSubs(market string) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	if market == "spot" {
		return len(c.spotTickers) > 0 || len(c.spotOrderbooks) > 0
	}
	return len(c.futTickers) > 0 || len(c.futOrderbooks) > 0
}

// connectLoop 단일 market 연결. 끊기면 3초 후 재시도.
func (c *PublicWSClient) connectLoop(market, base string) {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		c.singleConnect(market, base)
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// singleConnect 1회 연결 시도 + readLoop. 끊기면 return.
func (c *PublicWSClient) singleConnect(market, base string) {
	// combined stream URL 을 만들려면 구독이 최소 1개 있어야 한다.
	// 구독이 없으면 대기 (500ms 간격, ctx 취소 시 종료).
	for !c.hasSubs(market) {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	url := c.buildURL(base, market)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}

	conn, _, err := dialer.DialContext(c.ctx, url, nil)
	if err != nil {
		logger.Warnf("[Binance WS %s] 연결 실패: %v", market, err)
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

	logger.Infof("[Binance WS %s] 연결됨", market)

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

	logger.Warnf("[Binance WS %s] 연결 끊김", market)
}

// buildURL combined stream URL 생성. 구독 없으면 bare /ws.
// 내부 맵 key 는 coin 형식 → toNative 로 변환 후 stream 문자열 생성.
func (c *PublicWSClient) buildURL(base, market string) string {
	c.subMu.RLock()
	var streams []string
	if market == "spot" {
		streams = make([]string, 0, len(c.spotTickers)+len(c.spotOrderbooks))
		for coin := range c.spotTickers {
			streams = append(streams, strings.ToLower(toNative(coin))+"@bookTicker")
		}
		for coin := range c.spotOrderbooks {
			streams = append(streams, strings.ToLower(toNative(coin))+"@depth20@100ms")
		}
	} else {
		streams = make([]string, 0, len(c.futTickers)+len(c.futOrderbooks))
		for coin := range c.futTickers {
			streams = append(streams, strings.ToLower(toNative(coin))+"@bookTicker")
		}
		for coin := range c.futOrderbooks {
			streams = append(streams, strings.ToLower(toNative(coin))+"@depth20@100ms")
		}
	}
	c.subMu.RUnlock()

	if len(streams) == 0 {
		return base + "/ws"
	}
	return base + "/stream?streams=" + strings.Join(streams, "/")
}

// readLoop 메시지 수신 + ping 유지. ctx 취소 또는 에러 시 return.
func (c *PublicWSClient) readLoop(conn *websocket.Conn, market string) {
	conn.SetPongHandler(func(string) error { return nil })

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(wsPing)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			case <-done:
				return
			case <-c.ctx.Done():
				return
			}
		}
	}()
	defer close(done)

	// market → MarketType 변환
	mt := gap.MarketSpot
	if market == "futures" {
		mt = gap.MarketFutures
	}

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Debugf("[Binance WS %s] 읽기 오류: %v", market, err)
			}
			return
		}

		c.dispatch(msg, market, mt)
	}
}

// wsMsg combined stream 래퍼.
type wsMsg struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
	ID     *int64          `json:"id"`
}

// dispatch 메시지 타입 분기.
func (c *PublicWSClient) dispatch(raw []byte, market string, mt gap.MarketType) {
	var env wsMsg
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	// SUBSCRIBE 응답 무시
	if env.ID != nil && env.Stream == "" {
		return
	}

	payload := env.Data
	if payload == nil {
		payload = raw
	}

	if strings.Contains(env.Stream, "@depth") {
		c.handleDepth(payload, env.Stream, market, mt)
		return
	}
	c.handleBookTicker(payload, mt)
}

// handleBookTicker bookTicker 파싱 → onTicker 콜백.
// msg.Symbol 은 native ("ETHUSDT") → coin ("ETH") 로 역변환 후 콜백.
func (c *PublicWSClient) handleBookTicker(data []byte, mt gap.MarketType) {
	var msg struct {
		Symbol string `json:"s"`
		Bid    string `json:"b"`
		Ask    string `json:"a"`
	}
	if err := json.Unmarshal(data, &msg); err != nil || msg.Symbol == "" {
		return
	}
	bid := wsParseFloat(msg.Bid)
	ask := wsParseFloat(msg.Ask)
	if bid <= 0 || ask <= 0 {
		return
	}
	coin := fromNative(msg.Symbol)
	if coin == "" {
		return
	}
	c.cbMu.RLock()
	fn := c.onTicker
	c.cbMu.RUnlock()
	if fn != nil {
		fn(coin, mt, bid, ask, time.Now())
	}
}

// handleDepth Partial Book Depth 파싱 → onOrderbook 콜백.
func (c *PublicWSClient) handleDepth(data []byte, streamName string, market string, mt gap.MarketType) {
	var msg struct {
		Symbol string     `json:"s"`
		Bids   [][]string `json:"bids"`
		Asks   [][]string `json:"asks"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	// native symbol 추출 ("ETHUSDT"). msg.Symbol 비어있으면 stream 이름에서 추출.
	native := strings.ToUpper(msg.Symbol)
	if native == "" {
		if idx := strings.IndexByte(streamName, '@'); idx > 0 {
			native = strings.ToUpper(streamName[:idx])
		}
	}
	// native → coin 역변환. 구독 맵 key 는 coin 형식.
	coin := fromNative(native)
	if coin == "" {
		logger.Debugf("[Binance WS %s] depth 메시지: symbol 식별 불가 (stream=%s)", market, streamName)
		return
	}

	bids := wsParseBook(msg.Bids)
	asks := wsParseBook(msg.Asks)
	if len(bids) == 0 || len(asks) == 0 {
		return
	}

	c.cbMu.RLock()
	fn := c.onOrderbook
	c.cbMu.RUnlock()
	if fn != nil {
		if coin == "BABY" {
			logger.Infof("[BABY DEBUG][Binance WS %s] depth recv: bids=%d asks=%d top_bid=%.8f top_ask=%.8f", market, len(bids), len(asks), bids[0][0], asks[0][0])
		}
		fn(coin, mt, bids, asks, time.Now())
	}
}

// wsParseBook [][]string → [][2]float64
func wsParseBook(rows [][]string) [][2]float64 {
	out := make([][2]float64, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		price, e1 := strconv.ParseFloat(row[0], 64)
		qty, e2 := strconv.ParseFloat(row[1], 64)
		if e1 != nil || e2 != nil || price <= 0 {
			continue
		}
		out = append(out, [2]float64{price, qty})
	}
	return out
}

// wsParseFloat 문자열 → float64
func wsParseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// compile-time 인터페이스 체크
var _ gap.PublicWSClient = (*PublicWSClient)(nil)
