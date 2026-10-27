package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

// Plan is one submission's rate-limit question.
type Plan struct {
	Slug string
	// IPShort is iphash.Sum.Short(): the salted hash of the visitor, the only form in which an address is a
	// key. KeyID names the portal key that made the call (see KeyID in the controller).
	IPShort string
	KeyID   string
	// WindowMax per Window (the form's rate_limit_max / rate_limit_window_s) and DailyMax per UTC day
	// (rate_limit_daily_max) for ONE visitor.
	WindowMax int
	Window    time.Duration
	DailyMax  int
}

// Limits enforces the submission limits of doc/08 sections 6.3 and 2.10 and doc/10 R-020:
//
//  1. an aggregate cap per portal key (per minute and per day, all visitors together). A leaked key lets an
//     attacker send a random X-Client-IP with every call, which makes every per-visitor counter useless;
//     this cap bounds what such a flood can store;
//  2. per visitor (salted IP hash) and form: a window counter (rate_limit_max per rate_limit_window_s) and a
//     daily counter (rate_limit_daily_max).
//
// Every counter is a Redis fixed window (pkg/ratelimit). Redis failing - or not being configured - is NOT
// "allow": Check returns 503, because a submission is a write and the limits are the only thing standing
// between the form and a flood (this differs from doc/08 section 6.3, which proposed an in-memory bucket).
type Limits struct {
	limiter ratelimit.Limiter
	keys    rediskeys.Keys
	now     func() time.Time

	keyPerMinute int
	keyPerDay    int
}

// dailyTTL outlives the UTC day a daily key names, so the counter is never cut short; the key itself changes
// at midnight.
const dailyTTL = 25 * time.Hour

// NewLimits builds the limiter. perMinute / perDay are the aggregate caps per portal key.
func NewLimits(l ratelimit.Limiter, keys rediskeys.Keys, now func() time.Time, perMinute, perDay int) *Limits {
	if now == nil {
		now = time.Now
	}
	return &Limits{limiter: l, keys: keys, now: now, keyPerMinute: perMinute, keyPerDay: perDay}
}

// Check counts the submission and returns nil when it may proceed, a 429 (Retry-After = the longest wait of
// the limits that refused) or a 503.
func (l *Limits) Check(ctx context.Context, p Plan) error {
	now := l.now().UTC()
	unavailable := func(err error) error {
		logs.WarnEvery("forms.limits", time.Minute, "forms: rate limiter unavailable, submission refused")
		return apperror.ServiceUnavailable(err, 5*time.Second)
	}

	// 1. aggregate per portal key: stops at the first refusal
	for _, c := range []struct {
		key    string
		limit  int
		window time.Duration
	}{
		{l.aggregateKey(p.KeyID, "m:"+now.Format("200601021504")), l.keyPerMinute, time.Minute},
		{l.aggregateKey(p.KeyID, "d:"+now.Format("20060102")), l.keyPerDay, dailyTTL},
	} {
		res, err := l.limiter.Hit(ctx, c.key, c.limit, c.window)
		if err != nil {
			return unavailable(err)
		}
		if !res.Allowed {
			return apperror.RateLimited(res.RetryAfter)
		}
	}

	// 2. per visitor: both counters are always counted, the answer names the longest wait
	var wait time.Duration
	windowSecs := int64(p.Window / time.Second)
	if windowSecs < 1 {
		windowSecs = 1
	}
	start := time.Unix(now.Unix()/windowSecs*windowSecs, 0)
	res, err := l.limiter.Hit(ctx, l.keys.FormWindowRL(p.Slug, p.IPShort, start), p.WindowMax, time.Duration(windowSecs)*time.Second)
	if err != nil {
		return unavailable(err)
	}
	if !res.Allowed {
		// the key names the aligned window: the wait ends when that window does, even if the counter's own TTL
		// (armed at its first hit) is longer
		wait = res.RetryAfter
		if end := start.Add(time.Duration(windowSecs) * time.Second).Sub(now); end > 0 && end < wait {
			wait = end
		}
	}
	res, err = l.limiter.Hit(ctx, l.keys.FormDailyRL(p.Slug, p.IPShort, now), p.DailyMax, dailyTTL)
	if err != nil {
		return unavailable(err)
	}
	if !res.Allowed {
		// the key is a UTC day: the wait ends at midnight, not when the 25 h cleanup TTL does
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		if d := midnight.Sub(now); d < res.RetryAfter || res.RetryAfter <= 0 {
			res.RetryAfter = d
		}
		if res.RetryAfter > wait {
			wait = res.RetryAfter
		}
	}
	if wait > 0 {
		return apperror.RateLimited(wait)
	}
	return nil
}

func (l *Limits) aggregateKey(keyID, tail string) string {
	return l.keys.Prefix() + "rl:form-key:" + keyID + ":" + tail
}

// Dedupe makes an identical retry harmless. forms_submit_form does not de-duplicate (every call inserts), so
// a double click or a retried request would store the message twice: before the insert the service claims a
// short-lived marker for (visitor, form, content); a second identical submission inside the TTL finds it and
// is answered 200 without a second insert. The marker is released when the insert fails, so a retry after an
// error still works. The key carries only hashes.
type Dedupe struct {
	client redis.Cmdable
	keys   rediskeys.Keys
	ttl    time.Duration
}

// NewDedupe builds the guard. client may be nil: Claim then fails (the submission is refused with 503 first,
// by Limits).
func NewDedupe(client redis.Cmdable, keys rediskeys.Keys, ttl time.Duration) *Dedupe {
	if c, ok := client.(*redis.Client); ok && c == nil {
		client = nil // a nil *redis.Client in the interface is not a nil interface
	}
	return &Dedupe{client: client, keys: keys, ttl: ttl}
}

// Key builds the marker key from the form, the visitor hash and the canonical content.
func (d *Dedupe) Key(slug, ipShort string, canonicalData []byte) string {
	sum := sha256.Sum256(append([]byte(ipShort+"|"+slug+"|"), canonicalData...))
	return d.keys.Prefix() + "rl:form:" + slug + ":" + ipShort + ":dup:" + hex.EncodeToString(sum[:])[:32]
}

// Claim returns true for the first caller of key and false for a duplicate inside the TTL.
func (d *Dedupe) Claim(ctx context.Context, key string) (bool, error) {
	if d.client == nil {
		return false, ratelimit.ErrUnavailable
	}
	ok, err := d.client.SetNX(ctx, key, strconv.FormatInt(time.Now().Unix(), 10), d.ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Release removes the marker (the insert failed). Errors are ignored: the marker expires by itself.
func (d *Dedupe) Release(ctx context.Context, key string) {
	if d.client == nil {
		return
	}
	if err := d.client.Del(context.WithoutCancel(ctx), key).Err(); err != nil && !errors.Is(err, redis.Nil) {
		logs.WarnEvery("forms.dedupe", time.Minute, "forms: could not release a duplicate marker")
	}
}
