// Package services provides position cache for hedged pair matching.
// 헷지 포지션 쌍 매칭 캐시
package services

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/internal/gap"

	"github.com/rs/zerolog"
)

// 헷지 쌍 방향 상수
const (
	PairDirectionSpotShort    = "normal"
	PairDirectionFuturesHedge = "futures_hedge"
)

// stablecoin 필터
var stablecoins = map[string]bool{
	"KRW": true, "USDT": true, "USDC": true, "BUSD": true,
	"TUSD": true, "FDUSD": true, "USDE": true, "DAI": true, "USDP": true,
}

// HedgedPositionLeg 헷지 포지션의 한쪽 다리
type HedgedPositionLeg struct {
	Exchange   string  `json:"exchange"`
	MarketType string  `json:"marketType"`
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"`
	Size       float64 `json:"size"`
	EntryPrice float64 `json:"entryPrice"`
	MarkPrice  float64 `json:"markPrice"`
	PnL        float64 `json:"pnl"`
	Leverage   int     `json:"leverage"`
}

// HedgedPositionPair 매칭된 헷지 포지션 쌍
type HedgedPositionPair struct {
	PairID      string             `json:"pairId"`
	Coin        string             `json:"coin"`
	SpotLeg     *HedgedPositionLeg `json:"spotLeg"`
	FuturesLeg  *HedgedPositionLeg `json:"futuresLeg"`
	LongLeg     *HedgedPositionLeg `json:"longLeg,omitempty"`
	ShortLeg    *HedgedPositionLeg `json:"shortLeg,omitempty"`
	MatchedSize float64            `json:"matchedSize"`
	Direction   string             `json:"direction"`
	PairType    string             `json:"pairType"` // "spot_futures" | "futures_futures"
	FuturesPnL  float64            `json:"futuresPnl"`
}

// FailedExchange 조회 실패한 거래소 정보
type FailedExchange struct {
	Exchange string `json:"exchange"`
	Scope    string `json:"scope"`  // "spot" or "futures"
	Error    string `json:"error"`  // 짧은 오류 메시지
}

// HedgedPositionsResponse 헷지 포지션 조회 응답
type HedgedPositionsResponse struct {
	Pairs           []HedgedPositionPair `json:"pairs"`
	Unmatched       []HedgedPositionLeg  `json:"unmatched"`
	UpdatedAt       string               `json:"updatedAt"`
	FailedExchanges []FailedExchange     `json:"failedExchanges"`
}

// PositionCache 포지션 캐시 서비스
type PositionCache struct {
	exchangeMgr     *ExchangeManager
	mu              sync.RWMutex
	cached          HedgedPositionsResponse
	lastRefresh     time.Time
	lastTrigger     time.Time
	refreshInterval time.Duration
	logger          zerolog.Logger
	wsManager       *gap.Manager // WS 구독 관리 (nil 허용)
}

// SetWSManager WS Manager 주입. nil-safe.
func (pc *PositionCache) SetWSManager(m *gap.Manager) {
	pc.wsManager = m
}

// NewPositionCache 새 PositionCache 생성
func NewPositionCache(exchangeMgr *ExchangeManager, refreshInterval time.Duration, logger zerolog.Logger) *PositionCache {
	return &PositionCache{
		exchangeMgr:     exchangeMgr,
		refreshInterval: refreshInterval,
		logger:          logger.With().Str("component", "position_cache").Logger(),
	}
}

// Get 캐시된 포지션 반환 (만료 시 새로고침)
func (pc *PositionCache) Get(ctx context.Context) HedgedPositionsResponse {
	pc.mu.RLock()
	if time.Since(pc.lastRefresh) < pc.refreshInterval {
		resp := pc.cached
		pc.mu.RUnlock()
		return resp
	}
	pc.mu.RUnlock()

	pc.Refresh(ctx)

	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.cached
}

