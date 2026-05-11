// Package config — plaintext .env loader.
package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotenv 프로젝트 루트의 .env 를 읽어 os.Setenv 로 등록.
// envPath 가 빈 문자열이면 "./.env" 사용. alias 매핑도 함께 적용.
// 파일이 없으면 nil 반환 (오류 아님 — 환경변수만 사용하는 경우 허용).
func LoadDotenv(envPath string) error {
	if envPath == "" {
		envPath = ".env"
	}
	f, err := os.Open(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // .env 없는 건 정상
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		// 따옴표 제거
		value = strings.Trim(value, `"'`)

		os.Setenv(key, value)
		applyEnvAliases(key, value)
	}
	return scanner.Err()
}
