package redisguard

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newGuard(t *testing.T, cool time.Duration) (*Guard, *clock) {
	t.Helper()
	ck := &clock{t: time.Unix(1_700_000_000, 0)}
	return New(cool, WithClock(ck.now)), ck
}

// run sends one fake command through the hook; next returns result and counts the calls that reached "Redis".
func run(ctx context.Context, g *Guard, calls *atomic.Int64, result error) error {
	h := g.Hook().ProcessHook(func(_ context.Context, _ redis.Cmder) error {
		calls.Add(1)
		return result
	})
	return h(ctx, redis.NewStringCmd(ctx, "get", "k"))
}

type netErr struct{}

func (netErr) Error() string   { return "i/o timeout" }
func (netErr) Timeout() bool   { return true }
func (netErr) Temporary() bool { return true }

var errDial = &net.OpError{Op: "dial", Err: errors.New("connection refused")}

func TestClassify(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		ctx  context.Context
		err  error
		want outcome
	}{
		"nil":                 {context.Background(), nil, success},
		"cache miss":          {context.Background(), redis.Nil, success},
		"wrapped miss":        {context.Background(), errors.Join(errors.New("x"), redis.Nil), success},
		"NOSCRIPT is a reply": {context.Background(), redisError("NOSCRIPT No matching script"), success},
		"WRONGTYPE":           {context.Background(), redisError("WRONGTYPE bad"), success},
		"LOADING":             {context.Background(), redisError("LOADING Redis is loading"), failure},
		"BUSY":                {context.Background(), redisError("BUSY script running"), failure},
		"dial refused":        {context.Background(), errDial, failure},
		"read timeout":        {context.Background(), netErr{}, failure},
		"EOF":                 {context.Background(), errors.New("EOF"), failure},
		"pool timeout":        {context.Background(), redis.ErrPoolTimeout, neutral},
		"caller cancelled":    {canceled, context.Canceled, neutral},
		"cancel w/o ctx":      {context.Background(), context.Canceled, neutral},
		"caller deadline":     {canceled, errDial, neutral},
	} {
		if got := classify(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: classify = %v, want %v", name, got, tc.want)
		}
	}
}

type redisError string

func (e redisError) Error() string { return string(e) }
func (redisError) RedisError()     {}

