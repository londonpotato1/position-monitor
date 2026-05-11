// Bithumb 공개 시장 데이터 WebSocket 클라이언트
// gap.PublicWSClient 인터페이스 구현
// 프로토콜: wss://ws-api.bithumb.com/websocket/v1 (업비트 호환 v2)
// - type "orderbook" 단일 구독 → ticker(bid/ask) + orderbook 동시 처리
// - 심볼: 입력 "BTC" → 내부 "KRW-BTC.30" 변환 (30depth, 상위 20개 slice)
// - 재연결 로직 없음: Manager(runClient)가 담당
package bithumb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/londonpotato1/position-monitor/internal/gap"
)

const (
	bithumbPubWSHost = "ws-api.bithumb.com"
	bithumbPubWSPath = "/websocket/v1"
	obDepthSuffix    = ".30" // 30 depth 요청 → 상위 20개 사용
	obKeepLevels     = 20
	pubPingInterval  = 30 * time.Second
	pubReadDeadline  = 90 * time.Second
)

// PublicWSClient Bithumb 공개 시장 데이터 WebSocket 클라이언트.
// gap.PublicWSClient 인터페이스 구현.
// Bithumb 은 spot-only — MarketFutures 구독 시 에러 반환.
type PublicWSClient struct {
	logger zerolog.Logger

	mu   sync.Mutex
	conn *websocket.Conn

	// tickerSubs | orderbookSubs 어느 한 쪽이라도 있으면 WS codes 유지.
	// 둘 다 제거될 때만 codes에서 심볼 삭제.
	tickerSubs    map[string]bool // key: coin명 (예: "BTC")
	orderbookSubs map[string]bool

	connected bool
	ctx       context.Context
	cancel    context.CancelFunc

	onTicker    func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)
	onOrderbook func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)
}

// NewPublicWSClient 새 PublicWSClient 생성.
func NewPublicWSClient(logger zerolog.Logger) *PublicWSClient {
	return &PublicWSClient{
		logger:        logger.With().Str("client", "bithumb-pub-ws").Logger(),
		tickerSubs:    make(map[string]bool),
		orderbookSubs: make(map[string]bool),
	}
}

// Name 거래소 이름.
func (c *PublicWSClient) Name() string { return "bithumb" }

// SetOnTicker ticker 콜백 설정. Manager가 Start() 전에 호출.
func (c *PublicWSClient) SetOnTicker(fn func(symbol string, marketType gap.MarketType, bid, ask float64, ts time.Time)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onTicker = fn
}

// SetOnOrderbook 오더북 콜백 설정. Manager가 Start() 전에 호출.
func (c *PublicWSClient) SetOnOrderbook(fn func(symbol string, marketType gap.MarketType, bids, asks [][2]float64, ts time.Time)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onOrderbook = fn
}

// Connect WS 연결 및 read 루프 기동.
// 성공 시 goroutine 시작 후 반환. 실패 시 error 반환.
// 재연결 로직 없음 — Manager(runClient)가 담당.
func (c *PublicWSClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	wsURL := "wss://" + bithumbPubWSHost + bithumbPubWSPath
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("bithumb pub ws dial: %w", err)
	}

	c.conn = conn
	c.connected = true
	c.ctx, c.cancel = context.WithCancel(ctx)

	// 기존 구독 심볼이 있으면 즉시 구독 (재연결 후 resubscribe 패턴).
	if codes := c.activeCodes(); len(codes) > 0 {
		if err := c.sendSubscribe(conn, codes); err != nil {
			conn.Close()
			c.conn = nil
			c.connected = false
			return fmt.Errorf("bithumb pub ws subscribe: %w", err)
		}
	}

	go c.readLoop(conn, c.ctx)
	go c.pingLoop(conn, c.ctx)

	c.logger.Info().Msg("connected")
	return nil
}

// Disconnect 연결 해제 및 read goroutine 정리.
func (c *PublicWSClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	c.logger.Info().Msg("disconnected")
	return nil
}

// IsConnected 연결 상태 반환.
func (c *PublicWSClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// SubscribeTicker 심볼 ticker 구독 추가.
// symbol: coin명 ("BTC"). marketType 은 MarketSpot 만 허용.
func (c *PublicWSClient) SubscribeTicker(symbol string, marketType gap.MarketType) error {
	if marketType == gap.MarketFutures {
		return fmt.Errorf("bithumb: futures not supported")
	}
	return c.addSub(symbol, true)
}

// UnsubscribeTicker ticker 구독 해제.
func (c *PublicWSClient) UnsubscribeTicker(symbol string, marketType gap.MarketType) error {
	if marketType == gap.MarketFutures {
		return fmt.Errorf("bithumb: futures not supported")
	}
	return c.removeSub(symbol, true)
}

// SubscribeOrderbook 오더북 구독 추가.
// marketType 은 MarketSpot 만 허용.
func (c *PublicWSClient) SubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	if marketType == gap.MarketFutures {
		return fmt.Errorf("bithumb: futures not supported")
	}
	return c.addSub(symbol, false)
}

// UnsubscribeOrderbook 오더북 구독 해제.
func (c *PublicWSClient) UnsubscribeOrderbook(symbol string, marketType gap.MarketType) error {
	if marketType == gap.MarketFutures {
		return fmt.Errorf("bithumb: futures not supported")
	}
	return c.removeSub(symbol, false)
}

