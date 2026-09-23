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
