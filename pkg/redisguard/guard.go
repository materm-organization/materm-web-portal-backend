// Package redisguard makes a Redis outage cheap (B1 of the first cross-system run): a circuit breaker on the
// go-redis client, as a client hook, so that EVERY user of the one shared client (the response cache, the rate
// limiters, the IP salt, the duplicate marker, the preview cache, the readiness ping) takes the same decision.
//
// While Redis is unhealthy a command fails at once with ErrOpen instead of dialling: the callers already treat
// any Redis error as "answer from the database" (publiccache), "fail open" (preview limiter) or "fail closed"
// (form limiter), so an outage costs microseconds per request, not a dial timeout. After a cool-down ONE
// request is let through as a probe; its outcome closes the breaker (Redis is back) or opens it again.
//
// What counts as Redis being unhealthy: the command could not reach the server or got no answer in time (dial
// refused or timed out, connection reset, read/write timeout, EOF) and the server's own "I am not serving"
// replies (LOADING, MASTERDOWN, CLUSTERDOWN, BUSY). What does not: redis.Nil (a cache miss), any other server
// reply (NOSCRIPT, WRONGTYPE: Redis answered), an error caused by the caller's own context (cancelled client,
// request deadline) and a pool wait timeout (the process is saturated, not Redis).
//
// Epoch: every time the breaker closes after having been open, Epoch() grows. Pub/sub does not replay what was
// published while Redis was away, so the response cache flushes itself when it sees a new epoch (see
// publiccache): nothing cached before an outage is served after it.
package redisguard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// ErrOpen is returned (without touching the network) while the breaker is open or a probe is in flight.
var ErrOpen = errors.New("redisguard: redis unavailable, circuit open")

// DefaultCooldown is how long the breaker stays open before one probe is allowed.
const DefaultCooldown = 3 * time.Second

// probeLimit bounds how long a probe may hold the half-open slot (the client timeouts end it long before).
const probeLimit = 10 * time.Second

type state int

const (
	closed state = iota
	open
)

// Guard is the circuit breaker. The zero value is not usable: call New.
type Guard struct {
	cooldown time.Duration
	now      func() time.Time

	mu         sync.Mutex
	st         state
	openUntil  time.Time
	probing    bool
	probeStart time.Time

	epoch atomic.Uint64
	trips atomic.Uint64
	skips atomic.Uint64
}

// Option configures New.
type Option func(*Guard)

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option { return func(g *Guard) { g.now = now } }

// New builds a closed breaker. cooldown <= 0 means DefaultCooldown.
func New(cooldown time.Duration, opts ...Option) *Guard {
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	g := &Guard{cooldown: cooldown, now: time.Now}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Epoch counts the open -> closed transitions (Redis came back after an outage).
func (g *Guard) Epoch() uint64 {
	if g == nil {
		return 0
	}
	return g.epoch.Load()
}

// Trips counts how often the breaker opened; Skipped how many commands it refused without a network call.
func (g *Guard) Trips() uint64 { return g.trips.Load() }

// Skipped is the number of commands refused with ErrOpen.
func (g *Guard) Skipped() uint64 { return g.skips.Load() }

// Open reports whether commands are currently being refused (open, probe not yet due or in flight).
func (g *Guard) Open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.st == open
}

type outcome int

const (
	success outcome = iota
	failure
	neutral
)

// admit decides whether a command may go to Redis. probe is true for the one request that tests the waters.
func (g *Guard) admit() (probe bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.st == closed {
		return false, nil
	}
	now := g.now()
	if g.probing && now.Sub(g.probeStart) > probeLimit {
		g.probing = false // a probe that never reported must not block recovery forever
	}
	if now.Before(g.openUntil) || g.probing {
		g.skips.Add(1)
		return false, ErrOpen
	}
	g.probing = true
	g.probeStart = now
	return true, nil
}

