// Package okx - OKX V5 Private WebSocket (account + positions 실시간 수신)
package okx

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/pkg/logger"

	"github.com/gorilla/websocket"
)

const (
	privateWSURL    = "wss://ws.okx.com:8443/ws/v5/private"
	pingInterval    = 25 * time.Second
	pongDeadline    = 30 * time.Second
)

// PrivateWS OKX V5 Private WebSocket 스트림
// account + positions(SWAP) 이벤트 수신 시 onUpdate() 콜백 호출
type PrivateWS struct {
	client   *Client
	onUpdate func()
	running  atomic.Bool
}

// NewPrivateWS PrivateWS 생성
func NewPrivateWS(client *Client, onUpdate func()) *PrivateWS {
	return &PrivateWS{client: client, onUpdate: onUpdate}
}

// Start 비블로킹으로 스트림 루프 시작 (중복 실행 방지)
func (s *PrivateWS) Start(ctx context.Context) {
	if s.running.Swap(true) {
		return // 이미 실행 중
	}
	go s.runLoop(ctx)
}

// runLoop 재연결 루프 (context 취소까지 반복, 백오프 1s → 30s)
func (s *PrivateWS) runLoop(ctx context.Context) {
	defer s.running.Store(false)
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.session(ctx); err != nil {
			logger.Warnf("[OKX PrivateWS] session ended: %v — reconnect in %v", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			continue
		}
		backoff = time.Second
	}
}

// wsLoginArgs OKX WS 로그인 args 구조체
type wsLoginArgs struct {
	APIKey     string `json:"apiKey"`
	Passphrase string `json:"passphrase"`
	Timestamp  string `json:"timestamp"`
	Sign       string `json:"sign"`
}

// wsOp OKX WS 공통 op 메시지
type wsOp struct {
	Op   string      `json:"op"`
	Args interface{} `json:"args"`
}

// wsEvent OKX WS 이벤트 응답 (login/subscribe/error)
type wsEvent struct {
	Event  string `json:"event"`
	Code   string `json:"code"`
	Msg    string `json:"msg"`
	ConnID string `json:"connId"`
	Arg    struct {
		Channel  string `json:"channel"`
		InstType string `json:"instType"`
	} `json:"arg"`
}

// wsDataMsg OKX WS 데이터 메시지 (account/positions push)
type wsDataMsg struct {
	Arg struct {
		Channel string `json:"channel"`
	} `json:"arg"`
	Data json.RawMessage `json:"data"`
}

// signWSLogin OKX WS 로그인 서명 생성
// sign = BASE64(HMAC_SHA256(apiSecret, timestamp + "GET" + "/users/self/verify"))
func signWSLogin(apiSecret, timestamp string) string {
	msg := timestamp + "GET" + "/users/self/verify"
	h := hmac.New(sha256.New, []byte(apiSecret))
	h.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// session 단일 WebSocket 세션
// 1. 연결 → 2. 로그인 → 3. 구독 → 4. ping goroutine → 5. 읽기 루프
func (s *PrivateWS) session(ctx context.Context) error {
	// 1. Dial
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, privateWSURL, http.Header{})
	if err != nil {
		return fmt.Errorf("dial 실패: %w", err)
	}
	defer conn.Close()
	logger.Infof("[OKX PrivateWS] 연결됨: %s", privateWSURL)

	// 2. 로그인
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sign := signWSLogin(s.client.apiSecret, ts)
	loginMsg := wsOp{
		Op: "login",
		Args: []wsLoginArgs{{
			APIKey:     s.client.apiKey,
			Passphrase: s.client.passphrase,
			Timestamp:  ts,
			Sign:       sign,
		}},
	}
	loginBytes, _ := json.Marshal(loginMsg)
	if err := conn.WriteMessage(websocket.TextMessage, loginBytes); err != nil {
		return fmt.Errorf("login 메시지 전송 실패: %w", err)
	}

	// 로그인 응답 대기 (최대 10초)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, loginResp, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("login 응답 읽기 실패: %w", err)
	}
	conn.SetReadDeadline(time.Time{}) // 리셋

	var loginEvent wsEvent
	if err := json.Unmarshal(loginResp, &loginEvent); err != nil {
		return fmt.Errorf("login 응답 파싱 실패: %w", err)
	}
	if loginEvent.Event != "login" || loginEvent.Code != "0" {
		return fmt.Errorf("login 실패: event=%s code=%s msg=%s", loginEvent.Event, loginEvent.Code, loginEvent.Msg)
	}
	logger.Infof("[OKX PrivateWS] 로그인 성공 (connId=%s)", loginEvent.ConnID)

	// 3. 구독 (account + positions SWAP)
	subMsg := wsOp{
		Op: "subscribe",
		Args: []map[string]string{
			{"channel": "account"},
			{"channel": "positions", "instType": "SWAP"},
		},
	}
	subBytes, _ := json.Marshal(subMsg)
	if err := conn.WriteMessage(websocket.TextMessage, subBytes); err != nil {
		return fmt.Errorf("subscribe 메시지 전송 실패: %w", err)
	}

	// 4. ping goroutine: 25초마다 raw text "ping" 전송
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
				if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
					logger.Warnf("[OKX PrivateWS] ping 전송 실패: %v", err)
					return
				}
				// pong 수신 deadline 설정
				conn.SetReadDeadline(time.Now().Add(pongDeadline))
			}
		}
	}()

	// 5. 읽기 루프
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}
		// pong deadline 리셋
		conn.SetReadDeadline(time.Time{})

		// raw "pong" 텍스트 처리
		if string(raw) == "pong" {
			logger.Debugf("[OKX PrivateWS] pong 수신")
			continue
		}

		// JSON 파싱 시도
		// 먼저 event 필드로 subscribe/error 확인
		var ev wsEvent
		if err := json.Unmarshal(raw, &ev); err == nil && ev.Event != "" {
			switch ev.Event {
			case "subscribe":
				logger.Debugf("[OKX PrivateWS] 구독 확인: channel=%s instType=%s", ev.Arg.Channel, ev.Arg.InstType)
			case "error":
				logger.Warnf("[OKX PrivateWS] 서버 에러: code=%s msg=%s", ev.Code, ev.Msg)
			default:
				logger.Debugf("[OKX PrivateWS] 이벤트: %s", ev.Event)
			}
			continue
		}

		// 데이터 메시지 (account/positions push)
		var dm wsDataMsg
		if err := json.Unmarshal(raw, &dm); err != nil {
			logger.Debugf("[OKX PrivateWS] JSON 파싱 실패 (무시): %v", err)
			continue
		}

		switch dm.Arg.Channel {
		case "account", "positions":
			logger.Debugf("[OKX PrivateWS] %s 업데이트 수신 → 캐시 갱신 트리거", dm.Arg.Channel)
			s.onUpdate()
		default:
			logger.Debugf("[OKX PrivateWS] 알 수 없는 채널: %s", dm.Arg.Channel)
		}
	}
}