func TestOpensOnFirstTransportFailureAndSkipsTheNetwork(t *testing.T) {
	g, _ := newGuard(t, 3*time.Second)
	var calls atomic.Int64
	ctx := context.Background()
	if err := run(ctx, g, &calls, errDial); !errors.Is(err, errDial) {
		t.Fatalf("first call returns the real error: %v", err)
	}
	if !g.Open() || g.Trips() != 1 {
		t.Fatalf("open=%v trips=%d", g.Open(), g.Trips())
	}
	for i := 0; i < 50; i++ {
		if err := run(ctx, g, &calls, nil); !errors.Is(err, ErrOpen) {
			t.Fatalf("call %d: %v, want ErrOpen", i, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d calls reached Redis, want 1 (only the failing one)", calls.Load())
	}
	if g.Skipped() != 50 {
		t.Errorf("skipped = %d", g.Skipped())
	}
}

func TestMissesAndServerRepliesNeverTrip(t *testing.T) {
	g, _ := newGuard(t, time.Second)
	var calls atomic.Int64
	for i := 0; i < 20; i++ {
		_ = run(context.Background(), g, &calls, redis.Nil)
		_ = run(context.Background(), g, &calls, redisError("NOSCRIPT x"))
	}
	if g.Open() {
		t.Fatal("a miss or a server reply must not open the breaker")
	}
}

func TestCallerCancellationNeverTrips(t *testing.T) {
	g, _ := newGuard(t, time.Second)
	var calls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = run(ctx, g, &calls, context.Canceled)
	_ = run(context.Background(), g, &calls, redis.ErrPoolTimeout)
	if g.Open() {
		t.Fatal("neutral outcomes must not open the breaker")
	}
}

func TestSingleProbeAfterCooldownAndRecovery(t *testing.T) {
	g, ck := newGuard(t, 3*time.Second)
	var calls atomic.Int64
	ctx := context.Background()
	_ = run(ctx, g, &calls, errDial) // opens
	ck.add(2 * time.Second)
	if err := run(ctx, g, &calls, nil); !errors.Is(err, ErrOpen) {
		t.Fatalf("before the cool-down ends: %v", err)
	}
	ck.add(1100 * time.Millisecond)

	// the probe is held in flight while others arrive: they are refused, exactly one reaches "Redis"
	release := make(chan struct{})
	entered := make(chan struct{})
	probeDone := make(chan error, 1)
	go func() {
		h := g.Hook().ProcessHook(func(context.Context, redis.Cmder) error {
			close(entered)
			<-release
			return nil
		})
		probeDone <- h(ctx, redis.NewStringCmd(ctx, "get", "k"))
	}()
	<-entered
	for i := 0; i < 10; i++ {
		if err := run(ctx, g, &calls, nil); !errors.Is(err, ErrOpen) {
			t.Fatalf("while the probe is in flight: %v", err)
		}
	}
	before := g.Epoch()
	close(release)
	if err := <-probeDone; err != nil {
		t.Fatal(err)
	}
	if g.Open() || g.Epoch() != before+1 {
		t.Fatalf("after a successful probe: open=%v epoch %d -> %d", g.Open(), before, g.Epoch())
	}
	if err := run(ctx, g, &calls, nil); err != nil {
		t.Fatalf("closed again: %v", err)
	}
	if g.Epoch() != before+1 {
		t.Error("epoch must grow only on open -> closed")
	}
}

func TestFailedProbeReopensForAnotherCooldown(t *testing.T) {
	g, ck := newGuard(t, 3*time.Second)
	var calls atomic.Int64
	ctx := context.Background()
	_ = run(ctx, g, &calls, errDial)
	ck.add(3100 * time.Millisecond)
	if err := run(ctx, g, &calls, errDial); !errors.Is(err, errDial) {
		t.Fatalf("the probe reaches Redis and fails for real: %v", err)
	}
	if err := run(ctx, g, &calls, nil); !errors.Is(err, ErrOpen) {
		t.Fatalf("after a failed probe: %v", err)
	}
	if g.Epoch() != 0 {
		t.Error("no recovery happened")
	}
	ck.add(3100 * time.Millisecond)
	if err := run(ctx, g, &calls, nil); err != nil {
		t.Fatalf("second probe succeeds: %v", err)
	}
	if g.Epoch() != 1 || g.Open() {
		t.Fatalf("epoch=%d open=%v", g.Epoch(), g.Open())
	}
}

func TestNeutralProbeFreesTheSlot(t *testing.T) {
	g, ck := newGuard(t, time.Second)
	var calls atomic.Int64
	_ = run(context.Background(), g, &calls, errDial)
	ck.add(1100 * time.Millisecond)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = run(cctx, g, &calls, context.Canceled) // the probe's caller went away
	if err := run(context.Background(), g, &calls, nil); err != nil {
		t.Fatalf("next request must become the probe: %v", err)
	}
}

func TestStuckProbeDoesNotBlockForever(t *testing.T) {
	g, ck := newGuard(t, time.Second)
	_ = run(context.Background(), g, new(atomic.Int64), errDial)
	ck.add(1100 * time.Millisecond)
	if probe, err := g.admit(); !probe || err != nil {
		t.Fatalf("probe slot: %v %v", probe, err)
	}
	if _, err := g.admit(); !errors.Is(err, ErrOpen) {
		t.Fatal("second caller must wait")
	}
	ck.add(probeLimit + time.Second) // the probe never reported
	if probe, err := g.admit(); !probe || err != nil {
		t.Fatalf("a new probe must be allowed: %v %v", probe, err)
	}
}

func TestBypassReachesRedisWhileOpenAndClosesOnSuccess(t *testing.T) {
	g, _ := newGuard(t, time.Minute)
	var calls atomic.Int64
	_ = run(context.Background(), g, &calls, errDial)
	if err := run(WithBypass(context.Background()), g, &calls, nil); err != nil {
		t.Fatalf("bypass call: %v", err)
	}
	if g.Open() || g.Epoch() != 1 {
		t.Fatalf("a bypassed success proves Redis is back: open=%v epoch=%d", g.Open(), g.Epoch())
	}
}

func TestBypassFailureWhileOpenKeepsTheCooldown(t *testing.T) {
	g, ck := newGuard(t, 3*time.Second)
	var calls atomic.Int64
	_ = run(context.Background(), g, &calls, errDial)
	ck.add(2 * time.Second)
	_ = run(WithBypass(context.Background()), g, &calls, errDial)
	ck.add(1100 * time.Millisecond) // 3.1 s after the trip
	if err := run(context.Background(), g, &calls, nil); err != nil {
		t.Fatalf("the original cool-down applies, the probe is due: %v", err)
	}
}

func TestPipelineIsRefusedWithErrOnEveryCommand(t *testing.T) {
	g, _ := newGuard(t, time.Minute)
	ctx := context.Background()
	_ = run(ctx, g, new(atomic.Int64), errDial)
	var reached atomic.Int64
	h := g.Hook().ProcessPipelineHook(func(context.Context, []redis.Cmder) error { reached.Add(1); return nil })
	cmds := []redis.Cmder{redis.NewStringCmd(ctx, "get", "a"), redis.NewStringCmd(ctx, "get", "b")}
	if err := h(ctx, cmds); !errors.Is(err, ErrOpen) {
		t.Fatalf("pipeline: %v", err)
	}
	for i, c := range cmds {
		if !errors.Is(c.Err(), ErrOpen) {
			t.Errorf("cmd %d error = %v", i, c.Err())
		}
	}
	if reached.Load() != 0 {
		t.Error("the pipeline must not reach Redis")
	}
}

func TestNilGuardEpochIsZero(t *testing.T) {
	var g *Guard
	if g.Epoch() != 0 {
		t.Fatal("nil guard")
	}
}

// ---- real client against a listener that accepts and never answers (read timeout) and a closed port (refused)

func silentListener(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})
	return l.Addr().String()
}

