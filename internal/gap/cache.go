// Package gap provides WebSocket price caching, exchange rate management,
// and gap calculation infrastructure shared by spot-futures and Kimchi pairs.
//
// Phase 1: 공통 인프라 (cache, WS manager skeleton, rate manager, gap calculator).
// Phase 2 에서 거래소별 PublicWSClient 구현체를 추가하고 RateSource 구현체를 등록한다.
package gap

import (
	"sync"
	"time"
)

// staleThreshold 가격/오더북 데이터가 "극단적으로 오래됨" 판정 기준.
// WS 연결이 끊어진 후 cache 에 영구 잔존하는 데이터를 필터하는 용도.
const staleThreshold = 5 * time.Minute

// PriceEntry 단일 심볼의 가격 캐시 엔트리
type PriceEntry struct {
	Bid       float64
	Ask       float64
	Timestamp time.Time
}

// IsStale 5분 임계값 기준 stale 여부
func (p *PriceEntry) IsStale() bool {
	return time.Since(p.Timestamp) > staleThreshold
}

// OrderbookEntry 오더북 캐시 엔트리
type OrderbookEntry struct {
	Bids      [][2]float64 // [price, qty] (높은가격순)
	Asks      [][2]float64 // [price, qty] (낮은가격순)
	Timestamp time.Time
}

// IsStale 5분 임계값 기준 stale 여부
func (o *OrderbookEntry) IsStale() bool {
	return time.Since(o.Timestamp) > staleThreshold
}

// PriceCache exchange -> symbol -> marketType -> PriceEntry 3-level 캐시.
// spot/futures 가 동일 symbol 이어도 marketType 으로 구분되므로 cache collision 없음.
type PriceCache struct {
	mu   sync.RWMutex
	data map[string]map[string]map[MarketType]*PriceEntry
}

// NewPriceCache 새 가격 캐시 생성
func NewPriceCache() *PriceCache {
	return &PriceCache{
		data: make(map[string]map[string]map[MarketType]*PriceEntry),
	}
}

// Set 가격 업데이트
func (c *PriceCache) Set(exchange, symbol string, marketType MarketType, bid, ask float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data[exchange] == nil {
		c.data[exchange] = make(map[string]map[MarketType]*PriceEntry)
	}
	if c.data[exchange][symbol] == nil {
		c.data[exchange][symbol] = make(map[MarketType]*PriceEntry)
	}
	c.data[exchange][symbol][marketType] = &PriceEntry{
		Bid:       bid,
		Ask:       ask,
		Timestamp: time.Now(),
	}
}

// Get 가격 조회 (stale 체크 없음). stale 판정은 호출자가 직접.
func (c *PriceCache) Get(exchange, symbol string, marketType MarketType) (bid, ask float64, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	symbols := c.data[exchange]
	if symbols == nil {
		return 0, 0, false
	}
	markets := symbols[symbol]
	if markets == nil {
		return 0, 0, false
	}
	entry := markets[marketType]
	if entry == nil {
		return 0, 0, false
	}
	return entry.Bid, entry.Ask, true
}

// GetFresh stale 아닌 가격만 조회
func (c *PriceCache) GetFresh(exchange, symbol string, marketType MarketType) (bid, ask float64, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	symbols := c.data[exchange]
	if symbols == nil {
		return 0, 0, false
	}
	markets := symbols[symbol]
	if markets == nil {
		return 0, 0, false
	}
	entry := markets[marketType]
	if entry == nil || entry.IsStale() {
		return 0, 0, false
	}
	return entry.Bid, entry.Ask, true
}

// Delete 특정 심볼+marketType 캐시 삭제 (구독 해제 시 호출)
func (c *PriceCache) Delete(exchange, symbol string, marketType MarketType) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if symbols := c.data[exchange]; symbols != nil {
		if markets := symbols[symbol]; markets != nil {
			delete(markets, marketType)
		}
	}
}

// OrderbookCache exchange -> symbol -> marketType -> OrderbookEntry 3-level 캐시.
type OrderbookCache struct {
	mu   sync.RWMutex
	data map[string]map[string]map[MarketType]*OrderbookEntry
}

// NewOrderbookCache 새 오더북 캐시 생성
func NewOrderbookCache() *OrderbookCache {
	return &OrderbookCache{
		data: make(map[string]map[string]map[MarketType]*OrderbookEntry),
	}
}

// Set 오더북 업데이트
func (c *OrderbookCache) Set(exchange, symbol string, marketType MarketType, bids, asks [][2]float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data[exchange] == nil {
		c.data[exchange] = make(map[string]map[MarketType]*OrderbookEntry)
	}
	if c.data[exchange][symbol] == nil {
		c.data[exchange][symbol] = make(map[MarketType]*OrderbookEntry)
	}
	c.data[exchange][symbol][marketType] = &OrderbookEntry{
		Bids:      bids,
		Asks:      asks,
		Timestamp: time.Now(),
	}
}

// Get 오더북 조회 (stale 체크 없음)
func (c *OrderbookCache) Get(exchange, symbol string, marketType MarketType) (bids, asks [][2]float64, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	symbols := c.data[exchange]
	if symbols == nil {
		return nil, nil, false
	}
	markets := symbols[symbol]
	if markets == nil {
		return nil, nil, false
	}
	entry := markets[marketType]
	if entry == nil {
		return nil, nil, false
	}
	return entry.Bids, entry.Asks, true
}

// GetFresh stale 아닌 오더북만 조회
func (c *OrderbookCache) GetFresh(exchange, symbol string, marketType MarketType) (bids, asks [][2]float64, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	symbols := c.data[exchange]
	if symbols == nil {
		return nil, nil, false
	}
	markets := symbols[symbol]
	if markets == nil {
		return nil, nil, false
	}
	entry := markets[marketType]
	if entry == nil || entry.IsStale() {
		return nil, nil, false
	}
	return entry.Bids, entry.Asks, true
}

// Delete 특정 심볼+marketType 오더북 삭제
func (c *OrderbookCache) Delete(exchange, symbol string, marketType MarketType) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if symbols := c.data[exchange]; symbols != nil {
		if markets := symbols[symbol]; markets != nil {
			delete(markets, marketType)
		}
	}
}
