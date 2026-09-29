package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/internal/notify"

	"github.com/rs/zerolog"
)

// liqHarness 는 LiqAlertService 를 네트워크/sleep 없이 구동한다 (주입: 스냅샷, 거래소 상태, 시각, 전송).
type liqHarness struct {
	t         *testing.T
	s         *LiqAlertService
	now       time.Time
	snap      HedgedPositionsResponse
	refreshed time.Time
	up        bool
	disabled  error
	fail      []error // 다음 전송들이 순서대로 반환할 오류
	attempts  int
	sent      []string
}

func newLiqHarness(t *testing.T, dbPath string) *liqHarness {
	t.Helper()
	s, err := NewLiqAlertService(dbPath, nil, nil, nil, zerolog.Nop())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	h := &liqHarness{t: t, s: s, now: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), up: true}
	s.burstGap = 0 // 긴급 반복 간격 sleep 없음
	s.now = func() time.Time { return h.now }
	s.snapshot = func() (HedgedPositionsResponse, time.Time) { return h.snap, h.refreshed }
	s.exchangeUp = func(string) bool { return h.up }
	s.disabledReason = func() error { return h.disabled }
	s.send = func(text string) error {
		h.attempts++
		if len(h.fail) > 0 {
			err := h.fail[0]
			h.fail = h.fail[1:]
			return err
		}
		h.sent = append(h.sent, text)
		return nil
	}
	return h
}

func newLiqTest(t *testing.T) *liqHarness {
	return newLiqHarness(t, filepath.Join(t.TempDir(), "liq_alerts.db"))
}

// tick 시각을 d 만큼 진행하고 신선한 스냅샷으로 1회 평가한다.
func (h *liqHarness) tick(d time.Duration) {
	h.now = h.now.Add(d)
	h.refreshed = h.now
	h.s.evaluate(nil)
}

func (h *liqHarness) on(ex, sym string) {
	h.t.Helper()
	if err := h.s.SetEnabled(ex, sym, true); err != nil {
		h.t.Fatalf("SetEnabled: %v", err)
	}
}

// newSent 는 마지막 확인 이후 새로 성공 전송된 메시지 수를 돌려주고 기준을 옮긴다.
func (h *liqHarness) newSent(from *int) int {
	n := len(h.sent) - *from
	*from = len(h.sent)
	return n
}

func (h *liqHarness) last() string {
	if len(h.sent) == 0 {
		return ""
	}
	return h.sent[len(h.sent)-1]
}

func fp(v float64) *float64 { return &v }

// sfPair liq=100 기준 거리 d% 인 mark 로 sf 쌍 생성 (entry=mark → 상승 0%).
func sfPair(ex, sym string, dist float64) HedgedPositionPair {
	mark := 100 / (1 + dist/100)
	return HedgedPositionPair{
		PairID: "upbit_" + ex + "_" + sym, Coin: strings.TrimSuffix(sym, "USDT"), PairType: "spot_futures",
		SpotLeg: &HedgedPositionLeg{Exchange: "upbit", MarketType: "spot"},
		FuturesLeg: &HedgedPositionLeg{Exchange: ex, MarketType: "futures", Symbol: sym, Side: "short",
			MarkPrice: mark, EntryPrice: mark, LiquidationPrice: fp(100), Leverage: 3},
	}
}

func setDist(p *HedgedPositionPair, dist float64) { p.FuturesLeg.MarkPrice = 100 / (1 + dist/100) }

func TestPositionCacheSnapshotNoRefresh(t *testing.T) {
	pc := NewPositionCache(NewExchangeManager(zerolog.Nop()), 5*time.Second, zerolog.Nop())
	old := time.Now().Add(-time.Hour)
	pc.cached = HedgedPositionsResponse{Pairs: []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 10)}, UpdatedAt: "old"}
	pc.lastRefresh = old

	resp, at := pc.Snapshot()
	if !at.Equal(old) || resp.UpdatedAt != "old" || len(resp.Pairs) != 1 {
		t.Fatalf("Snapshot must return cached value as-is: at=%v resp=%+v", at, resp)
	}
	if !pc.lastRefresh.Equal(old) {
		t.Fatalf("Snapshot must not refresh: lastRefresh=%v", pc.lastRefresh)
	}
	// 대조군: 같은 오래된 캐시에서 Get 은 동기 Refresh 를 돌린다 (알림 루프가 Get 을 쓰면 안 되는 이유)
	pc.Get(context.Background())
	if pc.lastRefresh.Equal(old) {
		t.Fatalf("control: Get on a stale cache should refresh")
	}
}

// 단계 진입 1건; 70% → 12% 점프는 "≤15%" 1건만.
func TestLiqAlertTierEntryAndSkipJump(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 69)}
	var seen int
	h.tick(5 * time.Second)
	if h.newSent(&seen) != 1 || !strings.Contains(h.last(), "≤70%") {
		t.Fatalf("enter 70%% tier: want 1 message with ≤70%%, got %q", h.sent)
	}
	setDist(&h.snap.Pairs[0], 12)
	h.tick(5 * time.Second)
	if h.newSent(&seen) != liqUrgentCopies || !strings.Contains(h.last(), "≤15%") || strings.Contains(h.last(), "≤30%") || strings.Contains(h.last(), "≤60%") {
		t.Fatalf("skip-jump must send only the deepest tier: %q", h.last())
	}
	h.tick(5 * time.Second)
	if h.newSent(&seen) != 0 {
		t.Fatalf("no resend right after entry")
	}
}

