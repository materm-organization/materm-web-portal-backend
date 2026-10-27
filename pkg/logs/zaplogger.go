package logs

import (
	"fmt"
	"os"
	"sync"
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
		MaxAge:     0,
		Compress:   false,
		LocalTime:  true,
	}
}

var (
	logger *zap.Logger
	once   sync.Once
)

func init() {
	once.Do(setup)
}

func setup() {
	today := time.Now().Format("2006-01-02")
	writer := &dailyWriter{current: newLumberjack(today), date: today}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.StacktraceKey = ""

	core := zapcore.NewTee(
		zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderCfg),
			zapcore.AddSync(writer),
			zapcore.InfoLevel,
		),
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderCfg),
			zapcore.AddSync(os.Stdout),
			zapcore.InfoLevel,
		),
	)

	logger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}

// Info logs an informational message.
func Info(msg string, fields ...zap.Field) {
	logger.Info(msg, fields...)
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
