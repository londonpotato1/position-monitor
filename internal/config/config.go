package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// SupportedExchanges 지원 거래소 목록
var SupportedExchanges = []string{
	"binance", "bybit", "okx", "gate", "bitget",
	"mexc", "kucoin", "htx", "hyperliquid", "lighter",
	"upbit", "bithumb",
}

// DomesticExchanges 국내 거래소 (현물만 지원)
var DomesticExchanges = []string{"upbit", "bithumb"}

// UpbitConfig 업비트 설정
type UpbitConfig struct {
	APIKey    string `mapstructure:"api_key" yaml:"api_key"`
	APISecret string `mapstructure:"api_secret" yaml:"api_secret"`
	Enabled   bool   `mapstructure:"enabled" yaml:"enabled"`
}

// BithumbConfig 빗썸 설정
type BithumbConfig struct {
	APIKey    string `mapstructure:"api_key" yaml:"api_key"`
	APISecret string `mapstructure:"api_secret" yaml:"api_secret"`
	Enabled   bool   `mapstructure:"enabled" yaml:"enabled"`
}

// ExchangeConfig 해외 거래소 설정
type ExchangeConfig struct {
	Name              string `mapstructure:"name" yaml:"name"`
	APIKey            string `mapstructure:"api_key" yaml:"api_key"`
	APISecret         string `mapstructure:"api_secret" yaml:"api_secret"`
	Passphrase        string `mapstructure:"passphrase" yaml:"passphrase"`
	Ed25519PrivateKey string `mapstructure:"ed25519_private_key" yaml:"ed25519_private_key"` // base64 Ed25519 private key seed
	WSAPIKey          string `mapstructure:"ws_api_key" yaml:"ws_api_key"`                   // WS 주문용 API key (Binance Ed25519 전용)
	Enabled           bool   `mapstructure:"enabled" yaml:"enabled"`
}

// PositionConfig 포지션 관리 설정
type PositionConfig struct {
	RefreshInterval    int     `mapstructure:"refresh_interval" yaml:"refresh_interval"`
	HideSmallThreshold float64 `mapstructure:"hide_small_threshold" yaml:"hide_small_threshold"`
}

// LoggingConfig 로깅 설정
type LoggingConfig struct {
	Level string `mapstructure:"level" yaml:"level"`
	File  string `mapstructure:"file" yaml:"file"`
}

// Config 전체 설정
type Config struct {
	// 국내 거래소 설정
	Upbit   UpbitConfig   `mapstructure:"upbit" yaml:"upbit"`
	Bithumb BithumbConfig `mapstructure:"bithumb" yaml:"bithumb"`

	// 해외 거래소 설정 (이름 -> ExchangeConfig)
	Exchanges map[string]ExchangeConfig `mapstructure:"exchanges" yaml:"exchanges"`

	// 포지션 관리 설정
	Position PositionConfig `mapstructure:"position" yaml:"position"`

	// 로깅 설정
	Logging LoggingConfig `mapstructure:"logging" yaml:"logging"`
}