// 9 단계 각각의 체류 주기.
func TestLiqAlertTierCadence(t *testing.T) {
	cases := []struct {
		pct    float64
		repeat time.Duration
	}{
		{100, 0}, {80, 0}, {70, 0}, {60, 0}, {50, 0},
		{40, 4 * time.Hour}, {30, time.Hour}, {15, 15 * time.Minute}, {5, 2 * time.Minute},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("tier%.0f", c.pct), func(t *testing.T) {
			h := newLiqTest(t)
			h.on("okx", "ETH-USDT-SWAP")
			h.snap.Pairs = []HedgedPositionPair{sfPair("okx", "ETH-USDT-SWAP", c.pct-0.5)}
			want := 1
			if c.pct <= liqUrgentTierPct {
				want = liqUrgentCopies
			}
			var seen int
			h.tick(0)
			if h.newSent(&seen) != want || !strings.Contains(h.last(), fmt.Sprintf("≤%.0f%%", c.pct)) {
				t.Fatalf("entry: %q", h.sent)
			}
			if c.repeat == 0 {
				for i := 0; i < 24; i++ {
					h.tick(time.Hour)
				}
				if n := h.newSent(&seen); n != 0 {
					t.Fatalf("no-repeat tier resent %d times in 24h", n)
				}
				return
			}
			h.tick(c.repeat - time.Second)
			if n := h.newSent(&seen); n != 0 {
				t.Fatalf("resent before cadence: %d", n)
			}
			h.tick(time.Second)
			if n := h.newSent(&seen); n != want {
				t.Fatalf("want %d resends at cadence, got %d", want, n)
			}
			h.tick(c.repeat - time.Second)
			if n := h.newSent(&seen); n != 0 {
				t.Fatalf("cadence must restart from the resend: %d", n)
			}
		})
	}
}

// 경계 흔들림 hysteresis + 얕은 단계 이동 시 주기 재시작.
func TestLiqAlertHysteresis(t *testing.T) {
	h := newLiqTest(t)
	h.on("binance", "SOLUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("binance", "SOLUSDT", 4.9)}
	var seen int
	h.tick(0)
	if h.newSent(&seen) != liqUrgentCopies {
		t.Fatalf("enter 5%%: %q", h.sent)
	}
	for _, d := range []float64{6.9, 4.9, 6.9, 4.9, 6.9} { // 100초 (< 2분) 동안 흔들림
		setDist(&h.snap.Pairs[0], d)
		h.tick(20 * time.Second)
	}
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("wobble inside hysteresis sent %d", n)
	}
	setDist(&h.snap.Pairs[0], 7.1) // 5% + 2%p 초과 → 이탈 (15% 단계로 이동, 발송 없음)
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("moving shallower must not send: %d", n)
	}
	setDist(&h.snap.Pairs[0], 4.9)
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "≤5%") {
		t.Fatalf("re-entry after leaving must send once: %d %q", n, h.last())
	}

	// 5% → 20% (30% 단계, 1시간 주기): 이동 시각부터 주기
	setDist(&h.snap.Pairs[0], 20)
	h.tick(time.Minute)      // 이동 시각 M
	h.tick(59 * time.Minute) // 마지막 발송 +60분 (발송 기준이면 여기서 울림) / 이동 +59분
	h.tick(time.Minute - time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("shallower tier resent before its cadence from the move: %d", n)
	}
	h.tick(time.Second) // M + 1시간
	if n := h.newSent(&seen); n != 1 || !strings.Contains(h.last(), "≤30%") {
		t.Fatalf("shallower tier cadence from move time: %d %q", n, h.last())
	}
}

// 상승 20/30/40%.
func TestLiqAlertRiseLevels(t *testing.T) {
	h := newLiqTest(t)
	h.on("gate", "DOGE_USDT")
	p := sfPair("gate", "DOGE_USDT", 150) // 단계 밖 (청산 알림 없음)
	p.FuturesLeg.EntryPrice = 10
	h.snap.Pairs = []HedgedPositionPair{p}
	leg := h.snap.Pairs[0].FuturesLeg
	var seen int
	steps := []struct {
		mark float64
		want string // "" = 발송 없음
	}{
		{10, "모니터링 시작"}, {12.5, "+20%"}, {12.6, ""}, {14.5, "+40%"}, {14.5, ""},
		{13.7, ""}, {14.1, ""}, // 37% 는 40−5 이상 → 재무장 없음, 41% 재발송 없음
		{13.4, ""}, {14.1, "+40%"}, // 35% 미만 → 재무장 → 41% 재발송
	}
	for i, st := range steps {
		leg.MarkPrice = st.mark
		leg.LiquidationPrice = fp(st.mark * 2.5)
		h.tick(5 * time.Second)
		n := h.newSent(&seen)
		if st.want == "" {
			if n != 0 {
				t.Fatalf("step %d: want no message, got %q", i, h.last())
			}
			continue
		}
		want := liqUrgentCopies // 상승 알림 = 긴급
		if st.want == "모니터링 시작" {
			want = 1
		}
		if n != want || !strings.Contains(h.last(), st.want) {
			t.Fatalf("step %d: want %q, got %d %q", i, st.want, n, h.last())
		}
		if st.want == "+40%" && (strings.Contains(h.last(), "상승 +20%") || strings.Contains(h.last(), "상승 +30%")) {
			t.Fatalf("multi-level jump must send only the highest: %q", h.last())
		}
	}
}

