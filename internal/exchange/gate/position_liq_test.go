package gate

import (
	"context"
	"io"
	"net/http"
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
