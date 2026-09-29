// Package services - LiqAlertService: 사용자가 ON 한 sf 헷지 선물 숏의 청산가 근접 / 진입가 대비 상승 텔레그램 알림.
//
// 5초마다 PositionCache.Snapshot 만 읽는다 (Refresh/REST 호출 없음, WS 가격 미사용).
// 평가 1회의 항목은 한 메시지로 묶는다 (Telegram 4096자 초과 시 항목 경계에서 분할).
// 긴급 항목(청산 ≤15% 단계·도달/초과, 상승 알림)이 든 메시지는 같은 내용을 1초 간격 10회 보낸다 (상태 메시지만이면 1회).
// 발송 상태(단계/반복 시각/상승 레벨/감시 불가·복구/시작 메시지)는 전송 성공 후에만 기록한다 (반복 전송은 첫 성공 시 1회 기록).
// 영속(SQLite): 토글 + 마지막 발송 상승 레벨. 단계·타이머는 메모리 전용 (재시작 시 현재 단계 1회 재알림).
package services

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	localdb "github.com/londonpotato1/position-monitor/internal/db"
	"github.com/londonpotato1/position-monitor/internal/notify"

	"github.com/rs/zerolog"
)

// 청산 거리 단계 (distance ≤ pct, 얕은→깊은 순) 와 체류 중 반복 주기 (0 = 반복 없음).
var liqTiers = []struct {
	pct    float64
	repeat time.Duration
}{
	{100, 0}, {80, 0}, {70, 0}, {60, 0}, {50, 0},
	{40, 4 * time.Hour}, {30, time.Hour}, {15, 15 * time.Minute}, {5, 2 * time.Minute},
}

var riseLevels = []float64{20, 30, 40}

// 토글 가능한 선물 거래소. KuCoin/MEXC/HTX/Lighter 는 mark 신뢰 불가로 미지원.
var liqAlertExchanges = []string{"binance", "bybit", "okx", "gate", "bitget", "hyperliquid"}

const (
	liqTierExitPct    = 2.0 // 단계 t 이탈은 distance > t + 2%p
	riseRearmPct      = 5.0 // 레벨 재무장은 rise < level − 5%p
	liqStaleAfter     = 30 * time.Second
	liqDownAlertAfter = 60 * time.Second
	liqUrgentTierPct  = 15.0 // 이 이하 청산 단계 (15%/5%, 도달/초과) + 상승 알림 = 긴급
	liqUrgentCopies   = 10   // 긴급 메시지 반복 전송 횟수 (간격 burstGap)
	telegramMaxChars  = 4096 // Bot API sendMessage: text 1-4096 characters
	liqSep            = "\n\n"
	liqFooter         = "※ 알림 끄기: 앱 헷지 표의 알림 토글"

	liqWatchOK      = "ok"
	liqWatchNoLiq   = "청산가 없음"
	liqWatchBad     = "데이터 오류"
	liqWatchDown    = "감시 불가"
	liqWatchGone    = "행 없음"
	liqWatchPending = "확인 중"
)

const liqAlertSchema = `
CREATE TABLE IF NOT EXISTS liq_alert_keys (
    exchange   TEXT    NOT NULL,
    symbol     TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 0,
    rise_level REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY (exchange, symbol)
);`

// liqKey 토글 단위 = 선물 거래소 + 원시 선물 심볼.
type liqKey struct{ exchange, symbol string }

// liqState 키별 메모리 상태.
type liqState struct {
	tier      int       // 현재 단계 index (-1 = 단계 밖)
	since     time.Time // 반복 주기 기준 (마지막 발송 또는 얕은 단계 이동 시각)
	beyond    bool      // 마지막 단계 발송이 도달/초과 (거리 ≤ 0) → 체류 중 재돌파 즉시 발송 안 함
	downSince time.Time // 감시 불가 시작 (zero = 감시 중)
	downSent  bool      // "감시 불가" 발송됨 → 복구 시 "감시 복구" 1회
	watch     string
}

// liqItem 알림 1줄. commit 은 이 항목이 든 메시지 (첫) 전송 성공 후 s.mu 하에서 실행된다.
// urgent 항목이 하나라도 든 메시지는 liqUrgentCopies 회 반복 전송한다.
type liqItem struct {
	text   string
	urgent bool
	commit func()
}

// LiqAlertKeyStatus ON 키 1개의 감시 상태.
type LiqAlertKeyStatus struct {
	Exchange string `json:"exchange"`
	Symbol   string `json:"symbol"`
	Watch    string `json:"watch"` // ok / 청산가 없음 / 데이터 오류 / 감시 불가 / 행 없음 / 확인 중
}

