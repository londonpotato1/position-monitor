package bybit

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type liqTransport func(*http.Request) (*http.Response, error)

func (f liqTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// GetAllPositions passes liqPrice through only when it is a finite positive number string.
func TestGetAllPositionsLiquidationPrice(t *testing.T) {
	for _, raw := range []string{`"30153.7"`, `""`, `"0"`, `"-1"`, `"NaN"`, `"Inf"`, `"bad"`, `12345.6`, `null`, `missing`} {
		t.Run(raw, func(t *testing.T) {
			liq := ""
			if raw != "missing" {
				liq = `,"liqPrice":` + raw
			}
			a := NewAdapter("fixture", "fixture")
			a.client.client.Transport = liqTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v5/market/instruments-info" {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"retCode":0,"retMsg":"OK","result":{"list":[],"nextPageCursor":""}}`)), Header: make(http.Header)}, nil
				}
				if r.URL.Path != "/v5/position/list" {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				body := `{"retCode":0,"retMsg":"OK","result":{"list":[` +
					`{"positionIdx":0,"symbol":"BTCUSDT","side":"Sell","size":"300","avgPrice":"27464.50441675","markPrice":"28224.50","bustPrice":"","leverage":"10","unrealisedPnl":"-0.00029413"` + liq + `},` +
					`{"positionIdx":0,"symbol":"ETHUSDT","side":"","size":"0","avgPrice":"0","markPrice":"1600","liqPrice":"1500","leverage":"10","unrealisedPnl":"0"}` +
					`]}}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})

			positions, err := a.GetAllPositions(context.Background())
			if err != nil || len(positions) != 1 {
				t.Fatalf("positions=%v err=%v", positions, err)
			}
			p := positions[0]
			if p.Symbol != "BTCUSDT" || p.Side != "short" || p.Size != 300 || p.EntryPrice != 27464.50441675 ||
				p.MarkPrice != 28224.50 || p.UnrealizedPL != -0.00029413 || p.Leverage != 10 {
				t.Fatalf("existing semantics changed: %+v", p)
			}
			if raw == `"30153.7"` {
				if p.LiquidationPrice == nil || *p.LiquidationPrice != 30153.7 {
					t.Fatalf("liquidation price not passed through: %v", p.LiquidationPrice)
				}
			} else if p.LiquidationPrice != nil {
				t.Fatalf("invalid liqPrice %s must be nil, got %v", raw, *p.LiquidationPrice)
			}
		})
	}
}

// tradFiFixture position/list 는 고정 포지션, instruments-info 는 instruments(status/cursor 별 응답) 를 돌려주고 요청 query 를 기록한다.
func tradFiFixture(t *testing.T, instruments func(q url.Values) string, sent *[]url.Values) liqTransport {
	return func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/v5/position/list":
			body = `{"retCode":0,"retMsg":"OK","result":{"list":[` +
				`{"symbol":"TSLAUSDT","side":"Buy","size":"2","avgPrice":"300","markPrice":"310","leverage":"5","unrealisedPnl":"20"},` +
				`{"symbol":"SPYUSDT","side":"Sell","size":"1","avgPrice":"600","markPrice":"590","leverage":"5","unrealisedPnl":"10"},` +
				`{"symbol":"OPENAIUSDT","side":"Buy","size":"4","avgPrice":"100","markPrice":"101","leverage":"2","unrealisedPnl":"4"},` +
				`{"symbol":"NEWCOINUSDT","side":"Buy","size":"10","avgPrice":"1","markPrice":"1.1","leverage":"2","unrealisedPnl":"1"},` +
				`{"symbol":"BTCUSDT","side":"Sell","size":"0.1","avgPrice":"70000","markPrice":"69000","leverage":"10","unrealisedPnl":"100"}]}}`
		case "/v5/market/instruments-info":
			*sent = append(*sent, r.URL.Query())
			body = instruments(r.URL.Query())
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

// AC: 기본 조회(페이지네이션) + status=PreLaunch 조회의 symbolType stock/ETF/commodity/forex 만 TradFi, 재호출은 캐시 사용
func TestGetAllPositionsTradFi(t *testing.T) {
	var sent []url.Values
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, func(q url.Values) string {
		switch {
		case q.Get("status") == "" && q.Get("cursor") == "":
			return `{"retCode":0,"retMsg":"OK","result":{"category":"linear","list":[` +
				`{"symbol":"BTCUSDT","symbolType":""},{"symbol":"NEWCOINUSDT","symbolType":"innovation"}],"nextPageCursor":"first%3D500"}}`
		case q.Get("status") == "" && q.Get("cursor") == "first%3D500":
			return `{"retCode":0,"retMsg":"OK","result":{"category":"linear","list":[` +
				`{"symbol":"TSLAUSDT","symbolType":"stock"},{"symbol":"SPYUSDT","symbolType":"ETF"}],"nextPageCursor":""}}`
		case q.Get("status") == "PreLaunch" && q.Get("cursor") == "":
			return `{"retCode":0,"retMsg":"OK","result":{"category":"linear","list":[` +
				`{"symbol":"OPENAIUSDT","symbolType":"stock"}],"nextPageCursor":""}}`
		}
		t.Fatalf("unexpected instruments-info query %v", q)
		return ""
	}, &sent)

	want := map[string]bool{"TSLAUSDT": true, "SPYUSDT": true, "OPENAIUSDT": true, "NEWCOINUSDT": false, "BTCUSDT": false}
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
	if len(sent) != 3 {
		t.Fatalf("instruments-info requested %d times, want 3 (2 pages + PreLaunch, then cached)", len(sent))
	}
	for _, q := range sent {
		if q.Get("category") != "linear" || q.Get("limit") != "1000" {
			t.Fatalf("missing category=linear / limit=1000: %v", q)
		}
	}
}

// AC: 커서가 A→B→A 로 순환해도 페이지 상한에서 멈추고 실패 처리 (포지션은 TradFi=false 로 반환)
func TestGetAllPositionsTradFiCursorCycle(t *testing.T) {
	var sent []url.Values
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, func(q url.Values) string {
		next := map[string]string{"": "A", "A": "B", "B": "A"}[q.Get("cursor")]
		return `{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"TSLAUSDT","symbolType":"stock"}],"nextPageCursor":"` + next + `"}}`
	}, &sent)

	positions, err := a.GetAllPositions(context.Background())
	if err != nil || len(positions) != 5 {
		t.Fatalf("positions=%v err=%v", positions, err)
	}
	for _, p := range positions {
		if p.TradFi {
			t.Fatalf("%s TradFi=true from a partial list", p.Symbol)
		}
	}
	if len(sent) != instrumentsMaxPages {
		t.Fatalf("instruments-info requested %d times, want %d (page cap)", len(sent), instrumentsMaxPages)
	}
}

// AC: instruments-info 실패 시 포지션은 그대로 TradFi=false 로 반환
func TestGetAllPositionsTradFiLoadFailure(t *testing.T) {
	var sent []url.Values
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, func(url.Values) string {
		return `{"retCode":10001,"retMsg":"fixture error","result":{}}`
	}, &sent)

	positions, err := a.GetAllPositions(context.Background())
	if err != nil || len(positions) != 5 {
		t.Fatalf("positions=%v err=%v", positions, err)
	}
	for _, p := range positions {
		if p.TradFi {
			t.Fatalf("%s TradFi=true after load failure", p.Symbol)
		}
	}
	if p := positions[0]; p.Symbol != "TSLAUSDT" || p.Side != "long" || p.Size != 2 || p.EntryPrice != 300 || p.Leverage != 5 {
		t.Fatalf("position changed: %+v", p)
	}
}
