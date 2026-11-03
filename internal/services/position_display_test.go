package services

import (
	"encoding/json"
	"fmt"
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

// TradFi(주식·ETF 등) 선물은 같은 티커의 암호화폐와 짝지으면 가짜 헷지가 된다 — 이름만 같아서는 매칭하지 않는다.
func TestMatchPairsTradFi(t *testing.T) {
	fut := func(ex, sym, side string, size float64, tradFi bool) futuresPos {
		return futuresPos{exchange: ex, coin: extractCoin(sym), position: &exchange.Position{Symbol: sym, Side: side, Size: size, TradFi: tradFi}}
	}
	for _, tc := range []struct {
		name          string
		spots         []spotHolding
		futures       []futuresPos
		wantPairs     []string
		wantLegTradFi bool            // 쌍 안의 선물 다리 TradFi 기대값
		wantFactor    float64         // 현물-선물 쌍의 1000x 보정 계수
		wantUnmatched map[string]bool // "exchange/symbol" → TradFi
	}{
		{name: "Meta 주식 숏은 Metadium 현물과 짝짓지 않음",
			spots:         []spotHolding{{exchange: "upbit", coin: "META", total: 100, isDomestic: true}},
			futures:       []futuresPos{fut("binance", "METAUSDT", "short", 100, true)},
			wantUnmatched: map[string]bool{"upbit/META": false, "binance/METAUSDT": true}},
		{name: "CAT 현물은 1000CAT 과 짝짓고 Caterpillar 주식은 미매칭",
			spots: []spotHolding{{exchange: "bybit", coin: "CAT", total: 1_000_000}},
			futures: []futuresPos{
				fut("binance", "1000CATUSDT", "short", 1000, false),
				fut("binance", "CATUSDT", "short", 2000, true), // 더 커도 best-fit 후보가 아니어야 함
			},
			wantPairs: []string{"bybit_binance_CAT"}, wantFactor: 1000,
			wantUnmatched: map[string]bool{"binance/CATUSDT": true}},
		{name: "TradFi 끼리 같은 종목 선선 헷지는 허용",
			futures: []futuresPos{
				fut("binance", "METAUSDT", "long", 10, true),
				fut("bybit", "METAUSDT", "short", 10, true),
			},
			wantPairs: []string{"binance_bybit_META_ff"}, wantLegTradFi: true,
			wantUnmatched: map[string]bool{}},
		{name: "Backpack 롱 + BP plc 숏은 짝짓지 않음",
			futures: []futuresPos{
				fut("gate", "BP_USDT", "long", 10, false),
				fut("bitget", "BPUSDT", "short", 10, true),
			},
			wantUnmatched: map[string]bool{"gate/BP_USDT": false, "bitget/BPUSDT": true}},
		{name: "레지스트리가 다른 계약으로 바꾸는 TradFi(바이낸스 CAT→1000CAT)는 TradFi 끼리도 짝짓지 않음",
			futures: []futuresPos{
				fut("binance", "CATUSDT", "long", 10, true),
				fut("gate", "CAT_USDT", "short", 10, true),
			},
			wantUnmatched: map[string]bool{"binance/CATUSDT": true, "gate/CAT_USDT": true}},
		{name: "레지스트리가 바꾸는 TradFi 가 숏 쪽이어도 짝짓지 않음",
			futures: []futuresPos{
				fut("gate", "CAT_USDT", "long", 10, true),
				fut("binance", "CATUSDT", "short", 10, true),
			},
			wantUnmatched: map[string]bool{"gate/CAT_USDT": true, "binance/CATUSDT": true}},
		{name: "TradFi 는 레지스트리 역변환을 하지 않음 (PHAROS 가 PROS 로 바뀌지 않음)",
			futures: []futuresPos{
				fut("okx", "PHAROS-USDT-SWAP", "long", 10, true),
				fut("gate", "PHAROS_USDT", "short", 10, true),
			},
			wantPairs: []string{"okx_gate_PHAROS_ff"}, wantLegTradFi: true,
			wantUnmatched: map[string]bool{}},
		{name: "금 토큰 PAXG 는 거래소가 원자재로 분류해도 암호화폐처럼 매칭",
			spots:     []spotHolding{{exchange: "bithumb", coin: "PAXG", total: 1, isDomestic: true}},
			futures:   []futuresPos{fut("gate", "PAXG_USDT", "short", 1, true)},
			wantPairs: []string{"bithumb_gate_PAXG"}, wantFactor: 1,
			wantUnmatched: map[string]bool{}},
		{name: "게이트가 외환으로 분류한 USDC 선물도 암호화폐처럼 선선 매칭",
			futures: []futuresPos{
				fut("gate", "USDC_USDT", "long", 100, true),
				fut("okx", "USDC-USDT-SWAP", "short", 100, false),
			},
			wantPairs:     []string{"gate_okx_USDC_ff"},
			wantUnmatched: map[string]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := NewPositionCache(nil, 0, zerolog.Nop()).matchPairs(tc.spots, tc.futures)
			var ids []string
			for _, p := range result.Pairs {
				ids = append(ids, p.PairID)
				if p.PairType == "spot_futures" {
					if p.SpotLeg.TradFi || p.FuturesLeg.TradFi != tc.wantLegTradFi {
						t.Errorf("%s legs TradFi spot=%v futures=%v, want false/%v", p.PairID, p.SpotLeg.TradFi, p.FuturesLeg.TradFi, tc.wantLegTradFi)
					}
					if p.FuturesLeg.PriceScaleFactor != tc.wantFactor || p.SpotLeg.Size != p.MatchedSize*tc.wantFactor {
						t.Errorf("%s factor=%v spot=%v matched=%v, want factor %v", p.PairID, p.FuturesLeg.PriceScaleFactor, p.SpotLeg.Size, p.MatchedSize, tc.wantFactor)
					}
				} else if p.LongLeg.TradFi != tc.wantLegTradFi || p.ShortLeg.TradFi != tc.wantLegTradFi {
					t.Errorf("%s legs TradFi long=%v short=%v, want %v", p.PairID, p.LongLeg.TradFi, p.ShortLeg.TradFi, tc.wantLegTradFi)
				}
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.wantPairs) {
				t.Fatalf("pairs %v, want %v", ids, tc.wantPairs)
			}
			got := map[string]bool{}
			for _, leg := range result.Unmatched {
				got[leg.Exchange+"/"+leg.Symbol] = leg.TradFi
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.wantUnmatched) {
				t.Fatalf("unmatched %v, want %v", got, tc.wantUnmatched)
			}
		})
	}
}