// LiqAlertStatus 알림 상태 조회 결과.
type LiqAlertStatus struct {
	Keys               []LiqAlertKeyStatus `json:"keys"`
	Deliverable        bool                `json:"deliverable"`
	Reason             string              `json:"reason"`
	SupportedExchanges []string            `json:"supportedExchanges"`
}

// LiqAlertService 청산 근접 / 상승 알림 서비스.
type LiqAlertService struct {
	db     *sql.DB
	logger zerolog.Logger

	// 외부 의존 (생성자가 실제 값으로 채우고 테스트가 교체)
	snapshot       func() (HedgedPositionsResponse, time.Time)
	exchangeUp     func(exchange string) bool // 등록 + 연결 (미등록이면 false)
	now            func() time.Time
	send           func(text string) error
	disabledReason func() error
	interval       time.Duration
	burstGap       time.Duration // 긴급 반복 전송 간격 (테스트 0)

	mu        sync.Mutex
	on        map[liqKey]bool
	rise      map[liqKey]float64 // 마지막 발송 상승 레벨 (0 = 없음)
	state     map[liqKey]*liqState
	startSent bool
	retryAt   time.Time // Telegram 429 retry_after deadline
	running   bool
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewLiqAlertService DB 를 열고 저장된 토글/상승 레벨을 불러온다.
func NewLiqAlertService(dbPath string, posCache *PositionCache, exchangeMgr *ExchangeManager, notifier *notify.TelegramNotifier, logger zerolog.Logger) (*LiqAlertService, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("liq alert db dir: %w", err)
	}
	db, err := localdb.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("liq alert db open: %w", err)
	}
	s := &LiqAlertService{
		db:             db,
		logger:         logger.With().Str("component", "liq_alert").Logger(),
		snapshot:       posCache.Snapshot,
		exchangeUp:     exchangeMgr.IsConnected, // 미등록 거래소는 false (exchange_manager.go IsConnected)
		now:            time.Now,
		send:           notifier.Send,
		disabledReason: notifier.DisabledReason,
		interval:       5 * time.Second,
		burstGap:       time.Second, // Telegram Bots FAQ: 한 채팅에 초당 1건 넘게 보내지 말 것
		on:             make(map[liqKey]bool),
		rise:           make(map[liqKey]float64),
		state:          make(map[liqKey]*liqState),
	}
	if err := s.load(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *LiqAlertService) load() error {
	if _, err := s.db.Exec(liqAlertSchema); err != nil {
		return fmt.Errorf("liq alert schema init: %w", err)
	}
	rows, err := s.db.Query(`SELECT exchange, symbol, enabled, rise_level FROM liq_alert_keys`)
	if err != nil {
		return fmt.Errorf("liq alert load: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k liqKey
		var enabled bool
		var rise float64
		if err := rows.Scan(&k.exchange, &k.symbol, &enabled, &rise); err != nil {
			return fmt.Errorf("liq alert load: %w", err)
		}
		if enabled {
			s.on[k] = true
		}
		s.rise[k] = rise
	}
	return rows.Err()
}

// Close DB 닫기 (Stop 이후 호출).
func (s *LiqAlertService) Close() error {
	return s.db.Close()
}

// SetEnabled 키 토글을 저장한다. 미지원 거래소/빈 심볼/저장 실패는 error (메모리 불변).
func (s *LiqAlertService) SetEnabled(exchange, symbol string, enabled bool) error {
	if !slices.Contains(liqAlertExchanges, exchange) || symbol == "" {
		return fmt.Errorf("알림 미지원: %q %q", exchange, symbol)
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`INSERT INTO liq_alert_keys (exchange, symbol, enabled) VALUES (?, ?, ?)
		ON CONFLICT(exchange, symbol) DO UPDATE SET enabled = excluded.enabled`, exchange, symbol, enabledInt); err != nil {
		return fmt.Errorf("알림 토글 저장 실패: %w", err)
	}
	k := liqKey{exchange, symbol}
	if enabled {
		s.on[k] = true
	} else {
		delete(s.on, k)
		delete(s.state, k) // 다시 ON 하면 현재 단계를 즉시 알림
	}
	s.logger.Info().Str("exchange", exchange).Str("symbol", symbol).Bool("enabled", enabled).Msg("알림 토글 변경")
	return nil
}

// Status ON 키별 감시 상태, 전달 가능 여부/사유, 지원 거래소.
func (s *LiqAlertService) Status() LiqAlertStatus {
	st := LiqAlertStatus{Keys: []LiqAlertKeyStatus{}, Deliverable: true, SupportedExchanges: slices.Clone(liqAlertExchanges)}
	if err := s.disabledReason(); err != nil {
		st.Deliverable, st.Reason = false, err.Error()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.onKeys() {
		w := liqWatchPending
		if ks := s.state[k]; ks != nil && ks.watch != "" {
			w = ks.watch
		}
		st.Keys = append(st.Keys, LiqAlertKeyStatus{Exchange: k.exchange, Symbol: k.symbol, Watch: w})
	}
	return st
}

// Start 5초 평가 루프 시작 (세대별 stopCh/doneCh).
func (s *LiqAlertService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	stopCh, doneCh := make(chan struct{}), make(chan struct{})
	s.stopCh, s.doneCh, s.running, s.startSent = stopCh, doneCh, true, false
	s.mu.Unlock()

	go func() {
		defer close(doneCh)
		defer func() {
			s.mu.Lock()
			if s.doneCh == doneCh {
				s.running = false
			}
			s.mu.Unlock()
		}()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				s.evaluate(stopCh)
			}
		}
	}()
	s.logger.Info().Msg("liq alert started")
}

