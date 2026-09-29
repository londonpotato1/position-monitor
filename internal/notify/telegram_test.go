package notify

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestSanitizeURLError_NoTokenLeak(t *testing.T) {
	// url.Error 시뮬레이션 (토큰 포함 URL)
	fakeErr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot123:TEST/sendMessage",
		Err: errors.New("dial tcp: i/o timeout"),
	}
	msg := sanitizeURLError(fakeErr).Error()
	if strings.Contains(msg, "123:TEST") {
		t.Fatalf("token leaked in sanitized error: %s", msg)
	}
}

func TestSanitizeURLError_NonURLError_Passthrough(t *testing.T) {
	plain := errors.New("some other error")
	if sanitizeURLError(plain) != plain {
		t.Fatalf("non-url.Error should be returned as-is")
	}
}

func TestSanitizeURLError_PreservesOp(t *testing.T) {
	fakeErr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot123:TEST/sendMessage",
		Err: errors.New("connection refused"),
	}
	msg := sanitizeURLError(fakeErr).Error()
	if !strings.Contains(msg, "Post") || !strings.Contains(msg, "connection refused") {
		t.Fatalf("Op and underlying error should be preserved: %s", msg)
	}
}

// fakeTransport 는 네트워크 없이 고정 응답을 돌려주고 호출 수를 센다.
type fakeTransport struct {
	status int
	body   string
	err    error // 설정 시 전송 계층 오류 반환
	calls  atomic.Int32
}

func (f *fakeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

func newTestNotifier(token, chat string, master bool, rt *fakeTransport) *TelegramNotifier {
	n := NewTelegramNotifier(token, chat, master, zerolog.Nop())
	n.client = &http.Client{Transport: rt}
	return n
}

// 마스터 OFF 로 생성하면 비활성이고 HTTP 호출 0.
func TestNotifier_MasterOffAtConstruction_NoHTTP(t *testing.T) {
	rt := &fakeTransport{status: 200, body: `{"ok":true}`}
	n := newTestNotifier("123:TEST", "0", false, rt)

	if err := n.DisabledReason(); !errors.Is(err, ErrMasterOff) {
		t.Fatalf("master OFF 인데 DisabledReason() = %v", err)
	}
	if err := n.Send("x"); !errors.Is(err, ErrMasterOff) {
		t.Fatalf("master OFF 전송 오류 = %v, want ErrMasterOff", err)
	}
	if got := rt.calls.Load(); got != 0 {
		t.Fatalf("master OFF 인데 HTTP 호출 %d 회", got)
	}
}

// 비활성으로 건너뛴 전송은 성공(nil)과 구분된다.
func TestNotifier_DisabledSendIsNotSuccess(t *testing.T) {
	rt := &fakeTransport{status: 200, body: `{"ok":true}`}
	n := newTestNotifier("", "", true, rt)

	if err := n.Send("x"); err == nil {
		t.Fatal("비활성 전송이 nil 반환 (성공과 구분 불가)")
	}
	if got := rt.calls.Load(); got != 0 {
		t.Fatalf("비활성인데 HTTP 호출 %d 회", got)
	}
}

// 비활성 사유 (마스터 OFF vs 키 없음) 구분 + 없는 키 이름만 명시.
func TestNotifier_DisabledReasonDistinguishesMasterOffAndMissingKeys(t *testing.T) {
	masterOff := NewTelegramNotifier("123:TEST", "0", false, zerolog.Nop()).DisabledReason()
	if !errors.Is(masterOff, ErrMasterOff) || errors.Is(masterOff, ErrKeysMissing) {
		t.Fatalf("master OFF 사유 = %v", masterOff)
	}

	noChat := NewTelegramNotifier("123:TEST", "", true, zerolog.Nop()).DisabledReason()
	if !errors.Is(noChat, ErrKeysMissing) || errors.Is(noChat, ErrMasterOff) {
		t.Fatalf("키 없음 사유 = %v", noChat)
	}
	if msg := noChat.Error(); !strings.Contains(msg, "TELEGRAM_CHAT_ID") ||
		strings.Contains(msg, "TELEGRAM_BOT_TOKEN") || strings.Contains(msg, "123:TEST") {
		t.Fatalf("없는 키 이름만 명시해야 함: %q", msg)
	}

	if err := NewTelegramNotifier("123:TEST", "0", true, zerolog.Nop()).DisabledReason(); err != nil {
		t.Fatalf("활성 상태인데 사유 = %v", err)
	}
}

// 429 flood control: parameters.retry_after 를 호출자가 읽을 수 있고 토큰은 노출되지 않는다.
func TestNotifier_429ExposesRetryAfter(t *testing.T) {
	rt := &fakeTransport{
		status: 429,
		body:   `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`,
	}
	n := newTestNotifier("123:TEST", "0", true, rt)

	err := n.Send("x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 429 || apiErr.RetryAfter != 5*time.Second {
		t.Fatalf("status=%d retryAfter=%v, want 429 / 5s", apiErr.StatusCode, apiErr.RetryAfter)
	}
	if strings.Contains(err.Error(), "123:TEST") {
		t.Fatalf("token leaked: %s", err)
	}
}

// 전송 계층 오류: http.Client 가 *url.Error 로 감싸 URL(봇 토큰 포함)이 붙어도 반환 오류에 토큰이 없다.
func TestNotifier_TransportErrorDoesNotLeakToken(t *testing.T) {
	rt := &fakeTransport{err: errors.New("dial tcp: i/o timeout")}
	n := newTestNotifier("123:TEST", "0", true, rt)

	err := n.Send("x")
	if err == nil {
		t.Fatal("전송 계층 오류인데 nil 반환")
	}
	if strings.Contains(err.Error(), "123:TEST") {
		t.Fatalf("token leaked: %s", err)
	}
}
