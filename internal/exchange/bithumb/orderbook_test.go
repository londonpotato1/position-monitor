package bithumb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return t.base.RoundTrip(req)
}

func TestGetOrderbookSetsUpdatedAtWithinFiveSeconds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/orderbook" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("markets"); got != "KRW-BTC" {
			t.Fatalf("unexpected markets query: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"market":"KRW-BTC","orderbook_units":[{"bid_price":100.0,"bid_size":1.2,"ask_price":101.0,"ask_size":1.3}]}]`)
	}))
	defer server.Close()

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}

	client := &Client{client: server.Client()}
	client.client.Transport = rewriteTransport{target: target, base: http.DefaultTransport}

	ob, err := client.GetOrderbook(context.Background(), "BTC")
	if err != nil {
		t.Fatalf("GetOrderbook returned error: %v", err)
	}
	if ob.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt is zero")
	}
	if time.Since(ob.UpdatedAt) > 5*time.Second {
		t.Fatalf("UpdatedAt too old: %s", ob.UpdatedAt)
	}
}