// Refresh 캐시 강제 새로고침
func (pc *PositionCache) Refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	resp := pc.buildResponse(ctx)

	pc.mu.Lock()
	pc.cached = resp
	pc.lastRefresh = time.Now()
	pc.mu.Unlock()
}

// StartAutoRefresh 백그라운드 자동 갱신 시작
func (pc *PositionCache) StartAutoRefresh(ctx context.Context) {
	ticker := time.NewTicker(pc.refreshInterval)
	defer ticker.Stop()

	// 초기 1회 갱신
	pc.Refresh(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pc.Refresh(ctx)
		}
	}
}

// TriggerRefresh 외부 이벤트(WS 등)로부터 즉시 새로고침 요청
// 연속 호출 시 debounce (500ms 내 중복 무시)
func (pc *PositionCache) TriggerRefresh(ctx context.Context) {
	pc.mu.Lock()
	if time.Since(pc.lastTrigger) < 500*time.Millisecond {
		pc.mu.Unlock()
		return
	}
	pc.lastTrigger = time.Now()
	pc.mu.Unlock()
	go pc.Refresh(ctx)
}

// spotHolding 현물 보유 정보
type spotHolding struct {
	exchange   string
	coin       string
	free       float64
	total      float64
	isDomestic bool // true=국내 거래소(업비트/빗썸). 매칭은 국내/해외 모두 Total 기준.
}

// futuresPos 해외 선물 포지션 정보
type futuresPos struct {
	exchange string
	coin     string // USDT 제거한 코인명
	position *exchange.Position
}

func (pc *PositionCache) buildResponse(ctx context.Context) HedgedPositionsResponse {
	// 3단계 병렬 수집 (8초 타임아웃 내 12+ API 호출을 순차→병렬)
	var wg sync.WaitGroup
	var domesticSpots, overseasSpots []spotHolding
	var futures []futuresPos
	var domFailed, ovsFailed, futFailed []FailedExchange

	wg.Add(3)
	go func() {
		defer wg.Done()
		domesticSpots, domFailed = pc.collectDomesticSpots(ctx)
	}()
	go func() {
		defer wg.Done()
		overseasSpots, ovsFailed = pc.collectOverseasSpots(ctx)
	}()
	go func() {
		defer wg.Done()
		futures, futFailed = pc.collectOverseasPositions(ctx)
	}()
	wg.Wait()

	var allFailed []FailedExchange
	allFailed = append(allFailed, domFailed...)
	allFailed = append(allFailed, ovsFailed...)
	allFailed = append(allFailed, futFailed...)

	spots := append(domesticSpots, overseasSpots...)

	// 4. 매칭 전 안정 정렬 (map 순회 비결정성 제거)
	sort.Slice(spots, func(i, j int) bool {
		if spots[i].coin != spots[j].coin {
			return spots[i].coin < spots[j].coin
		}
		return spots[i].exchange < spots[j].exchange
	})
	sort.Slice(futures, func(i, j int) bool {
		if futures[i].coin != futures[j].coin {
			return futures[i].coin < futures[j].coin
		}
		return futures[i].exchange < futures[j].exchange
	})

	// 5. 쌍 매칭
	resp := pc.matchPairs(spots, futures)
	if allFailed == nil {
		allFailed = []FailedExchange{}
	}
	resp.FailedExchanges = allFailed

	// 6. 매칭된 쌍 WS 구독 요청 (refcount 기반 — 중복 구독은 내부에서 무시)
	if pc.wsManager != nil {
		for _, pair := range resp.Pairs {
			switch pair.PairType {
			case "spot_futures":
				if err := pc.wsManager.SubscribeForPair(pair.SpotLeg.Exchange, pair.FuturesLeg.Exchange, pair.Coin); err != nil {
					pc.logger.Warn().Err(err).Str("coin", pair.Coin).Msg("WS SubscribeForPair 실패")
				}
			case "futures_futures":
				if err := pc.wsManager.Subscribe(pair.LongLeg.Exchange, pair.Coin, gap.MarketFutures); err != nil {
					pc.logger.Warn().Err(err).Str("coin", pair.Coin).Str("exchange", pair.LongLeg.Exchange).Msg("WS Subscribe (long leg) 실패")
				}
				if err := pc.wsManager.Subscribe(pair.ShortLeg.Exchange, pair.Coin, gap.MarketFutures); err != nil {
					pc.logger.Warn().Err(err).Str("coin", pair.Coin).Str("exchange", pair.ShortLeg.Exchange).Msg("WS Subscribe (short leg) 실패")
				}
			}
		}
	}

	return resp
}

