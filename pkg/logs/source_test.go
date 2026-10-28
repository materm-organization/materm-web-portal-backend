package logs

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// The data-source lines follow the KKL convention ("🚀 - Cache Key", "🚀 - <What> From Redis|Postgres") and
// exist only while SetSourceTrace(true) is on.
func TestSourceLinesAreOffUnlessEnabled(t *testing.T) {
	core, seen := observer.New(zapcore.InfoLevel)
	old := logger
	logger = zap.New(core, zap.AddCallerSkip(1))
	defer func() { logger = old; SetSourceTrace(false) }()

	SetSourceTrace(false)
	CacheKey("materm:k")
	Source("Principal", "Redis")
	if seen.Len() != 0 {
		t.Fatalf("nothing may be logged while off, got %d lines", seen.Len())
	}

	SetSourceTrace(true)
	CacheKey("materm:authz:7")
	Source("Principal", "Redis")
	Source("Principal", "Postgres")
	got := seen.All()
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d", len(got))
	}
	if got[0].Message != "🚀 - Cache Key" || got[0].ContextMap()["cache_key"] != "materm:authz:7" {
		t.Fatalf("bad cache key line: %+v", got[0])
	}
	if got[1].Message != "🚀 - Principal From Redis" || got[2].Message != "🚀 - Principal From Postgres" {
		t.Fatalf("bad source lines: %q / %q", got[1].Message, got[2].Message)
	}
}
