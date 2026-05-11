package logger

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var log zerolog.Logger

// LogEntry UI로 전달되는 로그 항목
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}

// UILogCallback UI로 로그를 전달하는 콜백 타입
type UILogCallback func(entry LogEntry)

// UILogHandler UI 로그 핸들러 (Wails 이벤트 전달용)
type UILogHandler struct {
	mu       sync.RWMutex
	logs     []LogEntry
	maxLogs  int
	callback UILogCallback
}

// NewUILogHandler 새 UI 로그 핸들러 생성
func NewUILogHandler(maxLogs int) *UILogHandler {
	if maxLogs <= 0 {
		maxLogs = 1000
	}
	return &UILogHandler{
		logs:    make([]LogEntry, 0, maxLogs),
		maxLogs: maxLogs,
	}
}

// SetCallback UI 콜백 설정
func (h *UILogHandler) SetCallback(cb UILogCallback) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callback = cb
}

// AddEntry 로그 항목 추가
func (h *UILogHandler) AddEntry(entry LogEntry) {
	h.mu.Lock()
	h.logs = append(h.logs, entry)
	if len(h.logs) > h.maxLogs {
		h.logs = h.logs[len(h.logs)-h.maxLogs:]
	}
	cb := h.callback
	h.mu.Unlock()

	if cb != nil {
		cb(entry)
	}
}

// GetLogs 최근 로그 반환
func (h *UILogHandler) GetLogs(count int) []LogEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if count <= 0 || count > len(h.logs) {
		count = len(h.logs)
	}
	start := len(h.logs) - count
	result := make([]LogEntry, count)
	copy(result, h.logs[start:])
	return result
}

// Clear 로그 클리어
func (h *UILogHandler) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = h.logs[:0]
}

// uiHandler 전역 UI 로그 핸들러
var uiHandler *UILogHandler

// GetUIHandler 전역 UI 핸들러 반환
func GetUIHandler() *UILogHandler {
	return uiHandler
}

// ZeroLogger 내부 zerolog.Logger 반환 (services 패키지 등에서 사용)
func ZeroLogger() zerolog.Logger {
	return log
}

// Logger 로거 구조체 (인스턴스용)
type Logger struct {
	zlog zerolog.Logger
	name string
}

// NewLogger 새 로거 생성
func NewLogger(name string) *Logger {
	return &Logger{
		zlog: log.With().Str("module", name).Logger(),
		name: name,
	}
}

// Info 정보 로그
func (l *Logger) Info(msg string, args ...interface{}) {
	event := l.zlog.Info()
	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			event = event.Interface(key, args[i+1])
		}
	}
	event.Msg(msg)
	l.emitUI("INFO", msg)
}

// Warn 경고 로그
func (l *Logger) Warn(msg string, args ...interface{}) {
	event := l.zlog.Warn()
	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			event = event.Interface(key, args[i+1])
		}
	}
	event.Msg(msg)
	l.emitUI("WARN", msg)
}

// Error 에러 로그
func (l *Logger) Error(msg string, args ...interface{}) {
	event := l.zlog.Error()
	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			event = event.Interface(key, args[i+1])
		}
	}
	event.Msg(msg)
	l.emitUI("ERROR", msg)
}

// Debug 디버그 로그
func (l *Logger) Debug(msg string, args ...interface{}) {
	event := l.zlog.Debug()
	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			event = event.Interface(key, args[i+1])
		}
	}
	event.Msg(msg)
}

// Infof 포맷 정보 로그
func (l *Logger) Infof(format string, v ...interface{}) {
	l.zlog.Info().Msgf(format, v...)
}

// Warnf 포맷 경고 로그
func (l *Logger) Warnf(format string, v ...interface{}) {
	l.zlog.Warn().Msgf(format, v...)
}

// Errorf 포맷 에러 로그
func (l *Logger) Errorf(format string, v ...interface{}) {
	l.zlog.Error().Msgf(format, v...)
}

// Debugf 포맷 디버그 로그
func (l *Logger) Debugf(format string, v ...interface{}) {
	l.zlog.Debug().Msgf(format, v...)
}

func (l *Logger) emitUI(level, msg string) {
	if uiHandler != nil {
		uiHandler.AddEntry(LogEntry{
			Timestamp: time.Now(),
			Level:     level,
			Message:   msg,
			Source:    l.name,
		})
	}
}

// Init 로거 초기화
func Init(level string, logFile string) error {
	var lvl zerolog.Level
	switch level {
	case "debug":
		lvl = zerolog.DebugLevel
	case "info":
		lvl = zerolog.InfoLevel
	case "warn":
		lvl = zerolog.WarnLevel
	case "error":
		lvl = zerolog.ErrorLevel
	default:
		lvl = zerolog.InfoLevel
	}

	zerolog.TimeFieldFormat = "15:04:05.000"

	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "15:04:05.000",
	}

	var writers []io.Writer
	writers = append(writers, consoleWriter)

	if logFile != "" {
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		writers = append(writers, file)
	}

	multi := zerolog.MultiLevelWriter(writers...)
	log = zerolog.New(multi).Level(lvl).With().Timestamp().Logger()

	// UI 핸들러 초기화
	uiHandler = NewUILogHandler(1000)

	return nil
}

// --- Package-level convenience functions ---

// Debug 디버그 로그
func Debug(msg string) {
	log.Debug().Msg(msg)
}

// Debugf 포맷 디버그 로그
func Debugf(format string, v ...interface{}) {
	log.Debug().Msgf(format, v...)
}

// Info 정보 로그
func Info(msg string) {
	log.Info().Msg(msg)
}

// Infof 포맷 정보 로그
func Infof(format string, v ...interface{}) {
	log.Info().Msgf(format, v...)
}

// Warn 경고 로그
func Warn(msg string) {
	log.Warn().Msg(msg)
}

// Warnf 포맷 경고 로그
func Warnf(format string, v ...interface{}) {
	log.Warn().Msgf(format, v...)
}

// Error 에러 로그
func Error(msg string) {
	log.Error().Msg(msg)
}

// Errorf 포맷 에러 로그
func Errorf(format string, v ...interface{}) {
	log.Error().Msgf(format, v...)
}

// Fatal 치명적 에러 로그 (프로그램 종료)
func Fatal(msg string) {
	log.Fatal().Msg(msg)
}

// Fatalf 포맷 치명적 에러 로그
func Fatalf(format string, v ...interface{}) {
	log.Fatal().Msgf(format, v...)
}
