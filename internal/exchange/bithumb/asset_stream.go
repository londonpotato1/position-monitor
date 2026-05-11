// Package bithumb - private WebSocket balance stream (myAsset)
// 빗썸 프라이빗 WS 잔고 스트림 (업비트 프로토콜 호환)
package bithumb

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/londonpotato1/position-monitor/internal/exchange"
)

const (
	wsPrivateURL   = "wss://ws-api.bithumb.com/websocket/v1/private"
	wsInitialDelay = 1 * time.Second
	wsMaxDelay     = 30 * time.Second
)

// AssetStream 빗썸 프라이빗 WS 잔고 스트림
type AssetStream struct {
	client      *Client
	onUpdate    func(map[string]*exchange.Balance)
	conn        *websocket.Conn
	running     bool
	reconnectMu sync.Mutex
}

// NewAssetStream 새 잔고 스트림 생성
func NewAssetStream(client *Client, onUpdate func(map[string]*exchange.Balance)) *AssetStream {
	return &AssetStream{
		client:   client,
		onUpdate: onUpdate,
	}
}

// Start 잔고 스트림 시작 (비블로킹)
func (s *AssetStream) Start(ctx context.Context) {
	go s.runLoop(ctx)
}

// runLoop 연결 → 서브 → 재연결 루프
func (s *AssetStream) runLoop(ctx context.Context) {
	delay := wsInitialDelay
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := s.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.client.log("빗썸 WS 연결 끊김, 재연결 대기: "+err.Error(), "warning")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		delay *= 2
		if delay > wsMaxDelay {
			delay = wsMaxDelay
		}
	}
}

// connectAndServe WS 연결 → 구독 → 읽기 루프
func (s *AssetStream) connectAndServe(ctx context.Context) error {
	// JWT 인증 헤더 생성 (WS용: access_key + nonce 만 포함)
	token := s.client.generateJWT(map[string]interface{}{
		"access_key": s.client.apiKey,
		"nonce":      uuid.New().String(),
	})
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsPrivateURL, header)
	if err != nil {
		return err
	}
	defer conn.Close()

	s.reconnectMu.Lock()
	s.conn = conn
	s.running = true
	s.reconnectMu.Unlock()

	defer func() {
		s.reconnectMu.Lock()
		s.running = false
		s.conn = nil
		s.reconnectMu.Unlock()
	}()

	// 구독 메시지 전송
	subscribe := []interface{}{
		map[string]string{"ticket": "position-manager-asset"},
		map[string]string{"type": "myAsset"},
		map[string]string{"format": "DEFAULT"},
	}
	subBytes, err := json.Marshal(subscribe)
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, subBytes); err != nil {
		return err
	}

	s.client.log("빗썸 WS 잔고 스트림 연결됨", "info")

	// 읽기 루프
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		s.handleMessage(msg)
	}
}

// myAssetEvent 빗썸 myAsset 이벤트 구조체
type myAssetEvent struct {
	Type           string `json:"type"`
	AssetTimestamp int64  `json:"asset_timestamp"`
	Assets         []struct {
		Currency string `json:"currency"`
		Balance  string `json:"balance"`
		Locked   string `json:"locked"`
	} `json:"assets"`
}

// handleMessage WS 메시지 파싱 → onUpdate 호출
func (s *AssetStream) handleMessage(msg []byte) {
	var event myAssetEvent
	if err := json.Unmarshal(msg, &event); err != nil {
		return
	}
	if event.Type != "myAsset" {
		return
	}

	balances := make(map[string]*exchange.Balance, len(event.Assets))
	for _, a := range event.Assets {
		free, _ := strconv.ParseFloat(a.Balance, 64)
		locked, _ := strconv.ParseFloat(a.Locked, 64)
		currency := strings.ToUpper(a.Currency)
		// Bithumb 티커 정규화 (WAXL → AXL) + 원본 currency 키도 보존
		standard := NormalizeBithumbSymbol(currency)
		bal := &exchange.Balance{
			Currency: standard,
			Free:     free,
			Used:     locked,
			Total:    free + locked,
		}
		balances[standard] = bal
		if standard != currency {
			balances[currency] = bal
		}
	}

	if s.onUpdate != nil {
		s.onUpdate(balances)
	}
}
