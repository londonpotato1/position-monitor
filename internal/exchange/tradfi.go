package exchange

import (
	"errors"
	"sync"
	"time"

	"github.com/londonpotato1/position-monitor/pkg/logger"
)

// TradFi 심볼 목록 갱신 주기 / 조회 실패 후 재시도 간격
const (
	tradFiRefresh    = time.Hour
	tradFiRetryDelay = time.Minute
)

// TradFiSet 거래소 상품정보의 TradFi(주식·ETF·원자재·외환) 선물 심볼 캐시. zero value 로 사용.
type TradFiSet struct {
	mu      sync.Mutex
	symbols map[string]bool
	nextAt  time.Time
}

// Mark 각 포지션의 TradFi 를 심볼 목록 기준으로 설정. 갱신 시점이면 load 로 목록을 다시 읽는다.
// 조회 실패 시 이전 목록 유지 — 한 번도 못 읽었으면 전부 false (기존 이름 기반 매칭).
func (s *TradFiSet) Mark(exchangeName string, positions []*Position, load func() (map[string]bool, error)) {
	if len(positions) == 0 {
		return
	}
	s.mu.Lock()
	due := !time.Now().Before(s.nextAt)
	if due {
		s.nextAt = time.Now().Add(tradFiRetryDelay) // 재조회 선점 — 동시 호출자는 현재 목록 사용
	}
	symbols := s.symbols
	s.mu.Unlock()

	if due {
		loaded, err := load()
		if err == nil && len(loaded) == 0 {
			// 실제로는 수백 개 — 0개면 응답 형식이 바뀐 것이라 보호가 조용히 꺼지지 않게 실패로 본다
			err = errors.New("TradFi 0개 (응답 형식 변경 의심)")
		}
		if err != nil {
			logger.Warnf("%s TradFi 목록 조회 실패 (이전 %d개 유지): %v", exchangeName, len(symbols), err)
		} else {
			symbols = loaded
			s.mu.Lock()
			s.symbols, s.nextAt = loaded, time.Now().Add(tradFiRefresh)
			s.mu.Unlock()
			logger.Infof("%s TradFi %d개", exchangeName, len(loaded))
		}
	}
	for _, p := range positions {
		p.TradFi = symbols[p.Symbol]
	}
}