// 한 평가 = 메시지 1건 (긴급 → 같은 내용 반복); 길이 초과 분할에서 두 번째 분할 실패 → 첫 분할 항목만 발송 처리.
func TestLiqAlertBatchAndSplit(t *testing.T) {
	h := newLiqTest(t)
	for _, s := range []string{"AUSDT", "BUSDT", "CUSDT"} {
		h.on("bybit", s)
		h.snap.Pairs = append(h.snap.Pairs, sfPair("bybit", s, 10))
	}
	h.tick(0)
	if h.attempts != liqUrgentCopies || strings.Count(h.last(), "청산 근접") != 3 {
		t.Fatalf("3 alerts must go in 1 send: attempts=%d %q", h.attempts, h.sent)
	}

	h = newLiqTest(t)
	for i := 0; i < 60; i++ {
		sym := fmt.Sprintf("C%02dUSDT", i)
		h.on("bitget", sym)
		h.snap.Pairs = append(h.snap.Pairs, sfPair("bitget", sym, 10))
	}
	h.fail = append(make([]error, liqUrgentCopies), errors.New("boom"))
	h.s.send = func(text string) error { // 첫 분할 반복 모두 성공, 두 번째 분할 첫 전송 실패
		h.attempts++
		if utf8.RuneCountInString(text) > 4096 {
			t.Fatalf("part longer than 4096 characters: %d", utf8.RuneCountInString(text))
		}
		err := h.fail[0]
		h.fail = append(h.fail[1:], nil)
		if err == nil {
			h.sent = append(h.sent, text)
		}
		return err
	}
	h.tick(0)
	if h.attempts != liqUrgentCopies+1 || len(h.sent) != liqUrgentCopies {
		t.Fatalf("want part1 ok + part2 failed: attempts=%d sent=%d", h.attempts, len(h.sent))
	}
	first := strings.Count(h.sent[0], "청산 근접")
	if first == 0 || first >= 60 {
		t.Fatalf("first part should carry some alerts: %d", first)
	}
	h.tick(5 * time.Second)
	resent := 0
	for _, m := range h.sent[liqUrgentCopies:] {
		resent += strings.Count(m, "청산 근접")
		if strings.Contains(m, "C00USDT") || strings.Contains(m, "모니터링 시작") {
			t.Fatalf("items of the successful part must not be resent: %q", m)
		}
	}
	if resent != (60-first)*liqUrgentCopies {
		t.Fatalf("unsent items must be retried: first=%d resent=%d", first, resent)
	}
}

// 전송 실패 → 상태 불변 + 재전송; 429 retry_after 전 전송 시도 0. 시작 메시지 재포함.
func TestLiqAlertSendFailureAndRetryAfter(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	h.fail = []error{errors.New("network")}
	h.tick(0)
	if len(h.sent) != 0 || h.attempts != 1 {
		t.Fatalf("first send should fail")
	}
	h.tick(5 * time.Second)
	if len(h.sent) != liqUrgentCopies || !strings.Contains(h.sent[0], "≤15%") || !strings.Contains(h.sent[0], "모니터링 시작 (ON 1개)") {
		t.Fatalf("failed content must be resent next evaluation: %q", h.sent)
	}
	h.tick(5 * time.Second)
	if len(h.sent) != liqUrgentCopies {
		t.Fatalf("state must advance after success")
	}

	h = newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	h.fail = []error{&notify.APIError{StatusCode: 429, RetryAfter: 30 * time.Second}}
	h.tick(0)
	for i := 0; i < 5; i++ { // 25초
		h.tick(5 * time.Second)
	}
	if h.attempts != 1 {
		t.Fatalf("no send attempt before retry_after deadline: attempts=%d", h.attempts)
	}
	h.tick(5 * time.Second)
	if h.attempts != 1+liqUrgentCopies || len(h.sent) != liqUrgentCopies {
		t.Fatalf("send after deadline: attempts=%d sent=%d", h.attempts, len(h.sent))
	}
}

// 전달 불가 → 전송 0/상태 불변; 평가 도중 비활성 건너뜀 ≠ 성공; 전달 가능 전환 → 즉시 발송.
func TestLiqAlertNotDeliverable(t *testing.T) {
	h := newLiqTest(t)
	h.on("okx", "BTC-USDT-SWAP")
	h.snap.Pairs = []HedgedPositionPair{sfPair("okx", "BTC-USDT-SWAP", 12)}
	h.disabled = notify.ErrMasterOff
	for i := 0; i < 3; i++ {
		h.tick(5 * time.Second)
	}
	if h.attempts != 0 {
		t.Fatalf("not deliverable: want 0 send attempts, got %d", h.attempts)
	}
	st := h.s.Status()
	if st.Deliverable || !strings.Contains(st.Reason, "마스터") {
		t.Fatalf("status must report not deliverable + reason: %+v", st)
	}
	h.disabled = nil
	h.fail = []error{fmt.Errorf("skip: %w", notify.ErrMasterOff)} // 평가 도중 마스터 OFF
	h.tick(5 * time.Second)
	h.tick(5 * time.Second)
	if len(h.sent) != liqUrgentCopies || !strings.Contains(h.sent[0], "≤15%") {
		t.Fatalf("disabled-skip is not success; deliverable → current tier sent: %q", h.sent)
	}
}

// dedup + OFF 키 / ff 쌍.
func TestLiqAlertDedupOffAndFuturesPair(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	a, b := sfPair("bybit", "BTCUSDT", 12), sfPair("bybit", "BTCUSDT", 12)
	b.SpotLeg.Exchange = "bithumb"
	off := sfPair("bybit", "ETHUSDT", 3)
	ff := HedgedPositionPair{PairType: "futures_futures", Coin: "XRP",
		LongLeg:  &HedgedPositionLeg{Exchange: "okx", Symbol: "XRP-USDT-SWAP", Side: "long", MarkPrice: 1},
		ShortLeg: &HedgedPositionLeg{Exchange: "gate", Symbol: "XRP_USDT", Side: "short", MarkPrice: 1, EntryPrice: 1, LiquidationPrice: fp(1.01)}}
	h.snap.Pairs = []HedgedPositionPair{a, b, off, ff}
	h.tick(0)
	if len(h.sent) != liqUrgentCopies || strings.Count(h.sent[0], "청산 근접") != 1 {
		t.Fatalf("same key rows → 1 alert line: %q", h.sent)
	}
	if strings.Contains(h.sent[0], "ETHUSDT") || strings.Contains(h.sent[0], "XRP") {
		t.Fatalf("OFF key / ff pair must not alert: %q", h.sent[0])
	}

	h2 := newLiqTest(t) // ON 이 하나도 없으면 전송 0
	h2.snap.Pairs = []HedgedPositionPair{off}
	h2.tick(0)
	if h2.attempts != 0 {
		t.Fatalf("OFF only: want 0 sends")
	}
}

