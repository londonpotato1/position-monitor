package okx

import "testing"

// OKX 는 2026-10-31 부터 공인망 WebSocket 8443 포트를 받지 않는다 — 기본 443 포트 URL 고정.
func TestWSURLsUseDefaultPort(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{publicWSURL, "wss://ws.okx.com/ws/v5/public"},
		{privateWSURL, "wss://ws.okx.com/ws/v5/private"},
	} {
		if tc.got != tc.want {
			t.Errorf("WS URL = %q, want %q", tc.got, tc.want)
		}
	}
}