// Stop 루프 중지. 현재 세대 goroutine 종료를 기다린다 (5초 timeout).
func (s *LiqAlertService) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	stopCh, doneCh := s.stopCh, s.doneCh
	s.running = false
	s.mu.Unlock()

	close(stopCh)
	select {
	case <-doneCh:
		s.logger.Info().Msg("liq alert stopped")
	case <-time.After(5 * time.Second):
		s.logger.Warn().Msg("liq alert stop timeout")
	}
}

// evaluate 1회 평가: 상태 판정은 s.mu 하에서, 전송은 잠금 밖에서, 발송 상태는 성공한 분할분만 기록.
// stop 이 닫히면 (세대 Stop) 남은 분할은 보내지 않는다.
func (s *LiqAlertService) evaluate(stop <-chan struct{}) {
	if s.disabledReason() != nil {
		return // 전달 불가 → 평가 생략, 상태 진행 없음 (마스터 ON 시 현재 상태 즉시 발송)
	}
	up := make(map[string]bool, len(liqAlertExchanges)) // ExchangeManager 잠금 대기를 s.mu 밖에서
	for _, ex := range liqAlertExchanges {
		up[ex] = s.exchangeUp(ex)
	}
	now := s.now()
	s.mu.Lock()
	if len(s.on) == 0 || now.Before(s.retryAt) {
		s.mu.Unlock()
		return
	}
	snap, refreshed := s.snapshot()
	stale := now.Sub(refreshed) > liqStaleAfter
	pairs := make(map[liqKey]*HedgedPositionPair)
	for i := range snap.Pairs {
		if p := &snap.Pairs[i]; p.PairType == "spot_futures" && p.FuturesLeg != nil {
			k := liqKey{p.FuturesLeg.Exchange, p.FuturesLeg.Symbol}
			if pairs[k] == nil {
				pairs[k] = p // 같은 키 여러 행 → 1건
			}
		}
	}
	futFailed, spotFailed := map[string]bool{}, false
	for _, f := range snap.FailedExchanges {
		futFailed[f.Exchange] = futFailed[f.Exchange] || f.Scope == "futures"
		spotFailed = spotFailed || f.Scope == "spot"
	}

	var items []liqItem
	keys := s.onKeys()
	for _, k := range keys {
		ks := s.state[k]
		if ks == nil {
			ks = &liqState{tier: -1}
			s.state[k] = ks
		}
		p := pairs[k]
		switch {
		case stale || (p == nil && (!up[k.exchange] || futFailed[k.exchange] || spotFailed)):
			s.setWatch(k, ks, liqWatchDown) // 감시 불가: 단계 상태 유지
			if ks.downSince.IsZero() {
				ks.downSince = now
			}
			if !ks.downSent && now.Sub(ks.downSince) >= liqDownAlertAfter {
				items = append(items, liqItem{fmt.Sprintf("[감시 불가] %s %s — 60초 넘게 포지션 확인 불가 (단계 상태 유지)", k.exchange, k.symbol), false,
					func() { ks.downSent = true }})
			}
		case p == nil:
			s.setWatch(k, ks, liqWatchGone) // 진짜 사라짐: 메모리 상태 정리, 토글 유지
			if ks.downSent {                // 감시 불가를 보냈으면 종료 1건 발송 후 정리
				items = append(items, liqItem{fmt.Sprintf("[감시 종료 (포지션 없음)] %s %s", k.exchange, k.symbol), false,
					func() { *ks = liqState{tier: -1, watch: liqWatchGone} }})
			} else {
				*ks = liqState{tier: -1, watch: liqWatchGone}
			}
		default:
			if ks.downSent {
				items = append(items, liqItem{fmt.Sprintf("[감시 복구] %s %s — 감시 재개", k.exchange, k.symbol), false,
					func() { ks.downSince, ks.downSent = time.Time{}, false }})
			} else {
				ks.downSince = time.Time{}
			}
			items = append(items, s.judge(k, ks, p, refreshed, now)...)
		}
	}
	if !s.startSent {
		lines := []string{fmt.Sprintf("모니터링 시작 (ON %d개)", len(keys))}
		for _, k := range keys {
			line := "- " + k.exchange + " " + k.symbol
			if w := s.state[k].watch; w != liqWatchOK {
				line += " (" + w + ")"
			}
			lines = append(lines, line)
		}
		items = append([]liqItem{{strings.Join(lines, "\n"), false, func() { s.startSent = true }}}, items...)
	}
	s.mu.Unlock()

	for len(items) > 0 {
		select {
		case <-stop:
			return
		default:
		}
		n, text := 1, items[0].text
		for n < len(items) && utf8.RuneCountInString(text+liqSep+items[n].text+liqSep+liqFooter) <= telegramMaxChars {
			text += liqSep + items[n].text
			n++
		}
		copies := 1
		if slices.ContainsFunc(items[:n], func(it liqItem) bool { return it.urgent }) {
			copies = liqUrgentCopies
		}
		for c := 1; c <= copies; c++ {
			if c > 1 {
				select {
				case <-stop:
					return
				case <-time.After(s.burstGap):
				}
			}
			if err := s.send(text + liqSep + liqFooter); err != nil {
				var apiErr *notify.APIError
				if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
					s.mu.Lock()
					s.retryAt = s.now().Add(apiErr.RetryAfter)
					s.mu.Unlock()
				}
				msg, pending := "알림 전송 실패 — 다음 평가에서 재시도", len(items)
				if c > 1 { // 첫 전송은 성공 → 이 메시지 항목은 발송 처리됨, 남은 반복만 생략
					msg, pending = fmt.Sprintf("긴급 반복 %d/%d번째 전송 실패 — 남은 반복 생략, 미발송 항목은 다음 평가에서 재시도", c, copies), len(items)-n
				}
				s.logger.Warn().Err(err).Int("copy", c).Int("pending", pending).Msg(msg)
				return
			}
			if c == 1 {
				s.mu.Lock()
				for _, it := range items[:n] {
					it.commit()
				}
				s.mu.Unlock()
			}
		}
		items = items[n:]
	}
}

