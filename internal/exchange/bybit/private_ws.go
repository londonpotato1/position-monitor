// Package bybit - V5 Private WebSocket (wallet + position 실시간 수신)
package bybit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/pkg/logger"

	"github.com/gorilla/websocket"
)

const (
	privateWSURL  = "wss://stream.bybit.com/v5/private"
	pingInterval  = 20 * time.Second
)

// PrivateWS Bybit V5 Private WebSocket
// wallet + position 이벤트 수신 시 onUpdate() 콜백 호출
type PrivateWS struct {
	client   *Client
	onUpdate func()
	running  atomic.Bool
}

// NewPrivateWS PrivateWS 생성
func NewPrivateWS(client *Client, onUpdate func()) *PrivateWS {
	return &PrivateWS{client: client, onUpdate: onUpdate}
}

// Start 비블로킹으로 스트림 루프 시작
func (s *PrivateWS) Start(ctx context.Context) {
	if s.running.Swap(true) {
		return // 이미 실행 중
	}
	go s.runLoop(ctx)
}

// runLoop 재연결 루프 (context 취소까지 반복)
func (s *PrivateWS) runLoop(ctx context.Context) {
	defer s.running.Store(false)
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.session(ctx); err != nil {
			logger.Warnf("[Bybit PrivateWS] session ended: %v — reconnect in %v", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
	}
}

// session 단일 WebSocket 세션: 인증 → 구독 → 읽기 루프
func (s *PrivateWS) session(ctx context.Context) error {
	// 1. WebSocket 연결 (10s handshake timeout)
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, privateWSURL, http.Header{})
	if err != nil {
		return fmt.Errorf("WebSocket 연결 실패: %w", err)
	}
	defer conn.Close()
	logger.Infof("[Bybit PrivateWS] 연결됨: %s", privateWSURL)

	// 2. 인증 메시지 전송
	expires := time.Now().UnixMilli() + 10000
	sig := signWS(s.client.apiSecret, expires)
	authMsg := map[string]interface{}{
		"op": "auth",
		"args": []interface{}{
			s.client.apiKey,
			expires,
			sig,
		},
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		return fmt.Errorf("auth 메시지 전송 실패: %w", err)
	}

	// 3. 인증 응답 확인
	var authResp struct {
		Op      string `json:"op"`
		Success bool   `json:"success"`
		RetMsg  string `json:"ret_msg"`
	}
	if err := conn.ReadJSON(&authResp); err != nil {
		return fmt.Errorf("auth 응답 읽기 실패: %w", err)
	}
	if authResp.Op != "auth" || !authResp.Success {
		return fmt.Errorf("auth 실패: %s", authResp.RetMsg)
	}
	logger.Debugf("[Bybit PrivateWS] 인증 성공")

	// 4. 구독 메시지 전송
	subMsg := map[string]interface{}{
		"op":   "subscribe",
		"args": []string{"wallet", "position"},
	}
	if err := conn.WriteJSON(subMsg); err != nil {
		return fmt.Errorf("subscribe 메시지 전송 실패: %w", err)
	}
	logger.Debugf("[Bybit PrivateWS] wallet, position 구독 완료")

	// 5. ping goroutine (20s 간격)
	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteJSON(map[string]string{"op": "ping"}); err != nil {
					logger.Warnf("[Bybit PrivateWS] ping 실패: %v", err)
					conn.Close()
					return
				}
			}
		}
	}()

	// 6. 메시지 읽기 루프
	type wsEvent struct {
		Topic string `json:"topic"`
		Op    string `json:"op"`
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		var event wsEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			logger.Debugf("[Bybit PrivateWS] JSON 파싱 실패: %v", err)
			continue
		}

		switch event.Topic {
		case "wallet", "position":
			logger.Debugf("[Bybit PrivateWS] %s 이벤트 수신 → 캐시 갱신 트리거", event.Topic)
			s.onUpdate()
		default:
			if event.Op == "pong" {
				logger.Debugf("[Bybit PrivateWS] pong 수신")
			} else if event.Op == "subscribe" {
				logger.Debugf("[Bybit PrivateWS] subscribe 응답: %s", string(msg))
			} else if event.Topic != "" {
				logger.Debugf("[Bybit PrivateWS] 무시된 토픽: %s", event.Topic)
			}
		}
	}
}

// signWS Bybit WS 인증 서명 생성
// 서명 대상: "GET/realtime" + expires
func signWS(apiSecret string, expires int64) string {
	msg := fmt.Sprintf("GET/realtime%d", expires)
	h := hmac.New(sha256.New, []byte(apiSecret))
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

// minDuration 두 Duration 중 작은 값 반환
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