// 같은 DB 로 새 인스턴스 → 토글 유지, 상승 재발송 0, 청산 단계 1회, 시작 메시지 1회.
func TestLiqAlertRestartPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liq_alerts.db")
	pair := sfPair("hyperliquid", "BTC", 12)
	pair.FuturesLeg.EntryPrice = pair.FuturesLeg.MarkPrice / 1.25 // 상승 +25%
	h := newLiqHarness(t, path)
	h.on("hyperliquid", "BTC")
	h.snap.Pairs = []HedgedPositionPair{pair}
	h.tick(0)
	if len(h.sent) != liqUrgentCopies || !strings.Contains(h.sent[0], "+20%") || !strings.Contains(h.sent[0], "≤15%") {
		t.Fatalf("first run: %q", h.sent)
	}
	h.s.Close()

	h2 := newLiqHarness(t, path)
	if st := h2.s.Status(); len(st.Keys) != 1 || st.Keys[0].Exchange != "hyperliquid" || st.Keys[0].Symbol != "BTC" {
		t.Fatalf("toggle must persist: %+v", st.Keys)
	}
	h2.snap.Pairs = []HedgedPositionPair{pair}
	h2.tick(0)
	h2.tick(5 * time.Second)
	if len(h2.sent) != liqUrgentCopies {
		t.Fatalf("restart: want exactly 1 message (x copies), got %q", h2.sent)
	}
	m := h2.sent[0]
	if strings.Contains(m, "상승") || !strings.Contains(m, "≤15%") || !strings.Contains(m, "모니터링 시작 (ON 1개)") {
		t.Fatalf("restart: no rise resend, tier once, start once: %q", m)
	}
}

// 새 clone 에는 data/ 가 없다 (gitignore) — 서비스가 DB 디렉터리를 스스로 만들어야 첫 실행부터 동작한다.
func TestLiqAlertCreatesDBDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "liq_alerts.db")
	newLiqHarness(t, path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file: %v", err)
	}
}

// 감시 불가 (실제 ExchangeManager 로 미등록/미연결 판정).
// connOverseas 는 IsConnected 만 구현한 해외 거래소 (다른 메서드 호출 시 nil 임베드로 panic).
type connOverseas struct {
	exchange.OverseasExchange
	up bool
}

func (c *connOverseas) IsConnected() bool { return c.up }

func TestLiqAlertMonitoringUnavailable(t *testing.T) {
	cases := []struct {
		name    string
		present bool // 키가 스냅샷에 있음 (스냅샷 오래됨 케이스)
		stale   bool
		reg     string // "", "up", "down"
		failed  []FailedExchange
	}{
		{name: "futures fetch failed", reg: "up", failed: []FailedExchange{{Exchange: "bybit", Scope: "futures"}}},
		{name: "not registered, no failures"},
		{name: "registered but disconnected, no failures", reg: "down"},
		{name: "stale snapshot with key present", present: true, stale: true, reg: "up"},
		{name: "spot failure", reg: "up", failed: []FailedExchange{{Exchange: "upbit", Scope: "spot"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLiqTest(t)
			em := NewExchangeManager(zerolog.Nop())
			em.overseasExchanges["bybit"] = &connOverseas{up: true} // 초기엔 정상
			h.s.exchangeUp = em.IsConnected
			h.on("bybit", "BTCUSDT")
			pair := sfPair("bybit", "BTCUSDT", 12)
			h.snap.Pairs = []HedgedPositionPair{pair}
			var seen int
			h.tick(0)
			if h.newSent(&seen) != liqUrgentCopies {
				t.Fatalf("initial tier alert: %q", h.sent)
			}
			delete(em.overseasExchanges, "bybit")
			switch c.reg {
			case "up":
				em.overseasExchanges["bybit"] = &connOverseas{up: true}
			case "down":
				em.overseasExchanges["bybit"] = &connOverseas{}
			}
			if !c.present {
				h.snap.Pairs = nil
			}
			h.snap.FailedExchanges = c.failed
			down := func(d time.Duration) {
				h.now = h.now.Add(d)
				if c.stale {
					h.refreshed = h.now.Add(-31 * time.Second)
				} else {
					h.refreshed = h.now
				}
				h.s.evaluate(nil)
			}
			down(5 * time.Second)
			down(59 * time.Second)
			if n := h.newSent(&seen); n != 0 {
				t.Fatalf("no message before 60s: %q", h.sent)
			}
			if st := h.s.Status(); st.Keys[0].Watch != "감시 불가" {
				t.Fatalf("watch: %+v", st.Keys)
			}
			down(time.Second)
			if n := h.newSent(&seen); n != 1 || !strings.Contains(h.last(), "감시 불가") {
				t.Fatalf("want one 감시 불가 at 60s: %q", h.sent)
			}
			down(10 * time.Minute)
			if n := h.newSent(&seen); n != 0 {
				t.Fatalf("감시 불가 only once")
			}
			em.overseasExchanges["bybit"] = &connOverseas{up: true}
			h.snap.Pairs, h.snap.FailedExchanges = []HedgedPositionPair{pair}, nil
			h.tick(5 * time.Second)
			if n := h.newSent(&seen); n != 1 || !strings.Contains(h.last(), "감시 복구") || strings.Contains(h.last(), "청산 근접") {
				t.Fatalf("recovery: one 감시 복구 and tier state kept: %q", h.sent)
			}
		})
	}
}

// 진짜 사라짐 → 0건 + 토글 유지 + 재등장 시 즉시 단계 발송.
func TestLiqAlertTrulyGone(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	pair := sfPair("bybit", "BTCUSDT", 12)
	h.snap.Pairs = []HedgedPositionPair{pair}
	var seen int
	h.tick(0)
	h.newSent(&seen)
	h.snap.Pairs = nil
	for i := 0; i < 30; i++ {
		h.tick(5 * time.Second)
	}
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("truly gone must be silent: %q", h.sent)
	}
	if st := h.s.Status(); len(st.Keys) != 1 || st.Keys[0].Watch != "행 없음" {
		t.Fatalf("toggle kept with 행 없음: %+v", st.Keys)
	}
	h.snap.Pairs = []HedgedPositionPair{pair}
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "≤15%") {
		t.Fatalf("reappearance → immediate tier send: %q", h.sent)
	}
}

