package binance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type displayTransport func(*http.Request) (*http.Response, error)

func (f displayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AC3: optional liquidation data must survive API parsing without losing the position.
func TestPositionDisplayLiquidation(t *testing.T) {
	for _, raw := range []string{`"3.25"`, `null`, `""`, `"0"`, `"-1"`, `"NaN"`, `"Inf"`, `"bad"`, `3.25`, `missing`} {
		t.Run(raw, func(t *testing.T) {
			extra := ""
			if raw != "missing" {
				extra = `,"liquidationPrice":` + raw
			}
			c := NewClient("fixture", "fixture")
			c.client.Transport = displayTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/fapi/v2/positionRisk" {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"symbol":"TESTUSDT","positionSide":"BOTH","positionAmt":"-1000","markPrice":"2","entryPrice":"1.9","unRealizedProfit":"-100","leverage":"5"` + extra + `}]`)), Header: make(http.Header)}, nil
			})
			positions, err := c.GetAllPositions(context.Background())
			if err != nil || len(positions) != 1 {
				t.Fatalf("optional field lost position: %v %v", positions, err)
			}
			p := positions[0]
			if p.Size != 1000 || p.MarkPrice != 2 || p.Side != "short" || p.UnrealizedPL != -100 {
				t.Fatalf("existing semantics changed: %+v", p)
			}
			data, _ := json.Marshal(p)
			var observed map[string]interface{}
			json.Unmarshal(data, &observed)
			if raw == `"3.25"` {
				if observed["LiquidationPrice"] != 3.25 {
					t.Fatalf("liquidation price discarded: %s", data)
				}
			} else if value := observed["LiquidationPrice"]; value != nil {
				t.Fatalf("invalid optional price: %v", value)
			}
		})
	}
}

// tradFiFixture positionRisk 는 positions 를, 전체 exchangeInfo 는 info(status 200) 또는 HTTP 500 을 돌려주고 exchangeInfo 요청 수를 센다.
func tradFiFixture(t *testing.T, positions, info string, infoCalls *int) displayTransport {
	return func(r *http.Request) (*http.Response, error) {
		status, body := 200, positions
		switch r.URL.Path {
		case "/fapi/v2/positionRisk":
		case "/fapi/v1/exchangeInfo":
			*infoCalls++
			if r.URL.RawQuery != "" {
				t.Fatalf("exchangeInfo must request all symbols, got query %q", r.URL.RawQuery)
			}
			body = info
			if info == "" {
				status, body = 500, `{"code":-1000,"msg":"fixture"}`
			}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
}

const tradFiPositions = `[` +
	`{"symbol":"CATUSDT","positionSide":"BOTH","positionAmt":"-3","markPrice":"400","entryPrice":"390","unRealizedProfit":"-30","leverage":"5"},` +
	`{"symbol":"1000CATUSDT","positionSide":"BOTH","positionAmt":"5000","markPrice":"0.01","entryPrice":"0.011","unRealizedProfit":"-5","leverage":"3"},` +
	`{"symbol":"PAXGUSDT","positionSide":"BOTH","positionAmt":"1","markPrice":"4000","entryPrice":"3990","unRealizedProfit":"10","leverage":"2"}]`

// AC: 전체 exchangeInfo 의 contractType=TRADIFI_PERPETUAL 심볼만 TradFi, 1시간 내 재호출은 캐시 사용
func TestGetAllPositionsTradFi(t *testing.T) {
	calls := 0
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, tradFiPositions, `{"symbols":[`+
		`{"symbol":"CATUSDT","contractType":"TRADIFI_PERPETUAL"},`+
		`{"symbol":"1000CATUSDT","contractType":"PERPETUAL"},`+
		`{"symbol":"PAXGUSDT","contractType":"PERPETUAL"},`+
		`{"symbol":"BTCUSDT_261225","contractType":"CURRENT_QUARTER"}]}`, &calls)

	for i := 0; i < 2; i++ {
		positions, err := a.GetAllPositions(context.Background())
		if err != nil || len(positions) != 3 {
			t.Fatalf("call %d: positions=%v err=%v", i, positions, err)
		}
		for _, p := range positions {
			if p.TradFi != (p.Symbol == "CATUSDT") {
				t.Fatalf("call %d: %s TradFi=%v", i, p.Symbol, p.TradFi)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("exchangeInfo requested %d times, want 1 (cached)", calls)
	}
}

// AC: exchangeInfo 실패 시 포지션은 그대로 TradFi=false 로 반환, 곧바로 재조회하지 않음
func TestGetAllPositionsTradFiLoadFailure(t *testing.T) {
	calls := 0
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, tradFiPositions, "", &calls)

	for i := 0; i < 2; i++ {
		positions, err := a.GetAllPositions(context.Background())
		if err != nil || len(positions) != 3 {
			t.Fatalf("call %d: positions=%v err=%v", i, positions, err)
		}
		if p := positions[0]; p.Symbol != "CATUSDT" || p.Side != "short" || p.Size != 3 || p.TradFi {
			t.Fatalf("call %d: position changed: %+v", i, p)
		}
	}
	if calls != 1 {
		t.Fatalf("exchangeInfo requested %d times, want 1 (retry delay)", calls)
	}
}

// AC: 포지션이 없으면 exchangeInfo 를 요청하지 않음
func TestGetAllPositionsTradFiNoPositions(t *testing.T) {
	calls := 0
	a := NewAdapter("fixture", "fixture")
	a.client.client.Transport = tradFiFixture(t, `[]`, `{"symbols":[]}`, &calls)

	positions, err := a.GetAllPositions(context.Background())
	if err != nil || len(positions) != 0 || calls != 0 {
		t.Fatalf("positions=%v err=%v exchangeInfo calls=%d", positions, err, calls)
	}
}
