// Package notify provides notification services.
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// 비활성 사유. 비활성 상태의 전송은 nil 이 아니라 이 오류(들)를 wrap 해서 반환한다.
var (
	ErrMasterOff   = errors.New("텔레그램 마스터 스위치 OFF")
	ErrKeysMissing = errors.New("텔레그램 채널 미설정 (키 없음)")
)

// APIError 는 Telegram 이 200 이 아닌 응답을 줬을 때의 오류.
// RetryAfter 는 flood control 시 응답의 parameters.retry_after (초) — 없으면 0.
type APIError struct {
	StatusCode  int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("전송 실패: status %d (%s)", e.StatusCode, e.Description)
}

// TelegramNotifier sends notifications to Telegram.
type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
	logger   zerolog.Logger
	master   bool // 설정 마스터 스위치 (생성 시 고정, 이후 읽기 전용)
}

// NewTelegramNotifier creates a new Telegram notifier.
// 실효 상태 = masterEnabled AND botToken/chatID 모두 존재.
func NewTelegramNotifier(botToken, chatID string, masterEnabled bool, logger zerolog.Logger) *TelegramNotifier {
	t := &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		master:   masterEnabled,
		logger:   logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	if err := t.DisabledReason(); err != nil {
		t.logger.Warn().Err(err).Msg("[Telegram] 비활성 — 메시지 전송 안 함")
	} else {
		t.logger.Info().Msg("[Telegram] 활성")
	}

	return t
}

// DisabledReason 는 활성이면 nil, 비활성이면 ErrMasterOff 및/또는 ErrKeysMissing(없는 키 이름 포함)을 반환한다.
func (t *TelegramNotifier) DisabledReason() error {
	var missing []string
	if t.botToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if t.chatID == "" {
		missing = append(missing, "TELEGRAM_CHAT_ID")
	}
	var err error
	if len(missing) > 0 {
		err = fmt.Errorf("%w: %s", ErrKeysMissing, strings.Join(missing, ", "))
	}
	if !t.master {
		if err == nil {
			return ErrMasterOff
		}
		return fmt.Errorf("%w; %w", ErrMasterOff, err)
	}
	return err
}

// Send sends a plain text message synchronously.
func (t *TelegramNotifier) Send(message string) error {
	if err := t.DisabledReason(); err != nil {
		t.logger.Debug().Err(err).Msg("[Telegram] 비활성화 상태로 메시지 스킵")
		return err
	}

	body, err := json.Marshal(map[string]string{
		"chat_id": t.chatID,
		"text":    message,
	})
	if err != nil {
		return fmt.Errorf("JSON 마샬링 실패: %w", err)
	}

	resp, err := t.client.Post("https://api.telegram.org/bot"+t.botToken+"/sendMessage", "application/json", bytes.NewReader(body))
	if err != nil {
		err = sanitizeURLError(err)
		t.logger.Error().Err(err).Msg("[Telegram] 전송 오류")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		t.logger.Debug().Msg("[Telegram] 메시지 전송 성공")
		return nil
	}

	var r struct {
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r) // 본문이 JSON 이 아니어도 status 는 보고
	apiErr := &APIError{
		StatusCode:  resp.StatusCode,
		Description: r.Description,
		RetryAfter:  time.Duration(r.Parameters.RetryAfter) * time.Second,
	}
	t.logger.Error().Int("status", resp.StatusCode).Msg("[Telegram] 메시지 전송 실패")
	return apiErr
}

// sanitizeURLError 는 *url.Error 에서 URL(봇 토큰 포함)을 제거한다.
// Go 표준 http.Client 가 실패 시 url.Error 로 wrap 하므로 로그에 토큰이 노출되는 것을 차단.
func sanitizeURLError(err error) error {
	if uerr, ok := err.(*url.Error); ok {
		return fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
	}
	return err
}
