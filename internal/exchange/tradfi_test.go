package exchange

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// AC: 재조회가 0개 성공 또는 실패면 이전 목록 유지
func TestTradFiSetKeepsPreviousOnBadReload(t *testing.T) {
	for name, reload := range map[string]func() (map[string]bool, error){
		"empty":   func() (map[string]bool, error) { return map[string]bool{}, nil },
		"failure": func() (map[string]bool, error) { return nil, errors.New("fixture") },
	} {
		t.Run(name, func(t *testing.T) {
			var s TradFiSet
			s.Mark("fixture", []*Position{{Symbol: "CAT"}}, func() (map[string]bool, error) {
				return map[string]bool{"CAT": true}, nil
			})

			s.nextAt = time.Time{} // 갱신 시점 강제
			calls := 0
			positions := []*Position{{Symbol: "CAT"}, {Symbol: "BTC"}}
			s.Mark("fixture", positions, func() (map[string]bool, error) {
				calls++
				return reload()
			})
			if calls != 1 || !positions[0].TradFi || positions[1].TradFi {
				t.Fatalf("reload calls=%d CAT=%v BTC=%v, want previous list kept", calls, positions[0].TradFi, positions[1].TradFi)
			}
		})
	}
}

// AC: 포지션이 없으면 목록을 조회하지 않음
func TestTradFiSetNoPositionsSkipsLoad(t *testing.T) {
	var s TradFiSet
	for _, positions := range [][]*Position{nil, {}} {
		s.Mark("fixture", positions, func() (map[string]bool, error) {
			t.Fatal("load called with zero positions")
			return nil, nil
		})
	}
}

// AC: 조회 실패 직후 다시 불러도 재시도 간격 전에는 조회하지 않음
func TestTradFiSetRetryDelayAfterFailure(t *testing.T) {
	var s TradFiSet
	calls := 0
	load := func() (map[string]bool, error) {
		calls++
		return nil, errors.New("fixture")
	}
	for i := 0; i < 2; i++ {
		positions := []*Position{{Symbol: "CAT"}}
		s.Mark("fixture", positions, load)
		if positions[0].TradFi {
			t.Fatalf("call %d: TradFi=true without a list", i)
		}
	}
	if calls != 1 {
		t.Fatalf("load called %d times, want 1 (retry delay)", calls)
	}
}

// AC: 동시 호출해도 목록 조회는 한 번 (go test -race)
func TestTradFiSetConcurrentMarkLoadsOnce(t *testing.T) {
	var s TradFiSet
	var calls atomic.Int32
	load := func() (map[string]bool, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond) // 조회 중에 다른 호출자가 들어오게
		return map[string]bool{"CAT": true}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Mark("fixture", []*Position{{Symbol: "CAT"}}, load)
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("load called %d times, want 1", n)
	}
}
