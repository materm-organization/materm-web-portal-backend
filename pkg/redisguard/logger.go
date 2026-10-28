package redisguard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// libraryLogEvery is how often one kind of go-redis library message may reach our log (B4: the pool used to
// print "failed to dial after 5 attempts" about once a second while Redis was down).
const libraryLogEvery = time.Minute

type throttledLogger struct{}

// Printf routes a go-redis library message to logs.WarnEvery, keyed by its format string, so every kind
// appears at most once a minute. The text can contain the Redis address and the network error, never the
// password (go-redis does not print options).
func (throttledLogger) Printf(_ context.Context, format string, v ...interface{}) {
	logs.WarnEvery("redis.lib:"+format, libraryLogEvery, fmt.Sprintf(format, v...))
}

var loggerOnce sync.Once

// InstallLibraryLogger replaces go-redis's own logger (process wide, once) with the throttled adapter.
func InstallLibraryLogger() {
	loggerOnce.Do(func() { redis.SetLogger(throttledLogger{}) })
}