// 감시 불가 발송 후 진짜 사라짐 → "감시 종료 (포지션 없음)" 정확히 1건 (실패 시 재시도);
// 감시 불가를 보낸 적 없는 키는 무음.
func TestLiqAlertGoneAfterUnavailable(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	var seen int
	h.tick(0)
	h.newSent(&seen)
	h.snap.Pairs, h.up = nil, false // 감시 불가 60초 → 1건
	h.tick(5 * time.Second)
	h.tick(60 * time.Second)
	if n := h.newSent(&seen); n != 1 || !strings.Contains(h.last(), "감시 불가") {
		t.Fatalf("setup: want 감시 불가: %q", h.sent)
	}
	h.up = true // 연결 복구 + 행 없음 = 진짜 사라짐
	h.fail = []error{errors.New("network")}
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("failed send must not count: %q", h.sent)
	}
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 1 || !strings.Contains(h.last(), "감시 종료 (포지션 없음)") || !strings.Contains(h.last(), "bybit BTCUSDT") {
		t.Fatalf("want one 감시 종료 retried after failure: %q", h.sent)
	}
	for i := 0; i < 10; i++ {
		h.tick(5 * time.Second)
	}
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("감시 종료 only once: %q", h.sent[seen-n:])
	}
	if st := h.s.Status(); len(st.Keys) != 1 || st.Keys[0].Watch != "행 없음" {
		t.Fatalf("toggle kept with 행 없음: %+v", st.Keys)
	}

	h2 := newLiqTest(t) // 감시 불가 60초 전에 진짜 사라짐 → 무음
	h2.on("bybit", "BTCUSDT")
	h2.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	h2.tick(0)
	h2.snap.Pairs, h2.up = nil, false
	h2.tick(30 * time.Second)
	h2.up = true
	for i := 0; i < 10; i++ {
		h2.tick(5 * time.Second)
	}
	if len(h2.sent) != liqUrgentCopies { // 첫 ≤15% 알림 반복분뿐
		t.Fatalf("never-sent 감시 불가 → silent: %q", h2.sent)
	}
}

// mark ≥ liq → 최심 단계 (≤5%) 즉시 + 2분 반복, 첫 줄 "[추정 청산가 도달/초과]", 데이터 오류 아님.
// 대조군: 거리 > 0 인 ≤5% 는 기존 첫 줄 "[청산 근접 ≤5%]".
func TestLiqAlertMarkAtOrBeyondLiq(t *testing.T) {
	for _, c := range []struct {
		name   string
		mark   float64 // liq = 100
		head   string  // 첫 줄
		beyond bool
	}{
		{"mark == liq", 100, "[추정 청산가 도달/초과] BTC", true},
		{"mark > liq", 110, "[추정 청산가 도달/초과] BTC", true},
		{"d > 0 within 5%", 100 / 1.049, "[청산 근접 ≤5%] BTC", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newLiqTest(t)
			h.on("bybit", "BTCUSDT")
			h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
			var seen int
			h.tick(0)
			if h.newSent(&seen) != liqUrgentCopies || !strings.Contains(h.last(), "≤15%") {
				t.Fatalf("setup ≤15%%: %q", h.sent)
			}
			h.snap.Pairs[0].FuturesLeg.MarkPrice = c.mark // 얕은 단계에서 5% 이내/청산가 도달 → 일반 심화 진입
			want := func(what string) {
				t.Helper()
				m := h.last()
				first, _, _ := strings.Cut(m, "\n")
				if n := h.newSent(&seen); n != liqUrgentCopies || first != c.head || strings.Contains(m, "≤0%, 추정 청산가 도달/초과") != c.beyond {
					t.Fatalf("%s: want one alert (x copies) with first line %q (beyond=%v), got %d %q", what, c.head, c.beyond, n, m)
				}
			}
			h.tick(5 * time.Second)
			want("immediate on entry")
			if w := h.s.Status().Keys[0].Watch; w != "ok" {
				t.Fatalf("mark ≥ liq is not a data error: watch=%q", w)
			}
			h.tick(2*time.Minute - time.Second)
			if n := h.newSent(&seen); n != 0 {
				t.Fatalf("no resend before 2 min: %d", n)
			}
			h.tick(time.Second)
			want("repeat at 2 min")
		})
	}
}

