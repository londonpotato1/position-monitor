package bybit

import (
	"context"
	"io"
	"net/http"
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