func fastClient(addr string) (*redis.Client, *Guard) {
	c := redis.NewClient(&redis.Options{
		Addr: addr, DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond,
		MaxRetries: -1, DialerRetries: 1, DialerRetryTimeout: time.Millisecond, PoolTimeout: 100 * time.Millisecond,
	})
	return c, Attach(c, 2*time.Second)
}

func TestSilentServerCostsOneReadTimeoutThenNothing(t *testing.T) {
	c, g := fastClient(silentListener(t))
	t.Cleanup(func() { Forget(c); _ = c.Close() })
	ctx := context.Background()

	t0 := time.Now()
	err := c.Get(ctx, "k").Err()
	first := time.Since(t0)
	if err == nil {
		t.Fatal("a silent server must time out")
	}
	if first > 400*time.Millisecond {
		t.Errorf("first call took %v, want about one read timeout (100ms)", first)
	}
	if !g.Open() {
		t.Fatal("the timeout must open the breaker")
	}
	t0 = time.Now()
	for i := 0; i < 1000; i++ {
		if err = c.Get(ctx, "k").Err(); !errors.Is(err, ErrOpen) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Errorf("1000 refused calls took %v", d)
	}
}

func TestRefusedConnectionFailsInOneAttempt(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens there any more
	c, g := fastClient(addr)
	t.Cleanup(func() { Forget(c); _ = c.Close() })
	t0 := time.Now()
	if err = c.Get(context.Background(), "k").Err(); err == nil {
		t.Fatal("must fail")
	}
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Errorf("a refused connection took %v: one attempt, no 100ms back-off loop", d)
	}
	if !g.Open() {
		t.Error("breaker must be open")
	}
	if Of(c) != g || Of(nil) != nil {
		t.Error("Of must find the attached guard and tolerate nil")
	}
}

func TestLibraryLoggerIsThrottledPerKind(t *testing.T) {
	var buf bytes.Buffer
	restore := logs.Capture(&buf)
	defer restore()
	l := throttledLogger{}
	for i := 0; i < 200; i++ {
		l.Printf(context.Background(), "redis: connection pool: failed to dial after %d attempts: %v", 1, "dial tcp 127.0.0.1:1: connect: connection refused")
	}
	l.Printf(context.Background(), "redis: another kind of message %d", 7)
	lines := strings.Count(buf.String(), "\n")
	if lines > 2 {
		t.Fatalf("%d lines, want at most one per kind (2):\n%s", lines, buf.String())
	}
}

// go-redis runs the handshake of a new connection through the hooks with the probe's own context: it must not be
// refused by the breaker whose probe it belongs to, and the probe alone reports.
func TestHandshakeInsideTheProbeIsNotRefused(t *testing.T) {
	g, ck := newGuard(t, time.Second)
	_ = run(context.Background(), g, new(atomic.Int64), errDial)
	ck.add(1100 * time.Millisecond)
	var inner error
	h := g.Hook().ProcessHook(func(ctx context.Context, _ redis.Cmder) error {
		inner = g.Hook().ProcessHook(func(context.Context, redis.Cmder) error { return nil })(ctx, redis.NewStatusCmd(ctx, "auth", "x"))
		return nil
	})
	ctx := context.Background()
	if err := h(ctx, redis.NewStringCmd(ctx, "get", "k")); err != nil || inner != nil {
		t.Fatalf("probe=%v handshake=%v", err, inner)
	}
	if g.Open() || g.Epoch() != 1 {
		t.Fatalf("open=%v epoch=%d", g.Open(), g.Epoch())
	}
}