// 1000x 가격 ÷ factor, 거리/상승 % 는 factor 무관; 필수 항목.
func TestLiqAlertMessageContentScaleFactor(t *testing.T) {
	h := newLiqTest(t)
	h.on("binance", "1000SHIBUSDT")
	p := HedgedPositionPair{PairType: "spot_futures", Coin: "SHIB", SpotLeg: &HedgedPositionLeg{Exchange: "upbit"},
		FuturesLeg: &HedgedPositionLeg{Exchange: "binance", Symbol: "1000SHIBUSDT", Side: "short",
			MarkPrice: 0.012, EntryPrice: 0.0096, LiquidationPrice: fp(0.01374), PriceScaleFactor: 1000, Leverage: 5}}
	h.snap.Pairs = []HedgedPositionPair{p}
	h.tick(0)
	m := h.last()
	for _, want := range []string{"≤15%", "상승 +20%", "SHIB", "binance 1000SHIBUSDT", "0.000012", "0.00001374", "0.0000096",
		"14.50%", "+25.00%", "5x", "12:00:00", "다음 반복 12:15:00", "알림 끄기"} {
		if !strings.Contains(m, want) {
			t.Fatalf("message missing %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "0.0137") || strings.Contains(m, "0.012 ") {
		t.Fatalf("raw price must be divided by factor:\n%s", m)
	}
}

// 부정/적대 입력 (청산가 없음 → 상승은 동작).
func TestLiqAlertInvalidData(t *testing.T) {
	cases := []struct {
		name              string
		mark, entry       float64
		liq               *float64
		wantRise, wantLiq bool
	}{
		{"mark 0", 0, 10, fp(100), false, false},
		{"mark NaN", math.NaN(), 10, fp(100), false, false},
		{"mark Inf", math.Inf(1), 10, fp(100), false, false},
		{"liq below mark (도달/초과 = 최심 단계)", 90, 90, fp(80), false, true},
		{"liq NaN", 90, 90, fp(math.NaN()), false, false},
		{"liq 0", 90, 90, fp(0), false, false},
		{"liq negative", 90, 90, fp(-5), false, false},
		{"entry 0", 95, 0, fp(100), false, true},
		{"liq nil, rise works", 13, 10, nil, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLiqTest(t)
			h.on("bybit", "BTCUSDT")
			h.snap.Pairs = []HedgedPositionPair{{PairType: "spot_futures", Coin: "BTC", SpotLeg: &HedgedPositionLeg{},
				FuturesLeg: &HedgedPositionLeg{Exchange: "bybit", Symbol: "BTCUSDT", Side: "short", MarkPrice: c.mark, EntryPrice: c.entry, LiquidationPrice: c.liq}}}
			h.tick(0)
			m := strings.Join(h.sent, "\n")
			if strings.Contains(m, "상승 +") != c.wantRise || (strings.Contains(m, "청산 근접") || strings.Contains(m, "[추정 청산가 도달/초과]")) != c.wantLiq {
				t.Fatalf("rise=%v liq=%v, got:\n%s", c.wantRise, c.wantLiq, m)
			}
			if c.liq == nil && (!strings.Contains(m, "청산가 없음") || h.s.Status().Keys[0].Watch != "청산가 없음") {
				t.Fatalf("liq nil → 청산가 없음 in start message and status: %s", m)
			}
		})
	}
}

// 토글 설정/상태 조회.
func TestLiqAlertSetEnabledAndStatus(t *testing.T) {
	h := newLiqTest(t)
	for _, ex := range []string{"kucoin", "mexc", "htx", "lighter", "BYBIT", ""} {
		if err := h.s.SetEnabled(ex, "BTCUSDT", true); err == nil {
			t.Fatalf("unsupported exchange %q must be rejected", ex)
		}
	}
	h.on("bybit", "BTCUSDT")
	if err := h.s.SetEnabled("bybit", "BTCUSDT", false); err != nil {
		t.Fatal(err)
	}
	st := h.s.Status()
	if len(st.Keys) != 0 || len(st.SupportedExchanges) != 6 || !st.Deliverable {
		t.Fatalf("status: %+v", st)
	}
	h.s.db.Close()
	if err := h.s.SetEnabled("bybit", "BTCUSDT", true); err == nil {
		t.Fatalf("persistence failure must return an error")
	}
	if len(h.s.Status().Keys) != 0 {
		t.Fatalf("failed persist must not change memory")
	}
}

// 동시 토글 설정 + 평가 + 상태 조회 (-race).
func TestLiqAlertConcurrentToggleRace(t *testing.T) {
	h := newLiqTest(t)
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	h.refreshed = h.now
	var mu sync.Mutex
	h.s.send = func(string) error { mu.Lock(); defer mu.Unlock(); return nil }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = h.s.SetEnabled("bybit", "BTCUSDT", i%2 == 0)
			_ = h.s.Status()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			h.s.evaluate(nil)
		}
	}()
	wg.Wait()
}

// lifecycle: 거래소 연결 조회(ExchangeManager 잠금)가 막혀도 Status/SetEnabled 는 즉시 반환 (조회는 s.mu 밖).
func TestLiqAlertLookupOutsideLock(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	h.refreshed = h.now
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.s.exchangeUp = func(string) bool {
		once.Do(func() { close(entered) })
		<-release
		return true
	}
	evalDone := make(chan struct{})
	go func() { defer close(evalDone); h.s.evaluate(nil) }()
	<-entered

	callsDone := make(chan struct{})
	var setErr error
	go func() {
		defer close(callsDone)
		_ = h.s.Status()
		setErr = h.s.SetEnabled("okx", "ETH-USDT-SWAP", true)
	}()
	select {
	case <-callsDone:
	case <-time.After(time.Second):
		t.Errorf("Status/SetEnabled blocked while evaluate waits on the exchange lookup")
	}
	close(release) // 실패해도 조회를 풀어 goroutine 누수 없음
	<-evalDone
	<-callsDone
	if setErr != nil {
		t.Fatalf("SetEnabled: %v", setErr)
	}
}

