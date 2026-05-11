package gap

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// MarketType spot / futures 구분 enum.
type MarketType int

const (
	MarketSpot    MarketType = iota // 현물
	MarketFutures                   // 선물/무기한
)

// String 로그용 문자열 표현.
func (m MarketType) String() string {
	if m == MarketFutures {
		return "futures"
	}
	return "spot"
}

// PublicWSClient 거래소별 공개 시장 데이터 WS 클라이언트.
// Phase 2 에서 각 거래소 adapter 가 구현.
// SubscribeTicker/Orderbook 의 symbol 은 거래소 native 포맷 (예: "BTCUSDT", "BTC-USDT-SWAP").
type PublicWSClient interface {
	Name() string
	Connect(ctx context.Context) error
	Disconnect() error
	SubscribeTicker(symbol string, marketType MarketType) error
	UnsubscribeTicker(symbol string, marketType MarketType) error
	SubscribeOrderbook(symbol string, marketType MarketType) error
	UnsubscribeOrderbook(symbol string, marketType MarketType) error
	IsConnected() bool

	// 데이터 수신 콜백 — Manager 가 Start() 이전에 세팅.
	SetOnTicker(func(symbol string, marketType MarketType, bid, ask float64, ts time.Time))
	SetOnOrderbook(func(symbol string, marketType MarketType, bids, asks [][2]float64, ts time.Time))
}

// subKey refcount 맵의 복합 키.
type subKey struct {
	symbol     string
	marketType MarketType
}

// Manager 통합 WS 매니저. refcount 기반 구독 관리 + 단순 재연결 (3초 fixed).
type Manager struct {
	clients        map[string]PublicWSClient
	priceCache     *PriceCache
	orderbookCache *OrderbookCache
	subscriptions  map[string]map[subKey]int // exchange -> subKey -> refcount
	mu             sync.RWMutex
	logger         zerolog.Logger

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager 새 WS 매니저 생성.
func NewManager(priceCache *PriceCache, orderbookCache *OrderbookCache, logger zerolog.Logger) *Manager {
	return &Manager{
		clients:        make(map[string]PublicWSClient),
		priceCache:     priceCache,
		orderbookCache: orderbookCache,
		subscriptions:  make(map[string]map[subKey]int),
		logger:         logger,
	}
}

// RegisterClient 거래소 클라이언트 등록. Phase 2 에서 adapter 쪽이 호출.
// Start() 전에 호출해야 콜백/재연결 루프가 정상 시작된다.
func (m *Manager) RegisterClient(client PublicWSClient) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := client.Name()
	m.clients[name] = client

	client.SetOnTicker(func(symbol string, marketType MarketType, bid, ask float64, ts time.Time) {
		m.priceCache.Set(name, symbol, marketType, bid, ask)
	})
	client.SetOnOrderbook(func(symbol string, marketType MarketType, bids, asks [][2]float64, ts time.Time) {
		if symbol == "BABY" {
			m.logger.Info().Str("exchange", name).Str("symbol", symbol).Stringer("marketType", marketType).Int("bids", len(bids)).Int("asks", len(asks)).Msg("[BABY DEBUG] cache.Set")
		}
		m.orderbookCache.Set(name, symbol, marketType, bids, asks)
	})
}

// Start 모든 등록된 클라이언트 연결 시작 + 재연결 감시 루프 구동.
// 등록된 클라이언트가 없으면 경고 로그 후 nil 리턴 (Phase 1 정상).
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.ctx != nil {
		m.mu.Unlock()
		return fmt.Errorf("manager already started")
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	clients := make([]PublicWSClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.Unlock()

	if len(clients) == 0 {
		m.logger.Warn().Msg("gap.Manager.Start: no clients registered (Phase 1 normal state)")
		return nil
	}

	for _, c := range clients {
		go m.runClient(c)
	}
	return nil
}

// runClient 단일 클라이언트 연결 + 끊기면 3초 후 재시도 (단순).
func (m *Manager) runClient(client PublicWSClient) {
	name := client.Name()
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		if err := client.Connect(m.ctx); err != nil {
			m.logger.Warn().Err(err).Str("exchange", name).Msg("WS connect failed; retry in 3s")
			if !sleepCtx(m.ctx, 3*time.Second) {
				return
			}
			continue
		}

		m.logger.Info().Str("exchange", name).Msg("WS connected")
		m.resubscribe(client)

		// 연결 상태 폴링
		for client.IsConnected() {
			if !sleepCtx(m.ctx, 2*time.Second) {
				return
			}
		}

		m.logger.Warn().Str("exchange", name).Msg("WS disconnected; reconnecting in 3s")
		_ = client.Disconnect()
		if !sleepCtx(m.ctx, 3*time.Second) {
			return
		}
	}
}

// resubscribe 재연결 시 기존 구독 복원.
func (m *Manager) resubscribe(client PublicWSClient) {
	m.mu.RLock()
	keys := make([]subKey, 0)
	if subs := m.subscriptions[client.Name()]; subs != nil {
		for k, cnt := range subs {
			if cnt > 0 {
				keys = append(keys, k)
			}
		}
	}
	m.mu.RUnlock()

	for _, k := range keys {
		if err := client.SubscribeTicker(k.symbol, k.marketType); err != nil {
			m.logger.Warn().Err(err).Str("exchange", client.Name()).Str("symbol", k.symbol).Str("marketType", k.marketType.String()).Msg("resubscribe ticker failed")
		}
		if err := client.SubscribeOrderbook(k.symbol, k.marketType); err != nil {
			m.logger.Warn().Err(err).Str("exchange", client.Name()).Str("symbol", k.symbol).Str("marketType", k.marketType.String()).Msg("resubscribe orderbook failed")
		}
	}
}