func (pc *PositionCache) collectDomesticSpots(ctx context.Context) ([]spotHolding, []FailedExchange) {
	type result struct {
		spots []spotHolding
		fail  *FailedExchange
	}
	names := []string{"upbit", "bithumb"}
	ch := make(chan result, len(names))

	for _, name := range names {
		name := name
		go func() {
			domEx, ok := pc.exchangeMgr.GetDomesticExchange(name)
			if !ok || !domEx.IsConnected() {
				ch <- result{}
				return
			}

			callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
			balances, err := domEx.GetAllBalances(callCtx)
			callCancel()
			if err != nil {
				pc.logger.Warn().Str("exchange", name).Err(err).Msg("국내 잔고 조회 실패")
				ch <- result{fail: &FailedExchange{Exchange: name, Scope: "spot", Error: err.Error()}}
				return
			}

			var spots []spotHolding
			for coin, bal := range balances {
				if stablecoins[strings.ToUpper(coin)] {
					continue
				}
				if bal.Total <= 0 {
					continue
				}
				spots = append(spots, spotHolding{
					exchange:   name,
					coin:       strings.ToUpper(coin),
					free:       bal.Free,
					total:      bal.Total,
					isDomestic: true,
				})
			}
			ch <- result{spots: spots}
		}()
	}

	var all []spotHolding
	var failed []FailedExchange
	for range names {
		r := <-ch
		all = append(all, r.spots...)
		if r.fail != nil {
			failed = append(failed, *r.fail)
		}
	}
	return all, failed
}

func (pc *PositionCache) collectOverseasSpots(ctx context.Context) ([]spotHolding, []FailedExchange) {
	type result struct {
		spots []spotHolding
		fail  *FailedExchange
	}

	exchanges := pc.exchangeMgr.GetConnectedExchanges()
	var overseasNames []string
	for _, name := range exchanges {
		if pc.exchangeMgr.IsDomestic(name) {
			continue
		}
		overseasNames = append(overseasNames, name)
	}

	ch := make(chan result, len(overseasNames))
	for _, name := range overseasNames {
		name := name
		go func() {
			ovsEx, ok := pc.exchangeMgr.GetOverseasExchange(name)
			if !ok || !ovsEx.IsConnected() {
				ch <- result{}
				return
			}

			callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
			balances, err := ovsEx.GetAllBalances(callCtx)
			callCancel()
			if err != nil {
				pc.logger.Warn().Str("exchange", name).Err(err).Msg("해외 현물 잔고 조회 실패")
				ch <- result{fail: &FailedExchange{Exchange: name, Scope: "spot", Error: err.Error()}}
				return
			}

			var spots []spotHolding
			for coin, bal := range balances {
				upper := strings.ToUpper(coin)
				if stablecoins[upper] {
					continue
				}
				if bal.Total <= 0 {
					continue
				}
				spots = append(spots, spotHolding{
					exchange: name,
					coin:     upper,
					free:     bal.Free,
					total:    bal.Total,
				})
			}
			ch <- result{spots: spots}
		}()
	}

	var all []spotHolding
	var failed []FailedExchange
	for range overseasNames {
		r := <-ch
		all = append(all, r.spots...)
		if r.fail != nil {
			failed = append(failed, *r.fail)
		}
	}
	return all, failed
}