// judge 감시 가능한 키 1개의 청산 근접/상승 판정. 발송 없는 변화(얕은 이동, 재무장)는 즉시 반영.
func (s *LiqAlertService) judge(k liqKey, ks *liqState, p *HedgedPositionPair, snapAt, now time.Time) []liqItem {
	leg := p.FuturesLeg
	mark, entry, lp := leg.MarkPrice, leg.EntryPrice, leg.LiquidationPrice
	liqOK := lp != nil && validPrice(*lp) // mark ≥ liq 는 거리 ≤ 0 → 최심 단계
	switch {
	case !validPrice(mark) || !validPrice(entry) || (lp != nil && !liqOK):
		if ks.watch != liqWatchBad {
			s.logger.Warn().Str("exchange", k.exchange).Str("symbol", k.symbol).Float64("mark", mark).Float64("entry", entry).
				Interface("liq", lp).Msg("불량 입력 — 해당 판정 건너뜀")
		}
		s.setWatch(k, ks, liqWatchBad)
	case lp == nil:
		s.setWatch(k, ks, liqWatchNoLiq)
	default:
		s.setWatch(k, ks, liqWatchOK)
	}
	if !validPrice(mark) {
		return nil
	}

	var items []liqItem
	if liqOK {
		d := (*lp - mark) / mark * 100
		raw := -1
		for i, t := range liqTiers {
			if d <= t.pct {
				raw = i
			}
		}
		cur := ks.tier
		switch {
		case raw < cur && d > liqTiers[cur].pct+liqTierExitPct:
			ks.tier, ks.since, ks.beyond = raw, now, false // 얕은 단계로 이동: 발송 없음, 새 단계 주기는 지금부터
		// 진입, 체류 반복, 또는 최심 단계 체류 중 청산가 도달/초과 (반복을 기다리지 않고 즉시, 주기는 그 발송부터)
		case raw > cur || (d <= 0 && !ks.beyond) || (cur >= 0 && liqTiers[cur].repeat > 0 && now.Sub(ks.since) >= liqTiers[cur].repeat):
			t := max(raw, cur) // 더 깊은 단계 진입이면 raw, 체류 반복이면 cur
			next := "없음"
			if r := liqTiers[t].repeat; r > 0 {
				next = now.Add(r).Format("15:04:05")
			}
			head := fmt.Sprintf("[청산 근접 ≤%.0f%%]", liqTiers[t].pct)
			if d <= 0 {
				head = "[추정 청산가 도달/초과]" // 미리보기(첫 줄)에서 ≤5% 와 구분
			}
			items = append(items, liqItem{liqLine(head, p, snapAt, next), liqTiers[t].pct <= liqUrgentTierPct,
				func() { ks.tier, ks.since, ks.beyond = t, now, d <= 0 }})
		}
	}
	if validPrice(entry) {
		rise := (mark - entry) / entry * 100
		sent, keep, hit := s.rise[k], 0.0, 0.0
		for _, lv := range riseLevels {
			if lv <= sent && rise >= lv-riseRearmPct {
				keep = lv
			}
			if rise >= lv {
				hit = lv
			}
		}
		if keep != sent {
			s.saveRise(k, keep) // 재무장 (발송 없음)
		}
		if hit > keep {
			items = append(items, liqItem{liqLine(fmt.Sprintf("[상승 +%.0f%%]", hit), p, snapAt, "없음"), true,
				func() { s.saveRise(k, hit) }})
		}
	}
	return items
}