// Stop 매니저 종료. 모든 클라이언트 disconnect.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	clients := make([]PublicWSClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.Unlock()

	for _, c := range clients {
		if err := c.Disconnect(); err != nil {
			m.logger.Warn().Err(err).Str("exchange", c.Name()).Msg("disconnect failed")
		}
	}
	return nil
}

// Subscribe refcount 증가. 최초 구독 시 실제 SubscribeTicker + SubscribeOrderbook 호출.
func (m *Manager) Subscribe(exchange, symbol string, marketType MarketType) error {
	m.mu.Lock()
	client, ok := m.clients[exchange]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no client registered for exchange %q", exchange)
	}
	if m.subscriptions[exchange] == nil {
		m.subscriptions[exchange] = make(map[subKey]int)
	}
	k := subKey{symbol: symbol, marketType: marketType}
	cnt := m.subscriptions[exchange][k]
	m.subscriptions[exchange][k] = cnt + 1
	m.mu.Unlock()

	if cnt > 0 {
		return nil // 이미 구독 중
	}

	if err := client.SubscribeTicker(symbol, marketType); err != nil {
		m.decRef(exchange, symbol, marketType)
		return fmt.Errorf("SubscribeTicker %s/%s/%s: %w", exchange, symbol, marketType, err)
	}
	if err := client.SubscribeOrderbook(symbol, marketType); err != nil {
		_ = client.UnsubscribeTicker(symbol, marketType)
		m.decRef(exchange, symbol, marketType)
		return fmt.Errorf("SubscribeOrderbook %s/%s/%s: %w", exchange, symbol, marketType, err)
	}
	return nil
}

// Unsubscribe refcount 감소. 0 되면 실제 해지 + 캐시 삭제.
func (m *Manager) Unsubscribe(exchange, symbol string, marketType MarketType) error {
	m.mu.Lock()
	client, ok := m.clients[exchange]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no client registered for exchange %q", exchange)
	}
	subs := m.subscriptions[exchange]
	k := subKey{symbol: symbol, marketType: marketType}
	if subs == nil || subs[k] == 0 {
		m.mu.Unlock()
		return fmt.Errorf("not subscribed: %s/%s/%s", exchange, symbol, marketType)
	}
	subs[k]--
	remaining := subs[k]
	if remaining == 0 {
		delete(subs, k)
	}
	m.mu.Unlock()

	if remaining > 0 {
		return nil
	}

	if err := client.UnsubscribeTicker(symbol, marketType); err != nil {
		m.logger.Warn().Err(err).Str("exchange", exchange).Str("symbol", symbol).Str("marketType", marketType.String()).Msg("UnsubscribeTicker failed")
	}
	if err := client.UnsubscribeOrderbook(symbol, marketType); err != nil {
		m.logger.Warn().Err(err).Str("exchange", exchange).Str("symbol", symbol).Str("marketType", marketType.String()).Msg("UnsubscribeOrderbook failed")
	}
	m.priceCache.Delete(exchange, symbol, marketType)
	m.orderbookCache.Delete(exchange, symbol, marketType)
	return nil
}

func (m *Manager) decRef(exchange, symbol string, marketType MarketType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := subKey{symbol: symbol, marketType: marketType}
	if subs := m.subscriptions[exchange]; subs != nil {
		subs[k]--
		if subs[k] <= 0 {
			delete(subs, k)
		}
	}
}

// SubscribeForPair 페어 양쪽 동시 구독 헬퍼.
// spotEx 는 MarketSpot, futEx 는 MarketFutures 로 자동 전달.
func (m *Manager) SubscribeForPair(spotEx, futEx, coin string) error {
	if err := m.Subscribe(spotEx, coin, MarketSpot); err != nil {
		return err
	}
	if err := m.Subscribe(futEx, coin, MarketFutures); err != nil {
		_ = m.Unsubscribe(spotEx, coin, MarketSpot)
		return err
	}
	return nil
}

// UnsubscribeForPair 페어 양쪽 동시 해지. 한쪽 실패해도 나머지는 시도.
func (m *Manager) UnsubscribeForPair(spotEx, futEx, coin string) error {
	err1 := m.Unsubscribe(spotEx, coin, MarketSpot)
	err2 := m.Unsubscribe(futEx, coin, MarketFutures)
	if err1 != nil {
		return err1
	}
	return err2
}

// GetPrice 캐시된 bid/ask 조회
func (m *Manager) GetPrice(exchange, symbol string, marketType MarketType) (bid, ask float64, ok bool) {
	return m.priceCache.Get(exchange, symbol, marketType)
}

// GetOrderbook 캐시된 오더북 조회
func (m *Manager) GetOrderbook(exchange, symbol string, marketType MarketType) (bids, asks [][2]float64, ok bool) {
	return m.orderbookCache.Get(exchange, symbol, marketType)
}

// sleepCtx ctx 취소 시 false 반환, 정상 sleep 시 true.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
