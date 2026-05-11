package bithumb

import "strings"

// BithumbToStandard Bithumb 전용 티커 → 표준 심볼 매핑
var BithumbToStandard = map[string]string{
	"WAXL": "AXL",
}

// StandardToBithumb 표준 심볼 → Bithumb 전용 티커 (역방향, init에서 자동 생성)
var StandardToBithumb map[string]string

func init() {
	StandardToBithumb = make(map[string]string, len(BithumbToStandard))
	for bithumb, standard := range BithumbToStandard {
		StandardToBithumb[standard] = bithumb
	}
}

// NormalizeBithumbSymbol Bithumb 심볼을 표준 심볼로 변환 (WAXL → AXL)
func NormalizeBithumbSymbol(symbol string) string {
	if standard, ok := BithumbToStandard[strings.ToUpper(symbol)]; ok {
		return standard
	}
	return symbol
}

// ToBithumbSymbol 표준 심볼을 Bithumb 심볼로 변환 (AXL → WAXL)
func ToBithumbSymbol(symbol string) string {
	if bithumb, ok := StandardToBithumb[strings.ToUpper(symbol)]; ok {
		return bithumb
	}
	return symbol
}
