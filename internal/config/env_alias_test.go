package config

import (
	"os"
	"testing"
)

func TestApplyEnvAliases(t *testing.T) {
	tests := []struct {
		key    string
		value  string
		expect map[string]string // 기대되는 alias env 들
	}{
		{"BINANCE_SECRET", "binsec123", map[string]string{"BINANCE_API_SECRET": "binsec123"}},
		{"BYBIT_SECRET", "bbsec", map[string]string{"BYBIT_API_SECRET": "bbsec"}},
		{"OKX_SECRET", "okxsec", map[string]string{"OKX_API_SECRET": "okxsec"}},
		{"HTX_SECRET", "htxsec", map[string]string{"HTX_API_SECRET": "htxsec"}},
		{"UPBIT_SECRET", "upbsec", map[string]string{"UPBIT_API_SECRET": "upbsec"}},
		{"BITHUMB_SECRET", "bhsec", map[string]string{"BITHUMB_API_SECRET": "bhsec"}},
		{"GATE_API_SECRET", "gatesec", map[string]string{"GATE_SECRET": "gatesec"}},
		{"HYPERLIQUID_API_KEY", "hlkey", map[string]string{"HYPERLIQUID_WALLET_ADDRESS": "hlkey"}},
		{"HYPERLIQUID_SECRET", "hlsec", map[string]string{"HYPERLIQUID_PRIVATE_KEY": "hlsec"}},
		{"LIGHTER_SECRET", "ltsec", map[string]string{"LIGHTER_PRIVATE_KEY": "ltsec"}},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			// 테스트 격리 — 기대 alias 들 사전 초기화
			for alias := range tt.expect {
				os.Unsetenv(alias)
			}
			applyEnvAliases(tt.key, tt.value)
			for alias, want := range tt.expect {
				if got := os.Getenv(alias); got != want {
					t.Errorf("alias %s = %q, want %q", alias, got, want)
				}
			}
		})
	}
}
