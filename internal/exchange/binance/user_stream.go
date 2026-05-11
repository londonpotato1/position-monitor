// Package binance - Futures User Data Stream (ACCOUNT_UPDATE 실시간 수신)
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/pkg/logger"

	"github.com/gorilla/websocket"
)

const (
	futuresStreamBase = "wss://fstream.binance.com/ws/"
	keepaliveInterval = 30 * time.Minute
)

// UserStream Binance Futures User Data Stream
// ACCOUNT_UPDATE 이벤트 수신 시 onUpdate() 콜백 호출
type UserStream struct {
	client   *Client
	onUpdate func()
	running  atomic.Bool
}

// NewUserStream UserStream 생성
func NewUserStream(client *Client, onUpdate func()) *UserStream {
	return &UserStream{client: client, onUpdate: onUpdate}
}

// Start 비블로킹으로 스트림 루프 시작
func (s *UserStream) Start(ctx context.Context) {
	if s.running.Swap(true) {
		return // 이미 실행 중
	}
	go s.runLoop(ctx)
}

// runLoop 재연결 루프 (context 취소까지 반복)
func (s *UserStream) runLoop(ctx context.Context) {
	defer s.running.Store(false)
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.session(ctx); err != nil {
			logger.Warnf("[Binance UserStream] session ended: %v — reconnect in %v", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
	}
}

// session 단일 WebSocket 세션: listenKey 발급 → 연결 → keepalive → 읽기 루프
func (s *UserStream) session(ctx context.Context) error {
	// 1. listenKey 발급
	listenKey, err := s.client.StartUserDataStream(ctx)
	if err != nil {
		return fmt.Errorf("listenKey 발급 실패: %w", err)
	}
	logger.Debugf("[Binance UserStream] listenKey 발급 완료")

	// 2. WebSocket 연결
	wsURL := futuresStreamBase + listenKey
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("WebSocket 연결 실패: %w", err)
	}
	defer conn.Close()
	logger.Infof("[Binance UserStream] 연결됨: %s", wsURL)

	// 3. keepalive goroutine (30분마다 PUT /fapi/v1/listenKey)
	keepCtx, keepCancel := context.WithCancel(ctx)
	defer keepCancel()
	go func() {
		ticker := time.NewTicker(keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-keepCtx.Done():
				return
			case <-ticker.C:
				if err := s.client.KeepAliveUserDataStream(keepCtx); err != nil {
					logger.Warnf("[Binance UserStream] keepalive 실패: %v", err)
					// keepalive 실패 → 연결 종료 유도 (재연결 루프가 새 listenKey 발급)
					conn.Close()
					return
				}
				logger.Debugf("[Binance UserStream] keepalive OK")
			}
		}
	}()

	// 4. 메시지 읽기 루프
	type accountUpdateEvent struct {
		EventType string `json:"e"`
	}

	for {
		// context 취소 확인
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		var event accountUpdateEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			logger.Debugf("[Binance UserStream] JSON 파싱 실패: %v", err)
			continue
		}

		switch event.EventType {
		case "ACCOUNT_UPDATE":
			logger.Debugf("[Binance UserStream] ACCOUNT_UPDATE 수신 → 캐시 갱신 트리거")
			s.onUpdate()
		case "ORDER_TRADE_UPDATE", "MARGIN_CALL", "ACCOUNT_CONFIG_UPDATE",
			"TRADE_LITE", "listenKeyExpired":
			if event.EventType == "listenKeyExpired" {
				// listenKey 만료 → 즉시 재연결
				return fmt.Errorf("listenKey 만료 — 재연결 필요")
			}
			logger.Debugf("[Binance UserStream] 이벤트 무시: %s", event.EventType)
		default:
			logger.Debugf("[Binance UserStream] 알 수 없는 이벤트: %s", event.EventType)
		}
	}
}

// min returns the smaller of two durations (Go 1.21+ has built-in min, but keep compatible)
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
