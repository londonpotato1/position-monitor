package exchange

import "testing"

func TestSymbolRegistryMappings(t *testing.T) {
	cases := []struct {
		name       string
		resolve    func() (string, float64)
		wantBase   string
		wantFactor float64
	}{
		// PROS(Pharos Network): Binance/Bybit 선물만 PHAROS로 상장
		{"binance PROS→PHAROS", func() (string, float64) { return ResolveFuturesMapping("binance", "PROS") }, "PHAROS", 1},
		{"bybit PROS→PHAROS", func() (string, float64) { return ResolveFuturesMapping("bybit", "PROS") }, "PHAROS", 1},
		{"okx PROS unchanged", func() (string, float64) { return ResolveFuturesMapping("okx", "PROS") }, "PROS", 1},
		{"gate PROS unchanged", func() (string, float64) { return ResolveFuturesMapping("gate", "PROS") }, "PROS", 1},
		{"bitget PROS unchanged", func() (string, float64) { return ResolveFuturesMapping("bitget", "PROS") }, "PROS", 1},
		{"reverse PHAROS→PROS", func() (string, float64) { return ResolveSpotSymbol("PHAROS") }, "PROS", 1},
		// 회귀 가드: 기존 이름 변경 매핑
		{"binance RAY→RAYSOL", func() (string, float64) { return ResolveFuturesMapping("binance", "RAY") }, "RAYSOL", 1},
	}
	for _, tc := range cases {
		gotBase, gotFactor := tc.resolve()
		if gotBase != tc.wantBase || gotFactor != tc.wantFactor {
			t.Errorf("%s: got (%s, %v), want (%s, %v)", tc.name, gotBase, gotFactor, tc.wantBase, tc.wantFactor)
		}
	}
}

// 어댑터 조회 경로(GetFuturesTicker / GetPosition 등)는 받은 base를 ResolveFuturesBase로 다시 변환한다 → 선물 base가 같은 거래소의 현물 키이면 이중 변환된다.
func TestSymbolRegistryFuturesBaseNotSpotKey(t *testing.T) {
	for exName, spotMap := range FuturesRegistry {
		for spotBase, mapping := range spotMap {
			if _, ok := spotMap[mapping.FuturesBase]; ok {
				t.Errorf("%s: %s→%s, but %s is also a spot key (double resolve)", exName, spotBase, mapping.FuturesBase, mapping.FuturesBase)
			}
		}
	}
}
