// Package upbit provides a private WebSocket asset stream for real-time balance events.
// 업비트 프라이빗 WS - 실시간 잔고 이벤트 스트림
package upbit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/londonpotato1/position-monitor/internal/exchange"
	"github.com/londonpotato1/position-monitor/pkg/logger"

	"github.com/gorilla/websocket"
)

const (
	wsPrivateEndpoint = "wss://api.upbit.com/websocket/v1/private"
	wsHandshakeTimeout = 10 * time.Second
)

// AssetStream 업비트 프라이빗 잔고 WS 스트림
type AssetStream struct {
	client   *Client
	onUpdate func(map[string]*exchange.Balance)
	running  atomic.Bool
}

// NewAssetStream 새 AssetStream 생성
func NewAssetStream(client *Client, onUpdate func(map[string]*exchange.Balance)) *AssetStream {
	return &AssetStream{client: client, onUpdate: onUpdate}
}

// Start 블로킹 없이 고루틴에서 연결/재연결 루프 시작
func (s *AssetStream) Start(ctx context.Context) {
	go s.runLoop(ctx)
}

func (s *AssetStream) runLoop(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := s.connectAndServe(ctx)
		// 최대 백오프(30초)보다 오래 유지된 세션 = 정상 연결이었음 → 백오프 초기화.
		// 즉시 실패(인증 거부 등)는 초기화하지 않아 재시도 폭주를 막는다.
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		if err != nil {
			logger.Warnf("[Upbit AssetStream] disconnected: %v — reconnect in %v", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (s *AssetStream) connectAndServe(ctx context.Context) error {
	// JWT 생성 (쿼리 파라미터 없음 → 빈 해시)
	token, err := s.client.generateToken("")
	if err != nil {
		return fmt.Errorf("JWT 생성 실패: %w", err)
	}

	// Authorization 헤더 설정
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	dialer := websocket.Dialer{
		HandshakeTimeout: wsHandshakeTimeout,
	}

	conn, _, err := dialer.DialContext(ctx, wsPrivateEndpoint, header)
	if err != nil {
		return fmt.Errorf("WS 연결 실패: %w", err)
	}

	// 연결 수명: 함수 반환(stopPing) 또는 앱 ctx 취소 시 아래 고루틴들 종료
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()

	// 연결 수명 종료 시 conn 강제 종료 → ReadMessage 블록 해제
	go func() {
		<-pingCtx.Done()
		conn.Close()
	}()
	defer conn.Close()

	// 구독 메시지 전송
	subscribeMsg := []interface{}{
		map[string]string{"ticket": "position-manager-asset"},
		map[string]string{"type": "myAsset"},
		map[string]string{"format": "DEFAULT"},
	}
	if err := conn.WriteJSON(subscribeMsg); err != nil {
		return fmt.Errorf("구독 메시지 전송 실패: %w", err)
	}

	logger.Info("[Upbit AssetStream] 연결됨 — 실시간 잔고 수신 시작")

	// keepalive: 유휴 시 서버가 약 120초에 연결을 끊으므로 30초마다 ping 전송.
	// 구독 이후 write는 ping뿐이라 write 직렬화 불필요.
	go func() {
		ticker := time.NewTicker(upbitPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// 잔고 이벤트는 장시간 없을 수 있어 pong 수신으로도 read deadline 갱신.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(upbitReadDeadline))
	})

	// 수신 루프
	for {
		conn.SetReadDeadline(time.Now().Add(upbitReadDeadline))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// ctx 취소에 의한 종료인지 확인
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("메시지 수신 오류: %w", err)
		}

		balances, err := parseAssetEvent(msg)
		if err != nil {
			logger.Warnf("[Upbit AssetStream] 이벤트 파싱 실패: %v", err)
			continue
		}
		if balances != nil {
			s.onUpdate(balances)
		}
	}
}

// assetEvent 업비트 myAsset WS 이벤트
// balance/locked는 따옴표 없는 숫자(Double)로 오므로 json.Number로 받는다.
type assetEvent struct {
	Type   string `json:"type"`
	Assets []struct {
		Currency string      `json:"currency"`
		Balance  json.Number `json:"balance"`
		Locked   json.Number `json:"locked"`
	} `json:"assets"`
}

// parseAssetEvent JSON 메시지를 Balance 맵으로 변환
// type != "myAsset"인 메시지(예: 서버 응답)는 nil 반환
func parseAssetEvent(data []byte) (map[string]*exchange.Balance, error) {
	var event assetEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("JSON 파싱 실패: %w", err)
	}
	if event.Type != "myAsset" {
		return nil, nil
	}

	balances := make(map[string]*exchange.Balance, len(event.Assets))
	for _, a := range event.Assets {
		free, err := a.Balance.Float64()
		if err != nil {
			logger.Warnf("[Upbit AssetStream] balance 파싱 실패 currency=%s: %v", a.Currency, err)
			free = 0
		}
		locked, err := a.Locked.Float64()
		if err != nil {
			logger.Warnf("[Upbit AssetStream] locked 파싱 실패 currency=%s: %v", a.Currency, err)
			locked = 0
		}
		balances[a.Currency] = &exchange.Balance{
			Currency: a.Currency,
			Free:     free,
			Used:     locked,
			Total:    free + locked,
		}
	}
	return balances, nil
}