// Start→Stop→Start 후 루프 1개, Stop 후 루프 종료 (전송 불가).
func TestLiqAlertStartStopSingleLoop(t *testing.T) {
	h := newLiqTest(t)
	sent := make(chan struct{}, 100)
	h.s.snapshot = func() (HedgedPositionsResponse, time.Time) {
		return HedgedPositionsResponse{Pairs: []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}}, time.Now()
	}
	h.s.now = time.Now
	h.s.send = func(string) error { sent <- struct{}{}; return errors.New("keep retrying") }
	h.on("bybit", "BTCUSDT")
	h.s.interval = time.Millisecond

	ctx := context.Background()
	h.s.Start(ctx)
	gen1 := h.s.doneCh
	h.s.Start(ctx) // 이미 실행 중 → 새 루프 없음
	if h.s.doneCh != gen1 {
		t.Fatalf("second Start while running must not create a loop")
	}
	h.s.Stop()
	h.s.Start(ctx)
	gen2 := h.s.doneCh
	select {
	case <-gen1:
	default:
		t.Fatalf("first generation still running after Stop")
	}
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatalf("loop did not evaluate")
	}
	h.s.Stop()
	select {
	case <-gen2:
	default:
		t.Fatalf("loop still running after Stop")
	}
}

// lifecycle: 분할 전송 도중 Stop → 남은 분할은 전송하지 않는다.
func TestLiqAlertNoSendAfterStop(t *testing.T) {
	h := newLiqTest(t)
	var pairs []HedgedPositionPair
	for i := 0; i < 60; i++ { // 60 항목 → 4096자 초과 → 2개 이상 분할 (비긴급 40% 단계: 분할 경계의 stop 만 본다)
		sym := fmt.Sprintf("C%02dUSDT", i)
		h.on("bitget", sym)
		pairs = append(pairs, sfPair("bitget", sym, 35))
	}
	h.s.snapshot = func() (HedgedPositionsResponse, time.Time) {
		return HedgedPositionsResponse{Pairs: pairs}, time.Now()
	}
	h.s.now = time.Now
	h.s.interval = time.Millisecond
	started, release := make(chan struct{}), make(chan struct{})
	attempts := 0
	h.s.send = func(string) error {
		attempts++
		if attempts == 1 {
			close(started)
			<-release // 첫 분할 전송 중 Stop
		}
		return nil
	}
	h.s.Start(context.Background())
	stop := h.s.stopCh
	<-started
	stopped := make(chan struct{})
	go func() { h.s.Stop(); close(stopped) }()
	<-stop // Stop 이 세대 stopCh 를 닫은 뒤 첫 분할 전송 완료
	close(release)
	<-stopped
	if attempts != 1 {
		t.Fatalf("parts sent after Stop: attempts=%d, want 1", attempts)
	}
}

// 긴급 (청산 ≤15% 단계·도달/초과, 상승) 이 든 메시지는 같은 내용 10회, 그 외 (≥30% 단계, 상태 메시지) 1회.
// ≤5% 반복/도달·초과 반복은 TestLiqAlertMarkAtOrBeyondLiq, 단계별 반복 횟수는 TestLiqAlertTierCadence.
func TestLiqAlertUrgentCopies(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *liqHarness) // 시작 메시지 발송 후 (두 키 모두 단계 밖) 적용; sfPair 는 entry=mark (상승 0%)
		want  int
		has   []string
	}{
		{"30% tier once", func(h *liqHarness) { h.snap.Pairs[0] = sfPair("bybit", "AUSDT", 25) }, 1, []string{"≤30%"}},
		{"15% tier x10", func(h *liqHarness) { h.snap.Pairs[0] = sfPair("bybit", "AUSDT", 12) }, liqUrgentCopies, []string{"≤15%"}},
		{"5% tier x10", func(h *liqHarness) { h.snap.Pairs[0] = sfPair("bybit", "AUSDT", 4.9) }, liqUrgentCopies, []string{"≤5%"}},
		{"rise +20% outside urgent tiers x10", func(h *liqHarness) {
			leg := h.snap.Pairs[0].FuturesLeg
			leg.EntryPrice = leg.MarkPrice / 1.21
		}, liqUrgentCopies, []string{"[상승 +20%]"}},
		{"mixed non-urgent + urgent packed x10", func(h *liqHarness) {
			h.snap.Pairs[0] = sfPair("bybit", "AUSDT", 25)
			h.snap.Pairs[1] = sfPair("bybit", "BUSDT", 12)
		}, liqUrgentCopies, []string{"≤30%", "≤15%"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLiqTest(t)
			h.on("bybit", "AUSDT")
			h.on("bybit", "BUSDT")
			h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "AUSDT", 150), sfPair("bybit", "BUSDT", 150)}
			var seen int
			h.tick(0)
			if n := h.newSent(&seen); n != 1 || !strings.Contains(h.last(), "모니터링 시작") {
				t.Fatalf("status-only message must be sent once: %d %q", n, h.sent)
			}
			c.setup(h)
			h.tick(5 * time.Second)
			if n := h.newSent(&seen); n != c.want {
				t.Fatalf("want %d sends, got %d: %q", c.want, n, h.sent[1:])
			}
			for _, m := range h.sent[1:] {
				if m != h.sent[1] {
					t.Fatalf("copies must be identical:\n%s\n---\n%s", h.sent[1], m)
				}
			}
			for _, s := range c.has {
				if !strings.Contains(h.last(), s) {
					t.Fatalf("message missing %q:\n%s", s, h.last())
				}
			}
		})
	}
}