// report records what happened to a command that was admitted (or that bypassed the gate).
func (g *Guard) report(probe bool, o outcome, cause error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if probe {
		g.probing = false
	}
	switch o {
	case success:
		if g.st == open {
			g.st = closed
			g.epoch.Add(1)
			logs.Info("redis recovered: circuit closed, the response cache will be flushed once")
		}
	case failure:
		switch {
		case g.st == closed:
			g.st = open
			g.openUntil = g.now().Add(g.cooldown)
			g.trips.Add(1)
			logs.WarnEvery("redisguard.open", time.Minute, "redis unavailable: circuit open, requests are answered from the database",
				logs.Duration("cooldown", g.cooldown), logs.Err(cause))
		case probe:
			g.openUntil = g.now().Add(g.cooldown) // the probe failed: wait another cool-down
		}
	case neutral:
		// nothing learned (the caller gave up first): leave the state alone
	}
}

// classify turns a command error into an outcome.
func classify(ctx context.Context, err error) outcome {
	if err == nil || errors.Is(err, redis.Nil) {
		return success
	}
	if ctx != nil && ctx.Err() != nil {
		return neutral // the caller's own deadline or cancellation, not evidence about Redis
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, redis.ErrPoolTimeout) {
		return neutral
	}
	var re redis.Error
	if errors.As(err, &re) {
		msg := err.Error()
		for _, p := range [...]string{"LOADING", "MASTERDOWN", "CLUSTERDOWN", "BUSY", "TRYAGAIN"} {
			if strings.HasPrefix(msg, p) {
				return failure
			}
		}
		return success // Redis answered
	}
	return failure
}

type bypassKey struct{}

// WithBypass marks ctx so that the hook lets the command through even while the breaker is open (its result
// still feeds the breaker). Used for the work that must reach Redis the moment it is back: the subscriber's
// generation bump after a resubscribe.
func WithBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, true)
}

func bypassed(ctx context.Context) bool {
	v, _ := ctx.Value(bypassKey{}).(bool)
	return v
}

// Hook returns the go-redis hook. Pub/sub connections are not commands and are not gated: the subscriber has its
// own reconnect loop.
func (g *Guard) Hook() redis.Hook { return hook{g: g} }

type hook struct{ g *Guard }

func (hook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if inProbe(ctx) {
			return next(ctx, cmd) // part of the probe (see probeKey): the probe reports for it
		}
		probe, err := h.admit(ctx)
		if err != nil {
			cmd.SetErr(err)
			return err
		}
		if probe {
			ctx = context.WithValue(ctx, probeKey{}, true)
		}
		err = next(ctx, cmd)
		h.g.report(probe, classify(ctx, err), err)
		return err
	}
}

func (h hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if inProbe(ctx) {
			return next(ctx, cmds)
		}
		probe, err := h.admit(ctx)
		if err != nil {
			for _, c := range cmds {
				c.SetErr(err)
			}
			return err
		}
		if probe {
			ctx = context.WithValue(ctx, probeKey{}, true)
		}
		err = next(ctx, cmds)
		h.g.report(probe, classify(ctx, err), err)
		return err
	}
}

func (h hook) admit(ctx context.Context) (probe bool, err error) {
	if bypassed(ctx) {
		return false, nil
	}
	return h.g.admit()
}

// probeKey marks the context of the probe. go-redis runs the handshake of a NEW connection (AUTH, HELLO, SELECT,
// CLIENT SETINFO) through the same hooks with the same context, so without this mark the probe's own handshake
// would be refused by the breaker the probe is trying to close.
type probeKey struct{}

func inProbe(ctx context.Context) bool {
	v, _ := ctx.Value(probeKey{}).(bool)
	return v
}

var (
	registryMu sync.RWMutex
	registry   = map[any]*Guard{}
)

// Attach installs a new Guard on client and remembers it, so that code that only holds the client (the cache)
// can find the breaker with Of. cooldown <= 0 means DefaultCooldown.
func Attach(client *redis.Client, cooldown time.Duration, opts ...Option) *Guard {
	g := New(cooldown, opts...)
	client.AddHook(g.Hook())
	registryMu.Lock()
	registry[client] = g
	registryMu.Unlock()
	return g
}

// Of returns the Guard attached to c (a *redis.Client, possibly wrapped in the redis.Cmdable interface), or nil.
func Of(c redis.Cmdable) *Guard {
	rc, ok := c.(*redis.Client)
	if !ok || rc == nil {
		return nil
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[rc]
}

// Forget drops the registry entry of a closed client (tests and Close paths; the map would otherwise keep it).
func Forget(client *redis.Client) {
	registryMu.Lock()
	delete(registry, client)
	registryMu.Unlock()
}
