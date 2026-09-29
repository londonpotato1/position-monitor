package hyperliquid

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type positionsTransport func(*http.Request) (*http.Response, error)

func (f positionsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Hyperliquid 숏 포지션은 liquidationPx 가 유한 양수 문자열일 때만 LiquidationPrice 를 갖는다.
func TestGetAllPositionsLiquidationPrice(t *testing.T) {
	for _, liqField := range []string{`,"liquidationPx":"2866.26936529"`, `,"liquidationPx":null`, `,"liquidationPx":2866.26`, `,"liquidationPx":""`, `,"liquidationPx":"0"`, `,"liquidationPx":"-1"`, `,"liquidationPx":"Inf"`, `,"liquidationPx":"bad"`, ``} {
		t.Run(liqField, func(t *testing.T) {
			c := &Client{address: "0x0000000000000000000000000000000000000001", baseURL: "https://fixture", client: &http.Client{Transport: positionsTransport(func(r *http.Request) (*http.Response, error) {
				reqBody, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/info" || !strings.Contains(string(reqBody), `"clearinghouseState"`) {
					t.Fatalf("unexpected request %s %s", r.URL.Path, reqBody)
				}
				body := `{"assetPositions":[{"position":{"coin":"ETH","entryPx":"2986.3","leverage":{"rawUsd":"-95.059824","type":"isolated","value":20}` + liqField +
					`,"marginUsed":"4.967826","maxLeverage":50,"positionValue":"100.02765","returnOnEquity":"-0.0026789","szi":"-0.0335","unrealizedPnl":"-0.0134"}},` +
					`{"position":{"coin":"BTC","entryPx":"60000","leverage":{"type":"cross","value":10},"liquidationPx":null,"positionValue":"0","szi":"0.0","unrealizedPnl":"0"}}]}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}}
			positions, err := c.GetAllPositions(context.Background())
			if err != nil || len(positions) != 1 {
				t.Fatalf("position lost or size-0 row kept: %v %v", positions, err)
			}
			p := positions[0]
			posVal, size := 100.02765, 0.0335
			if p.Symbol != "ETH" || p.Side != "short" || p.Size != size || p.EntryPrice != 2986.3 || p.MarkPrice != posVal/size || p.UnrealizedPL != -0.0134 || p.Leverage != 20 {
				t.Fatalf("existing semantics changed: %+v", p)
			}
			if liqField == `,"liquidationPx":"2866.26936529"` {
				if p.LiquidationPrice == nil || *p.LiquidationPrice != 2866.26936529 {
					t.Fatalf("liquidation price not parsed: %v", p.LiquidationPrice)
				}
			} else if p.LiquidationPrice != nil {
				t.Fatalf("invalid liquidationPx %s must be nil, got %v", liqField, *p.LiquidationPrice)
			}
		})
	}
}
