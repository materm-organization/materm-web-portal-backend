package logs

import (
	"context"
	"testing"
	"time"
)

func TestWarnEveryThrottles(t *testing.T) {
	key := "test:" + t.Name()
	WarnEvery(key, time.Hour, "first")
	warnMu.Lock()
	first := warnLast[key]
	warnMu.Unlock()
	if first.IsZero() {
		t.Fatal("first call must log and be remembered")
	}
	WarnEvery(key, time.Hour, "second")
	warnMu.Lock()
	second := warnLast[key]
	warnMu.Unlock()
	if !second.Equal(first) {
		t.Error("a second call within the interval must be dropped")
	}
	WarnEvery(key, 0, "third")
	warnMu.Lock()
	third := warnLast[key]
	warnMu.Unlock()
	if !third.After(first) {
		t.Error("with a zero interval every call logs")
	}
}

func TestFromContextDoesNotPanic(t *testing.T) {
	FromContext(context.Background()).Info("plain")
	FromContext(WithRequest(context.Background(), "req-1")).Warn("with id")
}
