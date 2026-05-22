package exchange

import "strings"

// FuturesMapping 현물→선물 심볼 매핑 정보
type FuturesMapping struct {
	FuturesBase string  // 선물 base 심볼
	ScaleFactor float64 // 1.0 = 동일가격, 1000.0 = 1000x 토큰
}

// FuturesRegistry 거래소별 선물 심볼 매핑 (spotBase → FuturesMapping)
var FuturesRegistry = map[string]map[string]FuturesMapping{
	"binance": {
		// 이름 변경
		"RAY":      {FuturesBase: "RAYSOL", ScaleFactor: 1},
		"RON":      {FuturesBase: "RONIN", ScaleFactor: 1},
		"LUNA":     {FuturesBase: "LUNA2", ScaleFactor: 1},
		"BEAM":     {FuturesBase: "BEAMX", ScaleFactor: 1},
		"BROCCOLI": {FuturesBase: "BROCCOLI714", ScaleFactor: 1},
		// 1000x 토큰
		"SHIB":   {FuturesBase: "1000SHIB", ScaleFactor: 1000},
		"PEPE":   {FuturesBase: "1000PEPE", ScaleFactor: 1000},
		"FLOKI":  {FuturesBase: "1000FLOKI", ScaleFactor: 1000},
		"BONK":   {FuturesBase: "1000BONK", ScaleFactor: 1000},
		"LUNC":   {FuturesBase: "1000LUNC", ScaleFactor: 1000},
		"SATS":   {FuturesBase: "1000SATS", ScaleFactor: 1000},
		"CAT":    {FuturesBase: "1000CAT", ScaleFactor: 1000},
		"RATS":   {FuturesBase: "1000RATS", ScaleFactor: 1000},
		"CHEEMS": {FuturesBase: "1000CHEEMS", ScaleFactor: 1000},
	},
	"bybit": {
		// 이름 변경
		"RAY":   {FuturesBase: "RAYDIUM", ScaleFactor: 1},
		"RON":   {FuturesBase: "RONIN", ScaleFactor: 1},
		"LUNA":  {FuturesBase: "LUNA2", ScaleFactor: 1},
		"TST":   {FuturesBase: "TSTBSC", ScaleFactor: 1},
		"PUMP":  {FuturesBase: "PUMPFUN", ScaleFactor: 1},
		"NEIRO": {FuturesBase: "NEIROCTO", ScaleFactor: 1},
		"AI":    {FuturesBase: "AIGENSYN", ScaleFactor: 1}, // OKX 현물 AI(Gensyn) ↔ Bybit 선물 AIGENSYN
		// 1000x 토큰
		"SHIB":  {FuturesBase: "SHIB1000", ScaleFactor: 1000},
		"PEPE":  {FuturesBase: "1000PEPE", ScaleFactor: 1000},
		"FLOKI": {FuturesBase: "1000FLOKI", ScaleFactor: 1000},
		"BONK":  {FuturesBase: "1000BONK", ScaleFactor: 1000},
		"LUNC":  {FuturesBase: "1000LUNC", ScaleFactor: 1000},
		"SATS":  {FuturesBase: "1000SATS", ScaleFactor: 1000},
		"CAT":   {FuturesBase: "1000CAT", ScaleFactor: 1000},
		"RATS":  {FuturesBase: "1000RATS", ScaleFactor: 1000},
		// 1000000x 토큰
		"CHEEMS": {FuturesBase: "1000000CHEEMS", ScaleFactor: 1000000},
	},
}

// reverseRegistry 역방향 매핑 캐시: map[exchange]map[futuresBase]{spotBase, factor}
// init()에서 FuturesRegistry로부터 자동 생성
var reverseRegistry map[string]map[string]FuturesMapping

func init() {
	reverseRegistry = make(map[string]map[string]FuturesMapping)
	for exName, spotMap := range FuturesRegistry {
		reverseRegistry[exName] = make(map[string]FuturesMapping)
		for spotBase, mapping := range spotMap {
			reverseRegistry[exName][strings.ToUpper(mapping.FuturesBase)] = FuturesMapping{
				FuturesBase: spotBase, // 역방향이므로 spotBase를 저장
				ScaleFactor: mapping.ScaleFactor,
			}
		}
	}
}

// ResolveFuturesSymbol 현물 base를 거래소별 선물 base로 변환 (기존 시그니처 유지, string만 반환)
func ResolveFuturesSymbol(exchangeName, base string) string {
	futBase, _ := ResolveFuturesMapping(exchangeName, base)
	return futBase
}

// ResolveFuturesMapping 현물 base → (선물 base, scaleFactor) 반환
func ResolveFuturesMapping(exchangeName, base string) (futuresBase string, factor float64) {
	exKey := strings.ToLower(exchangeName)
	baseKey := strings.ToUpper(base)
	if exMap, ok := FuturesRegistry[exKey]; ok {
		if mapping, ok := exMap[baseKey]; ok {
			return mapping.FuturesBase, mapping.ScaleFactor
		}
	}
	return base, 1.0
}

// ResolveSpotSymbol 선물 base → (현물 base, scaleFactor) 역방향 조회
// 모든 거래소를 검색하여 첫 번째 매칭 반환 (거래소 지정 불필요)
func ResolveSpotSymbol(futuresBase string) (spotBase string, factor float64) {
	key := strings.ToUpper(futuresBase)
	for _, revMap := range reverseRegistry {
		if mapping, ok := revMap[key]; ok {
			return mapping.FuturesBase, mapping.ScaleFactor
		}
	}
	return futuresBase, 1.0
}

// ResolveSpotSymbolForExchange 특정 거래소의 선물 base → (현물 base, scaleFactor)
func ResolveSpotSymbolForExchange(exchangeName, futuresBase string) (spotBase string, factor float64) {
	exKey := strings.ToLower(exchangeName)
	key := strings.ToUpper(futuresBase)
	if revMap, ok := reverseRegistry[exKey]; ok {
		if mapping, ok := revMap[key]; ok {
			return mapping.FuturesBase, mapping.ScaleFactor
		}
	}
	return futuresBase, 1.0
}

// GetScaleFactor 현물 base의 scaleFactor만 조회
func GetScaleFactor(exchangeName, base string) float64 {
	_, factor := ResolveFuturesMapping(exchangeName, base)
	return factor
}
