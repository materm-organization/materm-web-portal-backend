package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/system/models"
)

func ok(context.Context) error  { return nil }
func bad(context.Context) error { return errors.New("host db.internal unreachable") }

func TestReadinessMatrix(t *testing.T) {
	cases := []struct {
		name      string
		db, redis func(context.Context) error
		strict    bool
		ready     bool
		status    string
	}{
		{"all up, prod", ok, ok, true, true, models.StatusOK},
		{"all up, dev", ok, ok, false, true, models.StatusOK},
		{"redis down, prod", ok, bad, true, true, models.StatusDegraded},
		{"redis not connected, prod", ok, nil, true, true, models.StatusDegraded},
		{"redis down, dev", ok, bad, false, true, models.StatusDegraded},
		{"redis not connected, dev", ok, nil, false, true, models.StatusDegraded},
		{"db down, prod", bad, ok, true, false, models.StatusDown},
		{"db down, dev", bad, ok, false, false, models.StatusDown},
		{"everything down, dev", bad, bad, false, false, models.StatusDown},
		{"everything down, prod", bad, bad, true, false, models.StatusDown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSystemService(Deps{PingDB: tc.db, PingRedis: tc.redis, Strict: tc.strict})
			if r := s.Readiness(context.Background()); r.Ready != tc.ready || r.Status != tc.status {
				t.Errorf("got %+v, want ready=%v status=%s", r, tc.ready, tc.status)
			}
		})
	}
}

func TestLivenessTouchesNothing(t *testing.T) {
	var calls atomic.Int32
	probe := func(context.Context) error { calls.Add(1); return nil }
	s := NewSystemService(Deps{PingDB: probe, PingRedis: probe})
	if !s.Liveness() || calls.Load() != 0 {
		t.Errorf("liveness must be true and call no dependency (calls=%d)", calls.Load())
	}
}

// The probe needs no credential: a burst of requests must reach the database at most once per cache window.
func TestReadinessIsCached(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s := NewSystemService(Deps{
		PingDB:    func(context.Context) error { calls.Add(1); return nil },
		PingRedis: ok, Strict: true, Now: func() time.Time { return now }, CacheTTL: 5 * time.Second,
	})
	for i := 0; i < 20; i++ {
		s.Readiness(context.Background())
	}
	if calls.Load() != 1 {
		t.Fatalf("20 probes inside the window cost %d database pings, want 1", calls.Load())
	}
	now = now.Add(6 * time.Second)
	s.Readiness(context.Background())
	if calls.Load() != 2 {
		t.Errorf("after the window the dependency must be probed again, pings=%d", calls.Load())
	}
}

func TestSlowDependencyTimesOut(t *testing.T) {
	slow := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	s := NewSystemService(Deps{PingDB: slow, PingRedis: ok, Strict: true})
	start := time.Now()
	r := s.Readiness(context.Background())
	if r.Ready || time.Since(start) > 3*time.Second {
		t.Errorf("a hanging database must make the service not ready within ~1s: %+v after %v", r, time.Since(start))
	}
}

func TestRedisPingerNilClient(t *testing.T) {
	if RedisPinger(nil) != nil {
		t.Error("a nil client must give a nil pinger (not connected)")
	}
}