func (pc *PositionCache) collectOverseasPositions(ctx context.Context) ([]futuresPos, []FailedExchange) {
	type result struct {
		positions []futuresPos
		fail      *FailedExchange
	}

	exchanges := pc.exchangeMgr.GetConnectedExchanges()
	var overseasNames []string
	for _, name := range exchanges {
		if pc.exchangeMgr.IsDomestic(name) {
			continue
		}
		overseasNames = append(overseasNames, name)
	}

	ch := make(chan result, len(overseasNames))
	for _, name := range overseasNames {
		name := name
		go func() {
			ovsEx, ok := pc.exchangeMgr.GetOverseasExchange(name)
			if !ok || !ovsEx.IsConnected() {
				ch <- result{}
				return
			}

			callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
			positions, err := ovsEx.GetAllPositions(callCtx)
			callCancel()
			if err != nil {
				pc.logger.Warn().Str("exchange", name).Err(err).Msg("해외 포지션 조회 실패")
				ch <- result{fail: &FailedExchange{Exchange: name, Scope: "futures", Error: err.Error()}}
				return
			}

			var fps []futuresPos
			for _, pos := range positions {
				if pos == nil || pos.Size <= 0 {
					continue
				}
				coin := extractCoin(pos.Symbol)
				fps = append(fps, futuresPos{
					exchange: name,
					coin:     coin,
					position: pos,
				})
			}
			ch <- result{positions: fps}
		}()
	}

	var all []futuresPos
	var failed []FailedExchange
	for range overseasNames {
		r := <-ch
		all = append(all, r.positions...)
		if r.fail != nil {
			failed = append(failed, *r.fail)
		}
	}
	return all, failed
}

// extractCoin 심볼에서 코인명 추출
// 지원 형식:
//
//	BTCUSDT, BTC_USDT, BTC-USDT, BTC-USDT-SWAP, BTCUSDC, BTC-USDC, ETHFDUSD 등
func extractCoin(symbol string) string {
	s := strings.ToUpper(symbol)
	s = strings.TrimSuffix(s, "-SWAP")
	// 우선순위 순 — USDT 최우선
	quotes := []string{"USDT", "USDC", "BUSD", "FDUSD", "USDE", "DAI", "USDP", "TUSD"}
	for _, q := range quotes {
		for _, delim := range []string{"_", "-", ""} {
			trimmed := strings.TrimSuffix(s, delim+q)
			if trimmed != s && trimmed != "" {
				return trimmed
			}
		}
	}
	return s
}

// normalize1000x 선물 심볼을 현물 코인명으로 변환 (중앙 레지스트리 사용)
// "1000SHIB" -> "SHIB", factor=1000
// "RAYSOL" -> "RAY", factor=1
// "BTC" -> "BTC", factor=1
func normalize1000x(futuresCoin string) (domesticCoin string, factor float64) {
	return exchange.ResolveSpotSymbol(futuresCoin)
}