func (s *LiqAlertService) saveRise(k liqKey, level float64) {
	s.rise[k] = level
	if _, err := s.db.Exec(`UPDATE liq_alert_keys SET rise_level = ? WHERE exchange = ? AND symbol = ?`, level, k.exchange, k.symbol); err != nil {
		s.logger.Error().Err(err).Str("exchange", k.exchange).Str("symbol", k.symbol).Msg("상승 레벨 저장 실패 — 재시작 후 1회 중복 가능")
	}
}

func (s *LiqAlertService) setWatch(k liqKey, ks *liqState, w string) {
	if ks.watch != w {
		s.logger.Info().Str("exchange", k.exchange).Str("symbol", k.symbol).Str("from", ks.watch).Str("to", w).Msg("감시 상태 변경")
		ks.watch = w
	}
}

func (s *LiqAlertService) onKeys() []liqKey {
	keys := make([]liqKey, 0, len(s.on))
	for k := range s.on {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b liqKey) int {
		return cmp.Or(cmp.Compare(a.exchange, b.exchange), cmp.Compare(a.symbol, b.symbol))
	})
	return keys
}

// liqLine 알림 1줄. 가격은 표와 같은 단위 (÷ PriceScaleFactor), 거리/상승 % 는 factor 무관.
func liqLine(head string, p *HedgedPositionPair, snapAt time.Time, next string) string {
	leg := p.FuturesLeg
	f := leg.PriceScaleFactor
	if !validPrice(f) {
		f = 1
	}
	liq, dist, rise := "없음", "-", "-"
	if lp := leg.LiquidationPrice; lp != nil {
		liq = fmtPrice(*lp / f)
		if validPrice(*lp) && *lp > leg.MarkPrice {
			dist = fmt.Sprintf("%.2f%%", (*lp-leg.MarkPrice)/leg.MarkPrice*100)
		} else if validPrice(*lp) {
			dist = "≤0%, 추정 청산가 도달/초과"
		}
	}
	if validPrice(leg.EntryPrice) {
		rise = fmt.Sprintf("%+.2f%%", (leg.MarkPrice-leg.EntryPrice)/leg.EntryPrice*100)
	}
	return fmt.Sprintf("%s %s\n%s %s\nmark %s / 청산가 %s (청산까지 %s)\n진입가 %s (진입가 대비 %s) / 레버리지 %dx\n스냅샷 %s / 다음 반복 %s",
		head, p.Coin, leg.Exchange, leg.Symbol, fmtPrice(leg.MarkPrice/f), liq, dist, fmtPrice(leg.EntryPrice/f), rise,
		leg.Leverage, snapAt.Format("15:04:05"), next)
}

func validPrice(v float64) bool { return v > 0 && !math.IsInf(v, 0) }

// fmtPrice 유효숫자 8자리. 거래소 앱 가격과 대조하도록 텔레그램 메시지는 8자리를 유지한다 (프론트 표 표시 자릿수와는 별개).
func fmtPrice(v float64) string {
	if !validPrice(v) {
		return "-"
	}
	s := strconv.FormatFloat(v, 'f', max(0, 7-int(math.Floor(math.Log10(v)))), 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}
