package upbit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/londonpotato1/position-monitor/internal/gap"
	"github.com/rs/zerolog"
)

// Upbit 은 websocket-message 를 초당 5회로 제한한다 — 초과하면 TOO_MANY_REQUEST 후 close 1000.
// 구독 메시지는 codes 집합이 실제로 바뀔 때만 보내야 한다.

// connectRecorder httptest WS 서버에 연결된 클라이언트를 만든다.
// 반환 함수는 연결을 닫고, 서버가 받은 구독 메시지마다의 codes(정렬)를 돌려준다.
func connectRecorder(t *testing.T) (*PublicWSClient, func() [][]string) {
	t.Helper()
	got := make(chan [][]string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		var msgs [][]string
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				got <- msgs
				return
			}
			var parts []struct {
				Codes []string `json:"codes"`
			}
			if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 3 {
				t.Errorf("unexpected subscribe message: %s", raw)
				continue
			}
			sort.Strings(parts[1].Codes)
			msgs = append(msgs, parts[1].Codes)
		}
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := NewPublicWSClient(zerolog.Nop())
	c.conn, c.connected = conn, true

	return c, func() [][]string {
		conn.Close()
		select {
		case msgs := <-got:
			return msgs
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for server to observe close")
			return nil
		}
	}
}

func TestSubscribeSendsOnlyWhenCodesChange(t *testing.T) {
	c, finish := connectRecorder(t)

	coins := []string{"BTC", "ETH", "XRP"}
	for round := 0; round < 2; round++ { // round 1 = Manager.resubscribe 경로, codes 불변
		for _, coin := range coins {
			if err := c.SubscribeTicker(coin, gap.MarketSpot); err != nil {
				t.Fatal(err)
			}
			if err := c.SubscribeOrderbook(coin, gap.MarketSpot); err != nil {
				t.Fatal(err)
			}
		}
	}

	msgs := finish()
	want := [][]string{
		{"KRW-BTC.15"},
		{"KRW-BTC.15", "KRW-ETH.15"},
		{"KRW-BTC.15", "KRW-ETH.15", "KRW-XRP.15"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("subscribe messages sent = %d, want %d (one per new coin): %v", len(msgs), len(want), msgs)
	}
	for i := range want {
		if strings.Join(msgs[i], ",") != strings.Join(want[i], ",") {
			t.Errorf("message %d codes = %v, want %v", i, msgs[i], want[i])
		}
	}
}

func TestUnsubscribeSendsOnlyWhenCoinLeavesUnion(t *testing.T) {
	c, finish := connectRecorder(t)

	for _, coin := range []string{"BTC", "ETH"} {
		if err := c.SubscribeTicker(coin, gap.MarketSpot); err != nil {
			t.Fatal(err)
		}
		if err := c.SubscribeOrderbook(coin, gap.MarketSpot); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.UnsubscribeTicker("ETH", gap.MarketSpot); err != nil { // ETH 는 orderbook 에 남음 → 전송 없음
		t.Fatal(err)
	}
	if err := c.UnsubscribeOrderbook("ETH", gap.MarketSpot); err != nil { // ETH 가 union 에서 빠짐 → 1회 전송
		t.Fatal(err)
	}

	msgs := finish()
	if len(msgs) != 3 {
		t.Fatalf("subscribe messages sent = %d, want 3 (BTC add, ETH add, ETH leave): %v", len(msgs), msgs)
	}
	if last := strings.Join(msgs[2], ","); last != "KRW-BTC.15" {
		t.Errorf("last message codes = %s, want KRW-BTC.15", last)
	}
}