// DefaultConfig 기본 설정 반환
func DefaultConfig() *Config {
	exchanges := make(map[string]ExchangeConfig)
	for _, name := range SupportedExchanges {
		if name == "upbit" || name == "bithumb" {
			continue
		}
		exchanges[name] = ExchangeConfig{Name: name}
	}

	return &Config{
		Exchanges: exchanges,
		Position: PositionConfig{
			RefreshInterval:    5,
			HideSmallThreshold: 1.0,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

// resolveConfigPath config.yaml 경로 탐색 (실행파일 기준 -> 현재 디렉토리 순)
func resolveConfigPath(path string) string {
	// 1) 절대경로거나 현재 디렉토리에 존재하면 그대로 사용
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	// 2) 실행 파일과 같은 디렉토리
	if exe, err := exec.LookPath(os.Args[0]); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// 3) .app 번들: Contents/MacOS/ -> Contents/Resources 또는 번들 외부 bin/
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		// .app/Contents/MacOS/ -> Contents/Resources/
		candidate := filepath.Join(exeDir, "..", "Resources", path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// .app/Contents/MacOS/ -> 번들 루트(../../) 옆 bin/
		candidate = filepath.Join(exeDir, "..", "..", "..", path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// 실행파일 위치 기준 상위 디렉토리
		candidate = filepath.Join(exeDir, "..", path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return path
}

// LoadConfig 설정 파일 로드 (YAML)
func LoadConfig(path string) (*Config, error) {
	// .env에서 API 키 로드 -> 환경변수로 설정
	if err := LoadDotenv(""); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: .env load failed: %v\n", err)
	}

	path = resolveConfigPath(path)
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.AutomaticEnv()

	// 환경변수 바인딩
	v.BindEnv("upbit.api_key", "UPBIT_API_KEY")
	v.BindEnv("upbit.api_secret", "UPBIT_API_SECRET")

	if err := v.ReadInConfig(); err != nil {
		// 파일이 없으면 기본값 사용
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("설정 파일 읽기 실패: %w", err)
	}

	cfg := DefaultConfig()
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("설정 파싱 실패: %w", err)
	}

	// 환경변수 우선 적용
	mergeEnvConfig(cfg)

	return cfg, nil
}

// mergeEnvConfig 환경변수에서 API 키 로드 (설정 파일보다 우선)
func mergeEnvConfig(cfg *Config) {
	// Upbit
	if key := os.Getenv("UPBIT_API_KEY"); key != "" {
		cfg.Upbit.APIKey = key
	}
	if key := os.Getenv("UPBIT_API_SECRET"); key != "" {
		cfg.Upbit.APISecret = key
	}
	if cfg.Upbit.APIKey != "" && cfg.Upbit.APISecret != "" {
		cfg.Upbit.Enabled = true
	}

	// Bithumb (환경변수에서 로드)
	if key := os.Getenv("BITHUMB_API_KEY"); key != "" {
		cfg.Bithumb.APIKey = key
	}
	if key := os.Getenv("BITHUMB_API_SECRET"); key != "" {
		cfg.Bithumb.APISecret = key
	}
	if cfg.Bithumb.APIKey != "" && cfg.Bithumb.APISecret != "" {
		cfg.Bithumb.Enabled = true
	}

	// 해외 거래소
	envMapping := map[string][3]string{
		"binance":     {"BINANCE_API_KEY", "BINANCE_API_SECRET", ""},
		"bybit":       {"BYBIT_API_KEY", "BYBIT_API_SECRET", ""},
		"okx":         {"OKX_API_KEY", "OKX_API_SECRET", "OKX_PASSPHRASE"},
		"gate":        {"GATE_API_KEY", "GATE_API_SECRET", ""},
		"bitget":      {"BITGET_API_KEY", "BITGET_API_SECRET", "BITGET_PASSPHRASE"},
		"mexc":        {"MEXC_API_KEY", "MEXC_API_SECRET", ""},
		"kucoin":      {"KUCOIN_API_KEY", "KUCOIN_API_SECRET", "KUCOIN_PASSPHRASE"},
		"htx":         {"HTX_API_KEY", "HTX_API_SECRET", ""},
		"hyperliquid": {"HYPERLIQUID_WALLET_ADDRESS", "HYPERLIQUID_PRIVATE_KEY", ""},
		"lighter":     {"LIGHTER_ACCOUNT_INDEX", "LIGHTER_PRIVATE_KEY", "LIGHTER_API_KEY_INDEX"},
	}

	for name, envKeys := range envMapping {
		apiKey := os.Getenv(envKeys[0])
		apiSecret := os.Getenv(envKeys[1])
		passphrase := ""
		if envKeys[2] != "" {
			passphrase = os.Getenv(envKeys[2])
		}

		if apiKey != "" && apiSecret != "" {
			ec := cfg.Exchanges[name]
			if ec.APIKey == "" {
				ec.Name = name
				ec.APIKey = apiKey
				ec.APISecret = apiSecret
				ec.Passphrase = passphrase
				ec.Enabled = true
				cfg.Exchanges[name] = ec
			}
		}
	}
}

// GetEnabledExchanges 활성화된 거래소 이름 목록 반환
func (c *Config) GetEnabledExchanges() []string {
	var enabled []string
	for name, ex := range c.Exchanges {
		if ex.Enabled && ex.APIKey != "" {
			enabled = append(enabled, name)
		}
	}
	if c.Upbit.Enabled && c.Upbit.APIKey != "" {
		enabled = append(enabled, "upbit")
	}
	if c.Bithumb.Enabled && c.Bithumb.APIKey != "" {
		enabled = append(enabled, "bithumb")
	}
	return enabled
}

// IsAPIConfigured 특정 거래소 API 설정 여부
func (c *Config) IsAPIConfigured(exchange string) bool {
	if exchange == "upbit" {
		return c.Upbit.APIKey != "" && c.Upbit.APISecret != ""
	}
	if exchange == "bithumb" {
		return c.Bithumb.APIKey != "" && c.Bithumb.APISecret != ""
	}
	if ec, ok := c.Exchanges[exchange]; ok {
		return ec.APIKey != "" && ec.APISecret != ""
	}
	return false
}

// GetAPIStatus 모든 거래소 API 설정 상태 반환
func (c *Config) GetAPIStatus() map[string]bool {
	status := make(map[string]bool)
	for _, name := range SupportedExchanges {
		status[name] = c.IsAPIConfigured(name)
	}
	return status
}

// SaveConfig config.yaml에 현재 설정 저장.
// 보안: API 키/시크릿/토큰 등 민감 정보는 제거 후 저장.
//
// #S2 fix: atomic write (tmp + fsync + rename) — 중간 panic/SIGKILL 시 빈 파일 잔존 방지.
// 부모 디렉토리 0700 + 절대경로 + YAML encoder Close() 누락 보정.
func SaveConfig(path string, cfg *Config) error {
	path = resolveConfigPath(path)

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("설정 경로 절대화 실패: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0700); err != nil {
		return fmt.Errorf("설정 디렉토리 생성 실패: %w", err)
	}

	// 민감 필드를 제거한 사본 생성
	safeCfg := *cfg
	safeCfg.Upbit.APIKey = ""
	safeCfg.Upbit.APISecret = ""
	safeCfg.Bithumb.APIKey = ""
	safeCfg.Bithumb.APISecret = ""

	safeExchanges := make(map[string]ExchangeConfig, len(cfg.Exchanges))
	for name, ec := range cfg.Exchanges {
		safeExchanges[name] = ExchangeConfig{
			Name:    ec.Name,
			Enabled: ec.Enabled,
		}
	}
	safeCfg.Exchanges = safeExchanges

	tmp := absPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("설정 임시 파일 생성 실패: %w", err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmp)
		}
	}()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(&safeCfg); err != nil {
		f.Close()
		return fmt.Errorf("설정 YAML 인코딩 실패: %w", err)
	}
	if err := enc.Close(); err != nil {
		f.Close()
		return fmt.Errorf("YAML encoder close 실패: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("설정 파일 fsync 실패: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("설정 파일 close 실패: %w", err)
	}

	if err := os.Rename(tmp, absPath); err != nil {
		return fmt.Errorf("설정 파일 atomic rename 실패: %w", err)
	}
	cleanup = false
	return nil
}
