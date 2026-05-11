// Package config — env alias mapping (separated from decryption).
// 사용자가 .env 에 한 키 이름으로 적어도, 같은 의미의 다른 변수명도 자동 채워준다.
// (호환성 유지)
package config

import "os"

// envAliases maps .env primary variable names to additional aliases.
// 사용자가 BINANCE_SECRET 만 적어도 BINANCE_API_SECRET 도 자동 set.
var envAliases = map[string][]string{
	"BINANCE_SECRET":      {"BINANCE_API_SECRET"},
	"BYBIT_SECRET":        {"BYBIT_API_SECRET"},
	"OKX_SECRET":          {"OKX_API_SECRET"},
	"HTX_SECRET":          {"HTX_API_SECRET"},
	"UPBIT_SECRET":        {"UPBIT_API_SECRET"},
	"BITHUMB_SECRET":      {"BITHUMB_API_SECRET"},
	"GATE_API_SECRET":     {"GATE_SECRET"},
	"HYPERLIQUID_API_KEY": {"HYPERLIQUID_WALLET_ADDRESS"},
	"HYPERLIQUID_SECRET":  {"HYPERLIQUID_PRIVATE_KEY"},
	"LIGHTER_SECRET":      {"LIGHTER_PRIVATE_KEY"},
}

// applyEnvAliases sets alias environment variables for a given key/value.
// LoadDotenv 가 .env 한 줄 파싱할 때마다 호출.
func applyEnvAliases(key, value string) {
	if aliases, ok := envAliases[key]; ok {
		for _, alias := range aliases {
			os.Setenv(alias, value)
		}
	}
}
