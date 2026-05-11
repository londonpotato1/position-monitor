// Package upbit provides a private WebSocket asset stream for real-time balance events.
// 업비트 프라이빗 WS - 실시간 잔고 이벤트 스트림
package upbit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
		if err := s.connectAndServe(ctx); err != nil {
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
			continue
		}
		backoff = time.Second // clean disconnect: reset backoff
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

	// ctx 취소 시 conn 강제 종료 → ReadMessage 블록 해제
	go func() {
		<-ctx.Done()
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

	// 수신 루프
	for {
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
type assetEvent struct {
	Type   string `json:"type"`
	Assets []struct {
		Currency string `json:"currency"`
		Balance  string `json:"balance"`
		Locked   string `json:"locked"`
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
		free, err := strconv.ParseFloat(a.Balance, 64)
		if err != nil {
			logger.Warnf("[Upbit AssetStream] balance 파싱 실패 currency=%s: %v", a.Currency, err)
			free = 0
		}
		locked, err := strconv.ParseFloat(a.Locked, 64)
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
