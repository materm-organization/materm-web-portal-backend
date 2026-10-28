package logs

import (
	"io"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var captureMu sync.Mutex

// Capture redirects the JSON log (stdout sink) to w until the returned function is called. It exists for the
// tests that assert something never reaches the log (a preview token, a form body): the production logger
// writes to the real stdout, which a test cannot intercept. Not safe for tests that run in parallel with
// each other; call restore with defer.
func Capture(w io.Writer) (restore func()) {
	captureMu.Lock()
	prev := logger
	cfg := zap.NewProductionEncoderConfig()
	cfg.TimeKey = "timestamp"
	cfg.StacktraceKey = ""
	logger = zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(cfg), zapcore.AddSync(w), zapcore.InfoLevel),
		zap.AddCaller(), zap.AddCallerSkip(1))
	return func() {
		logger = prev
		captureMu.Unlock()
	}
}
