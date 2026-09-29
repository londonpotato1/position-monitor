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
