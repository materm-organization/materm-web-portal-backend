package logs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// dailyWriter opens a new lumberjack file whenever the date changes.
type dailyWriter struct {
	mu      sync.Mutex
	current *lumberjack.Logger
	date    string
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	if today != w.date {
		if w.current != nil {
			_ = w.current.Close()
		}
		w.current = newLumberjack(today)
		w.date = today
	}
	return w.current.Write(p)
}

func (w *dailyWriter) Sync() error { return nil }

func newLumberjack(date string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   fmt.Sprintf("logs/%s.log", date),
		MaxSize:    100,
		MaxBackups: 0,
		MaxAge:     30, // days (NFR-22: keep 30 days)
		Compress:   false,
		LocalTime:  true,
	}
}

var (
	logger     *zap.Logger
	fileLogger *zap.Logger // JSON file sink only (nil when LOG_FILE=off)
	once       sync.Once
)

func init() {
	once.Do(setup)
}

// fileLoggingEnabled: the JSON file sink under logs/ is on by default (local development). Containers
// should log to stdout only: set LOG_FILE=off.
func fileLoggingEnabled() bool {
	switch strings.ToLower(os.Getenv("LOG_FILE")) {
	case "off", "false", "0", "no":
		return false
	}
	return true
}

func setup() {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.StacktraceKey = ""

	// stdout is JSON as well (NFR-22: structured logs with request_id, latency, status)
	cores := []zapcore.Core{
		zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(os.Stdout), zapcore.InfoLevel),
	}
	if fileLoggingEnabled() {
		today := time.Now().Format("2006-01-02")
		writer := &dailyWriter{current: newLumberjack(today), date: today}
		cores = append(cores, zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(writer), zapcore.InfoLevel))
	}

	logger = zap.New(zapcore.NewTee(cores...), zap.AddCaller(), zap.AddCallerSkip(1))
	if len(cores) > 1 {
		fileLogger = zap.New(cores[1], zap.AddCaller(), zap.AddCallerSkip(1))
	}
}

var sourceTrace atomic.Bool

// SetSourceTrace turns the data-source lines (CacheKey, Source) on or off. They are for development: they
// name the cache key and say where an answer came from, which is noise (and key material) in production.
func SetSourceTrace(on bool) { sourceTrace.Store(on) }

// CacheKey logs the Redis key about to be read ("🚀 - Cache Key"). A no-op unless SetSourceTrace(true).
func CacheKey(key string) {
	if sourceTrace.Load() {
		logger.Info("🚀 - Cache Key", zap.String("cache_key", key))
	}
}

// Source logs where an answer came from, e.g. Source("Principal", "Redis") -> "🚀 - Principal From Redis"
// (from is "Redis", "Postgres" or "Memory"). A no-op unless SetSourceTrace(true).
func Source(what, from string) {
	if sourceTrace.Load() {
		logger.Info("🚀 - " + what + " From " + from)
	}
}

// InfoFileOnly writes an info line to the JSON log file only, not to stdout. The access log uses it in
// development, where stdout shows the one-line console format instead (bootstrap accessLog); with the file sink
// off (LOG_FILE=off) the line is dropped.
func InfoFileOnly(msg string, fields ...zap.Field) {
	if fileLogger != nil {
		fileLogger.Info(msg, fields...)
	}
}

// Field is a structured log field (zap.Field); build them with String, Int, Bool, Err, Duration.
type Field = zap.Field

// String builds a string field.
func String(key, val string) Field { return zap.String(key, val) }

// Int builds an int field.
func Int(key string, val int) Field { return zap.Int(key, val) }

// Int64 builds an int64 field.
func Int64(key string, val int64) Field { return zap.Int64(key, val) }

// Bool builds a bool field.
func Bool(key string, val bool) Field { return zap.Bool(key, val) }

// Duration builds a duration field.
func Duration(key string, val time.Duration) Field { return zap.Duration(key, val) }

// Err builds an `error` field. Pass only errors whose text carries no secrets or personal data.
func Err(err error) Field { return zap.Error(err) }

// Info logs an informational message.
func Info(msg string, fields ...zap.Field) {
	logger.Info(msg, fields...)
}

// Warn logs a degraded-but-working condition (e.g. Redis fallback to the database).
func Warn(msg string, fields ...zap.Field) {
	logger.Warn(msg, fields...)
}

var (
	warnMu   sync.Mutex
	warnLast = map[string]time.Time{}
)

// WarnEvery logs msg at most once per interval for the same key, so a degraded dependency does not flood
// the log (doc/06 section 8.1: at most one line a minute per kind of degradation).
func WarnEvery(key string, interval time.Duration, msg string, fields ...zap.Field) {
	warnMu.Lock()
	last, seen := warnLast[key]
	now := time.Now()
	if seen && now.Sub(last) < interval {
		warnMu.Unlock()
		return
	}
	warnLast[key] = now
	warnMu.Unlock()
	logger.Warn(msg, fields...)
}

// Error logs an error message. Accepts string or error.
func Error(msg interface{}, fields ...zap.Field) {
	logger.Error(toStr(msg), fields...)
}

// ImportantLog logs an info message tagged with a searchable key.
func ImportantLog(key string, msg interface{}, fields ...zap.Field) {
	fields = append(fields, zap.String("search_key", key))
	logger.Info(toStr(msg), fields...)
}

// ErrorWithKeys logs an error tagged with a searchable key.
func ErrorWithKeys(key string, msg interface{}, fields ...zap.Field) {
	fields = append(fields, zap.String("search_key", key))
	logger.Error(toStr(msg), fields...)
}

// Sync flushes any buffered log entries.
func Sync() {
	_ = logger.Sync()
}

func toStr(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case error:
		return val.Error()
	default:
		return fmt.Sprintf("%v", val)
	}
}

type ctxKey struct{}

// ctxFields are the identifiers a service wants on every line it logs for one request. The portal has no
// users, so unlike the admin backend there is no user id.
type ctxFields struct {
	requestID string
}

// WithRequest returns a context that carries the request id for FromContext.
func WithRequest(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ctxFields{requestID: requestID})
}

// Logger is a logger bound to the request identifiers found in a context.
type Logger struct{ l *zap.Logger }

// FromContext returns a logger that adds request_id (when known) to every line.
func FromContext(ctx context.Context) *Logger {
	f, _ := ctx.Value(ctxKey{}).(ctxFields)
	l := logger
	if f.requestID != "" {
		l = l.With(zap.String("request_id", f.requestID))
	}
	return &Logger{l: l.WithOptions(zap.AddCallerSkip(0))}
}

// Info logs at info level.
func (l *Logger) Info(msg string, fields ...zap.Field) { l.l.Info(msg, fields...) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, fields ...zap.Field) { l.l.Warn(msg, fields...) }

// Error logs at error level.
func (l *Logger) Error(msg string, fields ...zap.Field) { l.l.Error(msg, fields...) }
