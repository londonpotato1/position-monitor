package gate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type positionsTransport func(*http.Request) (*http.Response, error)

func (f positionsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A Gate position carries LiquidationPrice only when liq_price is a finite positive string.
func TestGetAllPositionsLiquidationPrice(t *testing.T) {
	for _, liqField := range []string{`,"liq_price":"53615.4"`, `,"liq_price":""`, `,"liq_price":"0"`, `,"liq_price":"-1"`, `,"liq_price":"NaN"`, `,"liq_price":"Inf"`, `,"liq_price":"bad"`, `,"liq_price":12345.6`, `,"liq_price":null`, ``} {
		t.Run(liqField, func(t *testing.T) {
			c := NewClient("fixture", "fixture")
			c.client.Transport = positionsTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v4/futures/usdt/positions" {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				body := `[{"contract":"BTC_USDT","size":-2,"entry_price":"62000","mark_price":"62891.9","unrealised_pnl":"-1783.8","leverage":"10"` + liqField + `},` +
					`{"contract":"ETH_USDT","size":0,"entry_price":"0","mark_price":"3000","unrealised_pnl":"0","leverage":"10","liq_price":"0"}]`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			positions, err := c.GetAllPositions(context.Background())
			if err != nil || len(positions) != 1 {
				t.Fatalf("position lost or size-0 row kept: %v %v", positions, err)
			}
			p := positions[0]
			if p.Symbol != "BTC_USDT" || p.Side != "short" || p.Size != 2 || p.EntryPrice != 62000 || p.MarkPrice != 62891.9 || p.UnrealizedPL != -1783.8 || p.Leverage != 10 {
				t.Fatalf("existing semantics changed: %+v", p)
			}
			if liqField == `,"liq_price":"53615.4"` {
				if p.LiquidationPrice == nil || *p.LiquidationPrice != 53615.4 {
					t.Fatalf("liquidation price not parsed: %v", p.LiquidationPrice)
				}
			} else if p.LiquidationPrice != nil {
				t.Fatalf("invalid liq_price %s must be nil, got %v", liqField, *p.LiquidationPrice)
			}
		})
	}
}

// tradFiFixture futures positions 는 고정 포지션, futures contracts 는 contracts(query 별 응답) 를 돌려주고 요청 query 를 기록한다.
func tradFiFixture(t *testing.T, contracts func(q url.Values) string, sent *[]url.Values) positionsTransport {
	return func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/api/v4/futures/usdt/positions":
			body = `[{"contract":"CAT_USDT","size":-3,"entry_price":"390","mark_price":"400","unrealised_pnl":"-30","leverage":"5"},` +
				`{"contract":"XAU_USDT","size":1,"entry_price":"4000","mark_price":"4010","unrealised_pnl":"10","leverage":"2"},` +
				`{"contract":"HK50_USDT","size":2,"entry_price":"25000","mark_price":"25100","unrealised_pnl":"20","leverage":"3"},` +
				`{"contract":"BTC_USDT","size":-1,"entry_price":"70000","mark_price":"69000","unrealised_pnl":"1000","leverage":"10"}]`
		case "/api/v4/futures/usdt/contracts":
			*sent = append(*sent, r.URL.Query())
			body = contracts(r.URL.Query())
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

// fillerContracts n 개의 코인(contract_type "") 계약 JSON 항목
func fillerContracts(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"name":"COIN%d_USDT","contract_type":""}`, i)
	}
	return items
}

// AC: limit=100 offset 페이지네이션(100개 미만 쪽에서 끝)으로 contract_type 이 있는 계약만 TradFi, 재호출은 캐시 사용
func TestGetAllPositionsTradFi(t *testing.T) {
	var sent []url.Values
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, func(q url.Values) string {
		switch q.Get("offset") {
		case "0":
			return "[" + strings.Join(append(fillerContracts(98),
				`{"name":"XAU_USDT","contract_type":"metals"}`,
				`{"name":"BTC_USDT","contract_type":""}`), ",") + "]"
		case "100":
			return `[{"name":"CAT_USDT","contract_type":"stocks"},{"name":"HK50_USDT","contract_type":"indices"}]`
		}
		t.Fatalf("unexpected contracts query %v", q)
		return ""
	}, &sent)

	want := map[string]bool{"CAT_USDT": true, "XAU_USDT": true, "HK50_USDT": true, "BTC_USDT": false}
	for i := 0; i < 2; i++ {
		positions, err := a.GetAllPositions(context.Background())
		if err != nil || len(positions) != len(want) {
			t.Fatalf("call %d: positions=%v err=%v", i, positions, err)
		}
		for _, p := range positions {
			if p.TradFi != want[p.Symbol] {
				t.Fatalf("call %d: %s TradFi=%v", i, p.Symbol, p.TradFi)
			}
		}
	}
	if len(sent) != 2 {
		t.Fatalf("contracts requested %d times, want 2 (2 pages, then cached)", len(sent))
	}
	for i, q := range sent {
		if q.Get("limit") != "100" || q.Get("offset") != fmt.Sprint(i*100) {
			t.Fatalf("request %d: want limit=100 offset=%d, got %v", i, i*100, q)
		}
	}
}

// AC: contracts 에러 본문(HTTP 상태 무관) 시 포지션은 그대로 TradFi=false 로 반환 — 중간 쪽에서 실패해도 앞 쪽의 부분 목록을 쓰지 않음
func TestGetAllPositionsTradFiLoadFailure(t *testing.T) {
	const errBody = `{"label":"INVALID_PARAM_VALUE","message":"x"}`
	firstPage := "[" + strings.Join(append(fillerContracts(99), `{"name":"CAT_USDT","contract_type":"stocks"}`), ",") + "]"
	for name, tc := range map[string]struct {
		pages    map[string]string // offset → 응답 본문
		requests int
	}{
		"첫 쪽 실패":  {map[string]string{"0": errBody}, 1},
		"중간 쪽 실패": {map[string]string{"0": firstPage, "100": errBody}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			var sent []url.Values
			a := NewAdapter("fixture", "fixture")
			a.client.client.Transport = tradFiFixture(t, func(q url.Values) string {
				return tc.pages[q.Get("offset")]
			}, &sent)

			positions, err := a.GetAllPositions(context.Background())
			if err != nil || len(positions) != 4 || len(sent) != tc.requests {
				t.Fatalf("positions=%v err=%v contracts requests=%d, want %d", positions, err, len(sent), tc.requests)
			}
			for _, p := range positions {
				if p.TradFi {
					t.Fatalf("%s TradFi=true after load failure", p.Symbol)
				}
			}
			if p := positions[0]; p.Symbol != "CAT_USDT" || p.Side != "short" || p.Size != 3 || p.EntryPrice != 390 || p.Leverage != 5 {
				t.Fatalf("position changed: %+v", p)
			}
		})
	}
}

// AC: 모든 페이지가 꽉 차도 페이지 상한에서 멈추고 실패 처리 (부분 목록으로 TradFi 표시 안 함)
func TestGetAllPositionsTradFiPageCap(t *testing.T) {
	var sent []url.Values
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, func(url.Values) string {
		return "[" + strings.Join(append(fillerContracts(99), `{"name":"CAT_USDT","contract_type":"stocks"}`), ",") + "]"
	}, &sent)

	positions, err := a.GetAllPositions(context.Background())
	if err != nil || len(positions) != 4 {
		t.Fatalf("positions=%v err=%v", positions, err)
	}
	for _, p := range positions {
		if p.TradFi {
			t.Fatalf("%s TradFi=true from a partial list", p.Symbol)
		}
	}
	if len(sent) != contractsMaxPages {
		t.Fatalf("contracts requested %d times, want %d (page cap)", len(sent), contractsMaxPages)
	}
}
