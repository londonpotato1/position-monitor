package upbit

import "testing"

// TestParseAssetEvent 업비트 myAsset 파싱 검증
// 업비트는 balance/locked를 따옴표 없는 숫자(Double)로 전송한다.
func TestParseAssetEvent(t *testing.T) {
	tests := []struct {
		name     string
		msg      string
		currency string
		free     float64
		used     float64
		wantNil  bool
	}{
		{
			name:     "따옴표 없는 숫자 (업비트 문서 포맷)",
			msg:      `{"type":"myAsset","asset_timestamp":1710000000000,"assets":[{"currency":"KRW","balance":1386929.37231066771348207123,"locked":10329.67}]}`,
			currency: "KRW",
			free:     1386929.37231066771348207123,
			used:     10329.67,
		},
		{
			name:     "따옴표 있는 숫자 문자열",
			msg:      `{"type":"myAsset","assets":[{"currency":"BTC","balance":"0.5","locked":"0.25"}]}`,
			currency: "BTC",
			free:     0.5,
			used:     0.25,
		},
		{
			name:    "myAsset 아닌 메시지",
			msg:     `{"status":"UP"}`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			balances, err := parseAssetEvent([]byte(tt.msg))
			if err != nil {
				t.Fatalf("parseAssetEvent 오류: %v", err)
			}
			if tt.wantNil {
				if balances != nil {
					t.Fatalf("nil 맵 기대, 실제: %v", balances)
				}
				return
			}
			b := balances[tt.currency]
			if b == nil {
				t.Fatalf("%s 잔고 없음: %v", tt.currency, balances)
			}
			if b.Free != tt.free {
				t.Errorf("Free = %v, 기대 %v", b.Free, tt.free)
			}
			if b.Used != tt.used {
				t.Errorf("Used = %v, 기대 %v", b.Used, tt.used)
			}
			if b.Total != tt.free+tt.used {
				t.Errorf("Total = %v, 기대 %v", b.Total, tt.free+tt.used)
			}
		})
	}
}
