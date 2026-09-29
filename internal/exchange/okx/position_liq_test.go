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
