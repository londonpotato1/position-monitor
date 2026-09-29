package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig 임시 config.yaml 생성, 테스트 종료 시 자동 삭제.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("임시 config 파일 생성 실패: %v", err)
	}
	return path
}

// 텔레그램 토큰/채팅 ID 는 TELEGRAM_BOT_TOKEN / TELEGRAM_CHAT_ID 환경변수에서 읽는다.
func TestLoadConfig_TelegramReadsEnvKeys(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:TEST")
	t.Setenv("TELEGRAM_CHAT_ID", "0")
	path := writeTempConfig(t, "telegram:\n  enabled: true\n")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Telegram.Enabled || cfg.Telegram.BotToken != "123:TEST" || cfg.Telegram.ChatID != "0" {
		t.Fatalf("텔레그램 설정 로드 실패: %+v", cfg.Telegram)
	}
}

// telegram 섹션이 없으면 기본 비활성.
func TestLoadConfig_TelegramDisabledByDefault(t *testing.T) {
	if DefaultConfig().Telegram.Enabled {
		t.Fatal("DefaultConfig 텔레그램이 활성")
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:TEST")
	t.Setenv("TELEGRAM_CHAT_ID", "0")
	path := writeTempConfig(t, "logging:\n  level: info\n")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Telegram.Enabled {
		t.Fatal("telegram 섹션 없는데 활성")
	}
}

// SaveConfig 는 텔레그램 토큰/채팅 ID 를 파일에 쓰지 않는다.
func TestSaveConfig_StripsTelegramSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := DefaultConfig()
	cfg.Telegram = TelegramConfig{Enabled: true, BotToken: "123:TEST", ChatID: "-100999"}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if s := string(data); strings.Contains(s, "123:TEST") || strings.Contains(s, "-100999") {
		t.Fatalf("텔레그램 비밀값이 파일에 저장됨:\n%s", s)
	}
	if cfg.Telegram.BotToken != "123:TEST" {
		t.Fatal("SaveConfig 가 호출자 설정을 변경함")
	}
}
