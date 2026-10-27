package localize

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// FetchFunc loads the active locales (v_public_locales), typically through the response cache.
type FetchFunc func(ctx context.Context) ([]Locale, error)

// DefaultProviderTTL is how long a Provider keeps its Chain in the process (doc/08 section 2.4: 60 s).
const DefaultProviderTTL = 60 * time.Second

// Provider hands out the current Chain. It keeps the Chain in the process for ttl, shares concurrent
// refreshes (singleflight), and when a refresh fails it keeps serving the last good Chain (logged, rate
// limited) rather than failing every request; only a Provider that never loaded anything returns the error.
type Provider struct {
	fetch  FetchFunc
	ttl    time.Duration
	now    func() time.Time
	flight singleflight.Group

	mu  sync.RWMutex
	cur *Chain
	exp time.Time
}

// NewProvider builds a Provider. ttl <= 0 means DefaultProviderTTL.
func NewProvider(fetch FetchFunc, ttl time.Duration) *Provider {
	if ttl <= 0 {
		ttl = DefaultProviderTTL
	}
	return &Provider{fetch: fetch, ttl: ttl, now: time.Now}
}

// Chain returns the current Chain.
func (p *Provider) Chain(ctx context.Context) (*Chain, error) {
	p.mu.RLock()
	cur, exp := p.cur, p.exp
	p.mu.RUnlock()
	if cur != nil && p.now().Before(exp) {
		return cur, nil
	}
	v, err, _ := p.flight.Do("chain", func() (any, error) {
		locales, ferr := p.fetch(ctx)
		if ferr != nil {
			return nil, ferr
		}
		c, cerr := NewChain(locales)
		if cerr != nil {
			return nil, cerr
		}
		p.mu.Lock()
		p.cur, p.exp = c, p.now().Add(p.ttl)
		p.mu.Unlock()
		return c, nil
	})
	if err != nil {
		if cur != nil {
			logs.WarnEvery("localize.stale", time.Minute, "localize: reloading the locales failed, serving the previous chain", logs.Err(err))
			return cur, nil
		}
		return nil, err
	}
	return v.(*Chain), nil
}

// Invalidate drops the process copy so the next call reloads (used when a `locales` invalidation arrives
// in-process; otherwise the TTL applies).
func (p *Provider) Invalidate() {
	p.mu.Lock()
	p.exp = time.Time{}
	p.mu.Unlock()
}