// --- 내부 구현 ---

func (c *PublicWSClient) addSub(symbol string, isTicker bool) error {
	coin := strings.ToUpper(symbol)
	c.mu.Lock()
	defer c.mu.Unlock()

	if isTicker {
		c.tickerSubs[coin] = true
	} else {
		c.orderbookSubs[coin] = true
	}

	if !c.connected || c.conn == nil {
		return nil
	}
	return c.sendSubscribe(c.conn, c.activeCodes())
}

func (c *PublicWSClient) removeSub(symbol string, isTicker bool) error {
	coin := strings.ToUpper(symbol)
	c.mu.Lock()
	defer c.mu.Unlock()

	if isTicker {
		delete(c.tickerSubs, coin)
	} else {
		delete(c.orderbookSubs, coin)
	}

	if !c.connected || c.conn == nil {
		return nil
	}
	codes := c.activeCodes()
	if len(codes) == 0 {
		return nil
	}
	return c.sendSubscribe(c.conn, codes)
}

// activeCodes ticker | orderbook 어느 한 쪽이라도 구독된 심볼을 codes로 반환.
// 호출 시 mu를 잡고 있어야 함.
func (c *PublicWSClient) activeCodes() []string {
	union := make(map[string]bool, len(c.tickerSubs)+len(c.orderbookSubs))
	for coin := range c.tickerSubs {
		union[coin] = true
	}
	for coin := range c.orderbookSubs {
		union[coin] = true
	}
	codes := make([]string, 0, len(union))
	for coin := range union {
		bithumbCoin := ToBithumbSymbol(coin)
		codes = append(codes, "KRW-"+bithumbCoin+obDepthSuffix)
	}
	return codes
}

// sendSubscribe v2 WS에 구독 메시지 전송.
// Bithumb v2는 새 메시지가 이전 구독을 완전히 덮어씀 → 항상 전체 codes 전송.
func (c *PublicWSClient) sendSubscribe(conn *websocket.Conn, codes []string) error {
	msg := []map[string]interface{}{
		{"ticket": "pm-bth"},
		{"type": "orderbook", "codes": codes, "isOnlyRealtime": true},
		{"format": "DEFAULT"},
	}
	return conn.WriteJSON(msg)
}

// pingLoop 30초마다 ping 전송.
func (c *PublicWSClient) pingLoop(conn *websocket.Conn, ctx context.Context) {
	ticker := time.NewTicker(pubPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readLoop WS 메시지 수신 루프. 에러 시 connected=false 후 종료.
// Manager가 IsConnected() 폴링으로 감지해 재연결.
func (c *PublicWSClient) readLoop(conn *websocket.Conn, ctx context.Context) {
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.connected = false
		}
		c.mu.Unlock()
		c.logger.Warn().Msg("read loop exited")
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(pubReadDeadline))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-ctx.Done():
				// Disconnect() 호출에 의한 정상 종료
			default:
				c.logger.Warn().Err(err).Msg("read error")
			}
			return
		}

		c.handleMessage(raw)
	}
}

// v2ObMsg 빗썸 v2 오더북 메시지 구조체.
type v2ObMsg struct {
	Type  string `json:"type"`
	Code  string `json:"code"`
	Units []struct {
		AskPrice float64 `json:"ask_price"`
		BidPrice float64 `json:"bid_price"`
		AskSize  float64 `json:"ask_size"`
		BidSize  float64 `json:"bid_size"`
	} `json:"orderbook_units"`
	Timestamp int64 `json:"timestamp"`
}

// handleMessage 수신 메시지 파싱 및 콜백 호출.
func (c *PublicWSClient) handleMessage(raw []byte) {
	var msg v2ObMsg
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "orderbook" {
		return
	}

	// "KRW-BTC.30" → "BTC", Bithumb 전용 심볼 정규화 (WAXL → AXL)
	coin := strings.TrimPrefix(msg.Code, "KRW-")
	if idx := strings.Index(coin, "."); idx != -1 {
		coin = coin[:idx]
	}
	symbol := NormalizeBithumbSymbol(coin)

	ts := time.UnixMilli(msg.Timestamp)
	if ts.IsZero() {
		ts = time.Now()
	}

	units := msg.Units
	if len(units) > obKeepLevels {
		units = units[:obKeepLevels]
	}

	bids := make([][2]float64, 0, len(units))
	asks := make([][2]float64, 0, len(units))
	for _, u := range units {
		if u.BidPrice > 0 {
			bids = append(bids, [2]float64{u.BidPrice, u.BidSize})
		}
		if u.AskPrice > 0 {
			asks = append(asks, [2]float64{u.AskPrice, u.AskSize})
		}
	}

	c.mu.Lock()
	onTicker := c.onTicker
	onOrderbook := c.onOrderbook
	c.mu.Unlock()

	// ticker: 최선 bid[0] / ask[0] 만 전달. Bithumb 은 항상 MarketSpot.
	if onTicker != nil && len(bids) > 0 && len(asks) > 0 {
		onTicker(symbol, gap.MarketSpot, bids[0][0], asks[0][0], ts)
	}
	if onOrderbook != nil {
		onOrderbook(symbol, gap.MarketSpot, bids, asks, ts)
	}
}

// compile-time 인터페이스 체크
var _ gap.PublicWSClient = (*PublicWSClient)(nil)