func (pc *PositionCache) matchPairs(spots []spotHolding, futures []futuresPos) HedgedPositionsResponse {
	now := time.Now().Format(time.RFC3339)

	// 수집 현황 로그
	spotsByExchange := make(map[string]int)
	for _, s := range spots {
		spotsByExchange[s.exchange]++
	}
	futuresByExchange := make(map[string]int)
	for _, f := range futures {
		futuresByExchange[f.exchange]++
	}
	pc.logger.Info().
		Interface("spots", spotsByExchange).
		Interface("futures", futuresByExchange).
		Int("totalSpots", len(spots)).
		Int("totalFutures", len(futures)).
		Msg("매칭 시작")

	// 선물 포지션을 코인별로 그룹핑
	type futureEntry struct {
		pos     futuresPos
		factor  float64 // 1000x 보정 계수
		domCoin string  // 국내 매칭용 코인명
	}

	// 선물 -> 국내 코인명 매핑
	var futureEntries []futureEntry
	for _, f := range futures {
		domCoin, factor := normalize1000x(f.coin)
		futureEntries = append(futureEntries, futureEntry{
			pos:     f,
			factor:  factor,
			domCoin: domCoin,
		})
	}

	// 선물 코인 목록 (해외 현물 더스트 필터용)
	futureCoins := make(map[string]bool)
	for _, fe := range futureEntries {
		futureCoins[fe.domCoin] = true
	}

	var pairs []HedgedPositionPair
	var unmatched []HedgedPositionLeg

	// 매칭된 선물 인덱스 추적 (부분 매칭 시 소비된 수량 기록)
	matchedFutures := make(map[int]bool)
	consumedFuturesSize := make(map[int]float64) // 선물 단위로 소비된 수량

	// === Phase 1: 현물 + 숏 선물 매칭 (best-fit: matchedSize 최대화) ===
	for _, spot := range spots {
		matched := false

		// spotSize: 국내/해외 모두 Total 기준 (Free+Used). 활성 매도주문 수량 포함.
		spotSize := spot.total

		// 후보 중 matchedSize 최대인 것 선택
		// 알파벳순 first-match → best-fit으로 변경 (큰 선물 우선 매칭)
		matchIdx := -1
		var bestMatchedSize float64
		var bestSpotInFutures float64
		var hasSameCoin bool
		var sameCoinSide string
		var sameCoinExchange string
		for i, fe := range futureEntries {
			if !strings.EqualFold(spot.coin, fe.domCoin) {
				continue
			}
			hasSameCoin = true
			sameCoinSide = fe.pos.position.Side
			sameCoinExchange = fe.pos.exchange
			if fe.pos.position.Side != "short" {
				continue // 숏만 매칭
			}
			remaining := fe.pos.position.Size - consumedFuturesSize[i]
			if remaining <= 1e-9 {
				continue // 선물 완전 소비됨
			}

			spotInFutures := spotSize / fe.factor
			candSize := spotInFutures
			if remaining < candSize {
				candSize = remaining
			}

			if candSize > bestMatchedSize {
				bestMatchedSize = candSize
				bestSpotInFutures = spotInFutures
				matchIdx = i
			}
		}
		// 같은 코인 선물이 있는데 매칭 실패 시 원인 로그
		if matchIdx < 0 && hasSameCoin {
			pc.logger.Warn().
				Str("spotCoin", spot.coin).
				Str("spotExchange", spot.exchange).
				Str("futSide", sameCoinSide).
				Str("futExchange", sameCoinExchange).
				Msg("매칭 실패: 같은 코인 선물 존재하지만 미매칭")
		}
		if matchIdx >= 0 {
			fe := futureEntries[matchIdx]
			spotSizeInFutures := bestSpotInFutures
			matchedSize := bestMatchedSize

			// 매칭 수량이 0이면 쌍 생성 안 함 (더스트 잔고)
			if matchedSize >= 1e-8 {
				matched = true

				// 소비량 추적: 전부 소비되면 완전 매칭, 잔여분은 futures_futures에서 사용
				consumedFuturesSize[matchIdx] += matchedSize
				if consumedFuturesSize[matchIdx] >= fe.pos.position.Size-1e-9 {
					matchedFutures[matchIdx] = true
				}

				direction := PairDirectionSpotShort // spot + short (헷지)

				pairID := spot.exchange + "_" + fe.pos.exchange + "_" + spot.coin

				pair := HedgedPositionPair{
					PairID: pairID,
					Coin:   spot.coin,
					SpotLeg: &HedgedPositionLeg{
						Exchange:   spot.exchange,
						MarketType: "spot",
						Symbol:     spot.coin,
						Side:       "holding",
						Size:       matchedSize * fe.factor,
					},
					FuturesLeg: &HedgedPositionLeg{
						Exchange:   fe.pos.exchange,
						MarketType: "futures",
						Symbol:     fe.pos.position.Symbol,
						Side:       fe.pos.position.Side,
						Size:       matchedSize,
						EntryPrice: fe.pos.position.EntryPrice,
						MarkPrice:  fe.pos.position.MarkPrice,
						PnL:        fe.pos.position.UnrealizedPL * (matchedSize / fe.pos.position.Size),
						Leverage:   fe.pos.position.Leverage,
					},
					MatchedSize: matchedSize,
					Direction:   direction,
					PairType:    "spot_futures",
					FuturesPnL:  fe.pos.position.UnrealizedPL * (matchedSize / fe.pos.position.Size),
				}
				pairs = append(pairs, pair)

				// 현물 초과분 -> 미매칭 추가 (matched < spot인 경우)
				// 상대 오차 기준 (1ppm) — factor 큰 코인에서 (spotSizeInFutures-matchedSize)*factor 가 dust 로 튀는 것 방지.
				// matchedSize==0 방어는 위에서 matchedSize>=1e-8 가드로 이미 처리됨.
				if matchedSize > 0 && (spotSizeInFutures-matchedSize)/matchedSize > 1e-6 {
					unmatched = append(unmatched, HedgedPositionLeg{
						Exchange:   spot.exchange,
						MarketType: "spot",
						Symbol:     spot.coin,
						Side:       "holding",
						Size:       (spotSizeInFutures - matchedSize) * fe.factor,
					})
				}
			}
		}

		if !matched {
			unmatched = append(unmatched, HedgedPositionLeg{
				Exchange:   spot.exchange,
				MarketType: "spot",
				Symbol:     spot.coin,
				Side:       "holding",
				Size:       spot.total,
			})
		}
	}

	// === Phase 2: 선물 롱 <-> 선물 숏 매칭 (같은 코인, 같은 또는 다른 거래소) ===
	for i, longFe := range futureEntries {
		if matchedFutures[i] || longFe.pos.position.Side != "long" {
			continue
		}
		if longFe.pos.position.Size-consumedFuturesSize[i] <= 0 {
			continue
		}
		for j, shortFe := range futureEntries {
			if i == j || matchedFutures[j] || shortFe.pos.position.Side != "short" {
				continue
			}
			if !strings.EqualFold(longFe.domCoin, shortFe.domCoin) {
				continue
			}
			longRemaining := longFe.pos.position.Size - consumedFuturesSize[i]
			if longRemaining <= 0 {
				break
			}
			shortRemaining := shortFe.pos.position.Size - consumedFuturesSize[j]
			if shortRemaining <= 0 {
				continue
			}
			// 매칭 수량: 남은 수량 중 작은 쪽 기준 (선물 단위)
			matchSize := longRemaining
			if shortRemaining < matchSize {
				matchSize = shortRemaining
			}
			matchedSpotSize := matchSize * longFe.factor // 현물 단위로 표시

			// PnL 비율 배분 (잔여분에 해당하는 PnL)
			longPnlRatio := matchSize / longFe.pos.position.Size
			shortPnlRatio := matchSize / shortFe.pos.position.Size

			longLeg := &HedgedPositionLeg{
				Exchange:   longFe.pos.exchange,
				MarketType: "futures",
				Symbol:     longFe.pos.position.Symbol,
				Side:       "long",
				Size:       matchSize,
				EntryPrice: longFe.pos.position.EntryPrice,
				MarkPrice:  longFe.pos.position.MarkPrice,
				PnL:        longFe.pos.position.UnrealizedPL * longPnlRatio,
				Leverage:   longFe.pos.position.Leverage,
			}
			shortLeg := &HedgedPositionLeg{
				Exchange:   shortFe.pos.exchange,
				MarketType: "futures",
				Symbol:     shortFe.pos.position.Symbol,
				Side:       "short",
				Size:       matchSize,
				EntryPrice: shortFe.pos.position.EntryPrice,
				MarkPrice:  shortFe.pos.position.MarkPrice,
				PnL:        shortFe.pos.position.UnrealizedPL * shortPnlRatio,
				Leverage:   shortFe.pos.position.Leverage,
			}

			pairID := longFe.pos.exchange + "_" + shortFe.pos.exchange + "_" + longFe.domCoin + "_ff"
			pairs = append(pairs, HedgedPositionPair{
				PairID:      pairID,
				Coin:        longFe.domCoin,
				SpotLeg:     longLeg,  // UI 호환: SpotLeg = 롱 측
				FuturesLeg:  shortLeg, // UI 호환: FuturesLeg = 숏 측
				LongLeg:     longLeg,
				ShortLeg:    shortLeg,
				MatchedSize: matchedSpotSize,
				Direction:   PairDirectionFuturesHedge,
				PairType:    "futures_futures",
				FuturesPnL:  longLeg.PnL + shortLeg.PnL,
			})

			consumedFuturesSize[i] += matchSize
			consumedFuturesSize[j] += matchSize

			// 전부 소비되면 완전 매칭 처리
			if consumedFuturesSize[i] >= longFe.pos.position.Size-1e-9 {
				matchedFutures[i] = true
			}
			if consumedFuturesSize[j] >= shortFe.pos.position.Size-1e-9 {
				matchedFutures[j] = true
			}
			if matchedFutures[i] {
				break // 롱 전부 소비, 다음 롱으로
			}
		}
	}

	// === Phase 3: 미매칭 해외 포지션 (부분 소비 잔여분 포함) ===
	for i, fe := range futureEntries {
		remaining := fe.pos.position.Size - consumedFuturesSize[i]
		// 상대 오차 기준 dust 제거 — Phase1/2 소비 누적 시 float 잔차 (1e-15 등) 가 unmatched 로 새는 것 방지.
		// 절대 임계값 1e-9 은 1000x coin 에서 spot 단위로 factor 배 증폭되어 UI 에 dust leg 로 노출될 수 있음.
		if remaining <= 0 || remaining/fe.pos.position.Size < 1e-6 {
			continue
		}
		ratio := remaining / fe.pos.position.Size
		unmatched = append(unmatched, HedgedPositionLeg{
			Exchange:   fe.pos.exchange,
			MarketType: "futures",
			Symbol:     fe.pos.position.Symbol,
			Side:       fe.pos.position.Side,
			Size:       remaining,
			EntryPrice: fe.pos.position.EntryPrice,
			MarkPrice:  fe.pos.position.MarkPrice,
			PnL:        fe.pos.position.UnrealizedPL * ratio,
			Leverage:   fe.pos.position.Leverage,
		})
	}

	if pairs == nil {
		pairs = []HedgedPositionPair{}
	}
	if unmatched == nil {
		unmatched = []HedgedPositionLeg{}
	}

	// 안정적인 표시 순서: 코인명 + PairID 기준 정렬 (동일 코인 시 안정)
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Coin != pairs[j].Coin {
			return pairs[i].Coin < pairs[j].Coin
		}
		return pairs[i].PairID < pairs[j].PairID
	})
	sort.Slice(unmatched, func(i, j int) bool {
		if unmatched[i].Exchange != unmatched[j].Exchange {
			return unmatched[i].Exchange < unmatched[j].Exchange
		}
		if unmatched[i].Symbol != unmatched[j].Symbol {
			return unmatched[i].Symbol < unmatched[j].Symbol
		}
		return unmatched[i].MarketType < unmatched[j].MarketType
	})

	pc.logger.Info().
		Int("pairs", len(pairs)).
		Int("unmatched", len(unmatched)).
		Int("spots", len(spots)).
		Int("futures", len(futures)).
		Msg("매칭 완료")

	return HedgedPositionsResponse{
		Pairs:     pairs,
		Unmatched: unmatched,
		UpdatedAt: now,
	}
}
