package services

import (
	"encoding/json"
	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/rs/zerolog"
	"testing"
)

// AC2/3/4/5: allocated quantities and PnL stay unchanged, prices retain native units.
func TestPositionDisplaySplitAndScale(t *testing.T) {
	for _, tc := range []struct {
		coin, futureCoin  string
		factor, mark, liq float64
	}{
		{"TEST", "TEST", 1, 2, 3}, {"SHIB", "1000SHIB", 1000, .02, .03},
	} {
		t.Run(tc.coin, func(t *testing.T) {
			p := &exchange.Position{Symbol: tc.futureCoin + "USDT", Side: "short", Size: 1000, MarkPrice: tc.mark, UnrealizedPL: 50}
			// JSON observer keeps the pre-implementation test compilable.
			raw, _ := json.Marshal(map[string]float64{"LiquidationPrice": tc.liq})
			json.Unmarshal(raw, p)
			pc := NewPositionCache(nil, 0, zerolog.Nop())
			result := pc.matchPairs([]spotHolding{
				{exchange: "upbit", coin: tc.coin, total: 600 * tc.factor},
				{exchange: "bithumb", coin: tc.coin, total: 400 * tc.factor},
			}, []futuresPos{{exchange: "binance", coin: tc.futureCoin, position: p}})
			if len(result.Pairs) != 2 {
				t.Fatalf("expected split pair: %+v", result)
			}
			total := 0.0
			for _, pair := range result.Pairs {
				leg := pair.FuturesLeg
				expected := map[string]float64{"upbit": 600, "bithumb": 400}[pair.SpotLeg.Exchange]
				if leg.Size != expected || leg.MarkPrice != tc.mark || leg.PnL != expected/1000*50 || pair.SpotLeg.Size != expected*tc.factor {
					t.Fatalf("existing split changed: %+v", pair)
				}
				total += leg.Size * leg.MarkPrice
				data, _ := json.Marshal(leg)
				var observed map[string]interface{}
				json.Unmarshal(data, &observed)
				if observed["liquidationPrice"] != tc.liq || observed["priceScaleFactor"] != tc.factor {
					t.Errorf("display metadata missing or prorated: %s", data)
				}
			}
			if total != 1000*tc.mark {
				t.Fatalf("notional changed: %v", total)
			}
		})
	}
}

// PROS(Pharos): Binance/Bybit 선물 PHAROSUSDT 숏이 국내 PROS와 쌍을 이루고, 어댑터 조회 경로도 같은 PHAROS로 변환돼야 한다.
func TestMatchPairsPharosFutures(t *testing.T) {
	for _, tc := range []struct{ futEx, symbol, wantFutBase string }{
		{"binance", "PHAROSUSDT", "PHAROS"}, {"bybit", "PHAROSUSDT", "PHAROS"}, {"okx", "PROS-USDT-SWAP", "PROS"},
	} {
		t.Run(tc.futEx, func(t *testing.T) {
			p := &exchange.Position{Symbol: tc.symbol, Side: "short", Size: 100}
			result := NewPositionCache(nil, 0, zerolog.Nop()).matchPairs(
				[]spotHolding{{exchange: "upbit", coin: "PROS", total: 100, isDomestic: true}},
				[]futuresPos{{exchange: tc.futEx, coin: extractCoin(tc.symbol), position: p}})
			if len(result.Pairs) != 1 || len(result.Unmatched) != 0 || result.Pairs[0].PairID != "upbit_"+tc.futEx+"_PROS" {
				t.Fatalf("expected one pair upbit_%s_PROS, no unmatched: %+v", tc.futEx, result)
			}
			if base, factor := exchange.ResolveFuturesMapping(tc.futEx, result.Pairs[0].Coin); base != tc.wantFutBase || factor != 1 {
				t.Fatalf("adapter path resolves %s %s → (%s, %v), want (%s, 1)", tc.futEx, result.Pairs[0].Coin, base, factor, tc.wantFutBase)
			}
		})
	}
}
