package okx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type positionsTransport func(*http.Request) (*http.Response, error)

func (f positionsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// An OKX short position carries LiquidationPrice only when liqPx is a finite positive number string.
func TestGetAllPositionsLiquidationPrice(t *testing.T) {
	for _, liqPx := range []string{`"53615.448336593756"`, `""`, `"0"`, `"-1"`, `"Inf"`, `"bad"`, `12345.6`, `null`} {
		t.Run(liqPx, func(t *testing.T) {
			c := NewClient("fixture", "fixture", "fixture")
			c.client.Transport = positionsTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v5/account/positions" {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				body := `{"code":"0","msg":"","data":[{"instId":"BTC-USDT-SWAP","posSide":"net","pos":"-2","avgPx":"62000","markPx":"62891.9","upl":"-1783.8","lever":"10","liqPx":` + liqPx + `}]}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			positions, err := c.GetAllPositions(context.Background())
			if err != nil || len(positions) != 1 {
				t.Fatalf("position lost: %v %v", positions, err)
			}
			p := positions[0]
			if p.Symbol != "BTC-USDT-SWAP" || p.Side != "short" || p.Size != 2 || p.EntryPrice != 62000 || p.MarkPrice != 62891.9 || p.UnrealizedPL != -1783.8 || p.Leverage != 10 {
				t.Fatalf("existing semantics changed: %+v", p)
			}
			if liqPx == `"53615.448336593756"` {
				if p.LiquidationPrice == nil || *p.LiquidationPrice != 53615.448336593756 {
					t.Fatalf("liquidation price not parsed: %v", p.LiquidationPrice)
				}
			} else if p.LiquidationPrice != nil {
				t.Fatalf("invalid liqPx %s must be nil, got %v", liqPx, *p.LiquidationPrice)
			}
		})
	}
}

// tradFiFixture account/positions 는 고정 포지션, public/instruments 는 instruments(HTTP status) 를 돌려주고 요청 query 를 기록한다.
func tradFiFixture(t *testing.T, status int, instruments string, sent *[]string) positionsTransport {
	return func(r *http.Request) (*http.Response, error) {
		code, body := 200, ""
		switch r.URL.Path {
		case "/api/v5/account/positions":
			body = `{"code":"0","msg":"","data":[` +
				`{"instId":"BB-USDT-SWAP","posSide":"net","pos":"-3","avgPx":"20","markPx":"21","upl":"-3","lever":"5"},` +
				`{"instId":"QNT-USDT-SWAP","posSide":"net","pos":"2","avgPx":"90","markPx":"91","upl":"2","lever":"5"},` +
				`{"instId":"XAU-USDT-SWAP","posSide":"net","pos":"1","avgPx":"4000","markPx":"4010","upl":"10","lever":"2"},` +
				`{"instId":"BTC-USDT-SWAP","posSide":"net","pos":"-1","avgPx":"70000","markPx":"69000","upl":"1000","lever":"10"},` +
				`{"instId":"NEWCOIN-USDT-SWAP","posSide":"net","pos":"10","avgPx":"1","markPx":"1.1","upl":"1","lever":"2"},` +
				`{"instId":"EURUSD-USDT-SWAP","posSide":"net","pos":"5","avgPx":"1.08","markPx":"1.09","upl":"0.05","lever":"10"},` +
				`{"instId":"US10Y-USDT-SWAP","posSide":"net","pos":"-2","avgPx":"4.1","markPx":"4.2","upl":"-0.2","lever":"5"}]}`
		case "/api/v5/public/instruments":
			*sent = append(*sent, r.URL.RawQuery)
			code, body = status, instruments
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

// AC: SWAP 상품정보의 instCategory 3(주식)·4(원자재)·5(외환)·6(채권) 만 TradFi, 1(코인)·""(미분류) 는 아님, 재호출은 캐시 사용
func TestGetAllPositionsTradFi(t *testing.T) {
	var sent []string
	a := NewAdapter("fixture", "fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, 200, `{"code":"0","msg":"","data":[`+
		`{"instId":"BB-USDT-SWAP","instCategory":"3"},`+
		`{"instId":"QNT-USDT-SWAP","instCategory":"3"},`+
		`{"instId":"XAU-USDT-SWAP","instCategory":"4"},`+
		`{"instId":"EURUSD-USDT-SWAP","instCategory":"5"},`+
		`{"instId":"US10Y-USDT-SWAP","instCategory":"6"},`+
		`{"instId":"BTC-USDT-SWAP","instCategory":"1"},`+
		`{"instId":"NEWCOIN-USDT-SWAP","instCategory":""}]}`, &sent)

	want := map[string]bool{"BB-USDT-SWAP": true, "QNT-USDT-SWAP": true, "XAU-USDT-SWAP": true, "EURUSD-USDT-SWAP": true, "US10Y-USDT-SWAP": true,
		"BTC-USDT-SWAP": false, "NEWCOIN-USDT-SWAP": false}
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
	if len(sent) != 1 || sent[0] != "instType=SWAP" {
		t.Fatalf("instruments requests %q, want exactly one with query instType=SWAP (then cached)", sent)
	}
}

// AC: instruments 실패(code≠"0" 또는 HTTP 500) 시 포지션은 그대로 TradFi=false 로 반환
func TestGetAllPositionsTradFiLoadFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		// data 가 있어도 code≠"0" 이면 쓰지 않는다 (빈 목록 보호와 별개로 code 판정을 검증)
		"code 50011": {200, `{"code":"50011","msg":"fixture error","data":[{"instId":"BB-USDT-SWAP","instCategory":"3"}]}`},
		"HTTP 500":   {500, `{"code":"50000","msg":"fixture error"}`},
	} {
		t.Run(name, func(t *testing.T) {
			var sent []string
			a := NewAdapter("fixture", "fixture", "fixture")
			a.client.client.Transport = tradFiFixture(t, tc.status, tc.body, &sent)

			positions, err := a.GetAllPositions(context.Background())
			if err != nil || len(positions) != 7 || len(sent) != 1 {
				t.Fatalf("positions=%v err=%v instruments requests=%d", positions, err, len(sent))
			}
			for _, p := range positions {
				if p.TradFi {
					t.Fatalf("%s TradFi=true after load failure", p.Symbol)
				}
			}
			if p := positions[0]; p.Symbol != "BB-USDT-SWAP" || p.Side != "short" || p.Size != 3 || p.EntryPrice != 20 || p.Leverage != 5 {
				t.Fatalf("position changed: %+v", p)
			}
		})
	}
}
