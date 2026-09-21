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
	wsPrivateURL       = "wss://ws-api.bithumb.com/websocket/v2/private"
	wsInitialDelay     = 1 * time.Second
	wsMaxDelay         = 30 * time.Second
	wsPingInterval     = 30 * time.Second
	wsReadDeadline     = 90 * time.Second
	wsHandshakeTimeout = 10 * time.Second
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

		start := time.Now()
		err := s.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		// 최대 백오프(wsMaxDelay)보다 오래 유지된 세션 = 정상 연결이었음 → 백오프 초기화.
		// 즉시 실패(인증 거부 등)는 초기화하지 않아 재시도 폭주를 막는다.
		if time.Since(start) > wsMaxDelay {
			delay = wsInitialDelay
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
	// JWT 인증 헤더 생성 (WS용: access_key + nonce + timestamp)
	token := s.client.generateJWT(map[string]interface{}{
		"access_key": s.client.apiKey,
		"nonce":      uuid.New().String(),
		"timestamp":  time.Now().UnixMilli(),
	})
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)

	dialer := websocket.Dialer{HandshakeTimeout: wsHandshakeTimeout}
	conn, _, err := dialer.DialContext(ctx, wsPrivateURL, header)
	if err != nil {
		return err
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

	// keepalive: 유휴 시 서버가 약 120초에 연결을 끊으므로 30초마다 ping 전송.
	// 구독 이후 write는 ping뿐이라 write 직렬화 불필요.
	go func() {
		ticker := time.NewTicker(wsPingInterval)
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
		return conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	})

	// 읽기 루프
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
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
