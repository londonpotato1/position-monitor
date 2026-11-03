package bitget

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type positionsTransport func(*http.Request) (*http.Response, error)

func (f positionsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A Bitget position carries LiquidationPrice only when liquidationPrice is a finite positive string.
func TestGetAllPositionsLiquidationPrice(t *testing.T) {
	for _, liq := range []string{`"5737867.8639926850760812"`, `""`, `"0"`, `"-1"`, `"NaN"`, `"Inf"`, `"bad"`, `5737867.8`, `null`} {
		t.Run(liq, func(t *testing.T) {
			c := NewClient("fixture", "fixture", "fixture")
			c.client.Transport = positionsTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v2/mix/position/all-position" {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				body := `{"code":"00000","msg":"success","data":[{"symbol":"BTCUSDT","holdSide":"short","marginMode":"crossed","total":"0.03","openPriceAvg":"88505.233333333333","markPrice":"93203.4","unrealisedPL":"-140.9451","leverage":"20","liquidationPrice":` + liq + `}]}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			positions, err := c.GetAllPositions(context.Background())
			if err != nil || len(positions) != 1 {
				t.Fatalf("position lost: %v %v", positions, err)
			}
			p := positions[0]
			if p.Symbol != "BTCUSDT" || p.Side != "short" || p.Size != 0.03 || p.EntryPrice != 88505.233333333333 || p.MarkPrice != 93203.4 || p.UnrealizedPL != -140.9451 || p.Leverage != 20 {
				t.Fatalf("existing semantics changed: %+v", p)
			}
			if liq == `"5737867.8639926850760812"` {
				if p.LiquidationPrice == nil || *p.LiquidationPrice != 5737867.8639926850760812 {
					t.Fatalf("liquidation price not parsed: %v", p.LiquidationPrice)
				}
			} else if p.LiquidationPrice != nil {
				t.Fatalf("invalid liquidationPrice %s must be nil, got %v", liq, *p.LiquidationPrice)
			}
		})
	}
}

// tradFiFixture all-position 은 고정 포지션, market/contracts 는 contracts(HTTP status) 를 돌려주고 요청 query 를 기록한다.
func tradFiFixture(t *testing.T, status int, contracts string, sent *[]string) positionsTransport {
	return func(r *http.Request) (*http.Response, error) {
		code, body := 200, ""
		switch r.URL.Path {
		case "/api/v2/mix/position/all-position":
			body = `{"code":"00000","msg":"success","data":[` +
				`{"symbol":"CATUSDT","holdSide":"short","total":"3","openPriceAvg":"400","markPrice":"401","unrealisedPL":"-3","leverage":"5"},` +
				`{"symbol":"BPUSDT","holdSide":"long","total":"10","openPriceAvg":"35","markPrice":"36","unrealisedPL":"10","leverage":"5"},` +
				`{"symbol":"BTCUSDT","holdSide":"short","total":"0.01","openPriceAvg":"70000","markPrice":"69000","unrealisedPL":"10","leverage":"10"},` +
				`{"symbol":"NEWUSDT","holdSide":"long","total":"100","openPriceAvg":"1","markPrice":"1.1","unrealisedPL":"10","leverage":"2"}]}`
		case "/api/v2/mix/market/contracts":
			*sent = append(*sent, r.URL.RawQuery)
			code, body = status, contracts
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

// AC: USDT-FUTURES 상품정보의 isRwa "YES" 만 TradFi, "NO"·누락은 아님, 재호출은 캐시 사용
func TestGetAllPositionsTradFi(t *testing.T) {
	var sent []string
	a := NewAdapter("fixture", "fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, 200, `{"code":"00000","msg":"success","data":[`+
		`{"symbol":"CATUSDT","isRwa":"YES"},`+
		`{"symbol":"BPUSDT","isRwa":"YES"},`+
		`{"symbol":"BTCUSDT","isRwa":"NO"},`+
		`{"symbol":"NEWUSDT"}]}`, &sent)

	want := map[string]bool{"CATUSDT": true, "BPUSDT": true, "BTCUSDT": false, "NEWUSDT": false}
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
	if len(sent) != 1 || sent[0] != "productType=USDT-FUTURES" {
		t.Fatalf("contracts requests %q, want exactly one with query productType=USDT-FUTURES (then cached)", sent)
	}
}

// AC: contracts 실패(code≠"00000" 또는 HTTP 500) 시 포지션은 그대로 TradFi=false 로 반환
func TestGetAllPositionsTradFiLoadFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		// data 가 있어도 code≠"00000" 이면 쓰지 않는다 (빈 목록 보호와 별개로 code 판정을 검증)
		"code 40001": {200, `{"code":"40001","msg":"fixture error","data":[{"symbol":"CATUSDT","isRwa":"YES"}]}`},
		"HTTP 500":   {500, `<html>fixture error</html>`},
	} {
		t.Run(name, func(t *testing.T) {
			var sent []string
			a := NewAdapter("fixture", "fixture", "fixture")
			a.client.client.Transport = tradFiFixture(t, tc.status, tc.body, &sent)

			positions, err := a.GetAllPositions(context.Background())
			if err != nil || len(positions) != 4 || len(sent) != 1 {
				t.Fatalf("positions=%v err=%v contracts requests=%d", positions, err, len(sent))
			}
			for _, p := range positions {
				if p.TradFi {
					t.Fatalf("%s TradFi=true after load failure", p.Symbol)
				}
			}
			if p := positions[0]; p.Symbol != "CATUSDT" || p.Side != "short" || p.Size != 3 || p.EntryPrice != 400 || p.Leverage != 5 {
				t.Fatalf("position changed: %+v", p)
			}
		})
	}
}