// 3번째 반복 실패 → 첫 전송 성공으로 단계는 기록됨 (다음 평가 재알림 없음), 시도 3회에서 중단; 429 면 retryAt.
func TestLiqAlertUrgentLaterCopyFailure(t *testing.T) {
	for _, fail := range []error{errors.New("network"), &notify.APIError{StatusCode: 429, RetryAfter: 30 * time.Second}} {
		h := newLiqTest(t)
		h.on("bybit", "BTCUSDT")
		h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
		h.fail = []error{nil, nil, fail} // 반복 1·2 성공, 3 실패
		h.tick(0)
		if h.attempts != 3 {
			t.Fatalf("%v: stop at the failed copy: attempts=%d", fail, h.attempts)
		}
		is429 := errors.As(fail, new(*notify.APIError))
		if h.s.retryAt.Equal(h.now.Add(30*time.Second)) != is429 {
			t.Fatalf("%v: retryAt=%v (429=%v)", fail, h.s.retryAt, is429)
		}
		h.tick(5 * time.Second)
		h.tick(30 * time.Second) // 429 deadline 이후도 포함
		if h.attempts != 3 {
			t.Fatalf("%v: tier committed by copy 1 must not re-alert: attempts=%d", fail, h.attempts)
		}
	}
}

// 반복 간격 대기 중 stop → 남은 반복 없음. 간격 1시간이라 stop 만 준비된 select 가 되어 결정적.
func TestLiqAlertStopDuringUrgentGap(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 12)}
	h.refreshed = h.now
	h.s.burstGap = time.Hour
	stop := make(chan struct{})
	h.s.send = func(string) error {
		h.attempts++
		if h.attempts == 1 {
			close(stop) // 첫 전송 중 Stop
		}
		return nil
	}
	done := make(chan struct{})
	go func() { defer close(done); h.s.evaluate(stop) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("evaluate still waiting the burst gap after stop")
	}
	if h.attempts != 1 {
		t.Fatalf("copies sent after stop: attempts=%d, want 1", h.attempts)
	}
}

// 5% 단계 체류 중 청산가 도달/초과 → 다음 2분 반복을 기다리지 않고 즉시 1건 (x copies), 반복 주기는 그 발송부터.
func TestLiqAlertBeyondWhileInDeepestTier(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 4)}
	var seen int
	h.tick(0)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[청산 근접 ≤5%]") {
		t.Fatalf("enter 5%%: %d %q", n, h.last())
	}
	setDist(&h.snap.Pairs[0], -1)
	h.tick(30 * time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("crossing liq inside 5%% tier must send immediately: %d %q", n, h.last())
	}
	h.tick(30 * time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("still beyond → no resend before 2 min: %d", n)
	}
	h.tick(2*time.Minute - 30*time.Second - time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("repeat must count from the 도달 send: %d", n)
	}
	h.tick(time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("repeat 2 min after the 도달 send: %d %q", n, h.last())
	}
}

// 청산가 부근 흔들림 → 같은 2분 주기 안에서 재돌파 재발송 없음; d > 0 반복 발송 후의 돌파는 다시 즉시.
func TestLiqAlertBeyondOscillation(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 4)}
	var seen int
	h.tick(0)
	h.newSent(&seen)
	setDist(&h.snap.Pairs[0], -1)
	h.tick(5 * time.Second) // 도달 발송 S
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("setup 도달: %d %q", n, h.last())
	}
	for _, d := range []float64{1, -1, 1, -1} { // S + 80초
		setDist(&h.snap.Pairs[0], d)
		h.tick(20 * time.Second)
	}
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("re-crossing within the same 2-min window sent %d", n)
	}
	setDist(&h.snap.Pairs[0], 1)
	h.tick(40 * time.Second) // S + 2분, d > 0 → 일반 ≤5% 반복
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[청산 근접 ≤5%]") || strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("2-min repeat with d > 0: %d %q", n, h.last())
	}
	setDist(&h.snap.Pairs[0], -1)
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("crossing after a d > 0 repeat must send immediately: %d %q", n, h.last())
	}
}

// 도달 발송 후 얕은 단계로 이탈 (hysteresis) → 발송 없음; 다시 도달 → 즉시 (진입).
func TestLiqAlertBeyondAfterShallowMove(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 4)}
	var seen int
	h.tick(0)
	setDist(&h.snap.Pairs[0], -1)
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 2*liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("setup ≤5%% + 도달: %d %q", n, h.last())
	}
	setDist(&h.snap.Pairs[0], 30)
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("moving shallower must not send: %d", n)
	}
	setDist(&h.snap.Pairs[0], -1)
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("back beyond liq after leaving the tier → immediate: %d %q", n, h.last())
	}
}

// 즉시 도달 발송 실패 → 기록 없음 → 다음 평가 재시도 (성공 후엔 재발송 없음).
func TestLiqAlertBeyondSendFailureRetries(t *testing.T) {
	h := newLiqTest(t)
	h.on("bybit", "BTCUSDT")
	h.snap.Pairs = []HedgedPositionPair{sfPair("bybit", "BTCUSDT", 4)}
	var seen int
	h.tick(0)
	h.newSent(&seen)
	setDist(&h.snap.Pairs[0], -1)
	h.fail = []error{errors.New("network")}
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 0 || h.attempts != liqUrgentCopies+1 {
		t.Fatalf("immediate 도달 send should be attempted once and fail: sent=%d attempts=%d", n, h.attempts)
	}
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != liqUrgentCopies || !strings.Contains(h.last(), "[추정 청산가 도달/초과]") {
		t.Fatalf("failed 도달 send must be retried: %d %q", n, h.last())
	}
	h.tick(5 * time.Second)
	if n := h.newSent(&seen); n != 0 {
		t.Fatalf("no resend after the retried success: %d", n)
	}
}
