// Package services holds the probe logic of the system module (doc/05 section 4, doc/08 P-20/P-21).
package services

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/materm-organization/materm-web-portal-backend/modules/system/models"
)

const (
	checkTimeout = time.Second
	defaultCache = 5 * time.Second
)

// Deps are the collaborators of the SystemService. The pingers are plain functions so tests can fake them.
type Deps struct {
	// PingDB checks the database (SELECT 1). Required. Once the portal views exist (PB-3) it can move to a
	// select on v_public_locales (doc/08 P-21); the probe stays a status-only answer.
	PingDB func(ctx context.Context) error
	// PingRedis checks Redis; nil means Redis is not connected (degraded start in dev), which counts as down.
	PingRedis func(ctx context.Context) error
	// Strict is staging/prod. It no longer changes readiness: the portal reads fall back to the database when the
	// cache is gone, so a Redis outage degrades the service in every environment instead of removing it from the
	// load balancer (decision doc/08 section 2.7 item 5, doc/05 section 4.2; the admin backend is the one that
	// answers 503 without Redis). Kept so that callers keep compiling.
	Strict bool
	// Now is the clock (default time.Now).
	Now func() time.Time
	// CacheTTL bounds how often the dependencies are probed (default 5 s): the probe needs no credential, so
	// a flood of /ready requests must not become a flood of database queries.
	CacheTTL time.Duration
}

// SystemService answers the liveness and readiness questions.
type SystemService interface {
	// Liveness is true as long as the process runs; it touches nothing.
	Liveness() bool
	// Readiness probes the essential dependencies (in parallel, 1 s each, cached up to 5 s).
	Readiness(ctx context.Context) models.Readiness
}

type systemService struct {
	d Deps

	mu     sync.Mutex
	at     time.Time
	cached *models.Readiness
}

// NewSystemService builds the service.
func NewSystemService(d Deps) SystemService {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.CacheTTL <= 0 {
		d.CacheTTL = defaultCache
	}
	return &systemService{d: d}
}

// DBPinger returns a PingDB function for a GORM handle.
func DBPinger(db *gorm.DB) func(context.Context) error {
	return func(ctx context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	}
}

// RedisPinger returns a PingRedis function; a nil client yields nil (not connected).
func RedisPinger(rdb *redis.Client) func(context.Context) error {
	if rdb == nil {
		return nil
	}
	return func(ctx context.Context) error { return rdb.Ping(ctx).Err() }
}

func (s *systemService) Liveness() bool { return true }

// Readiness: ready = database ok. Redis down (or not connected) only degrades: 200 {status: degraded}, in dev and
// in staging/prod alike. The database down is 503 {status: down}. The body never names a dependency.
func (s *systemService) Readiness(ctx context.Context) models.Readiness {
	s.mu.Lock()
	if s.cached != nil && s.d.Now().Sub(s.at) < s.d.CacheTTL {
		r := *s.cached
		s.mu.Unlock()
		return r
	}
	s.mu.Unlock()

	var dbOK, redisOK bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); dbOK = s.ping(ctx, s.d.PingDB) }()
	go func() { defer wg.Done(); redisOK = s.ping(ctx, s.d.PingRedis) }()
	wg.Wait()

	var r models.Readiness
	switch {
	case !dbOK:
		r = models.Readiness{Ready: false, Status: models.StatusDown}
	case redisOK:
		r = models.Readiness{Ready: true, Status: models.StatusOK}
	default:
		r = models.Readiness{Ready: true, Status: models.StatusDegraded}
	}

	s.mu.Lock()
	s.cached, s.at = &r, s.d.Now()
	s.mu.Unlock()
	return r
}

// ping runs one check with a 1 s timeout; a nil function or an error is "not ok". The error text is dropped
// on purpose: it could carry a host name.
func (s *systemService) ping(ctx context.Context, fn func(context.Context) error) bool {
	if fn == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return fn(cctx) == nil
}
