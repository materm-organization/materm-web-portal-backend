package publiccache

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// The invalidation contract with the admin relay (doc/08 section 2.7, doc/10 R-017 / Q-08-16).
//
// Channel: DefaultChannel ("materm:cms.invalidate"; the plain name "cms.invalidate" of the first drafts is
// not used because the Redis is shared and an unprefixed name could be written by anything).
//
// A message is the JSON envelope
//
//	{"payload":"<the event as a JSON string>","sig":"<hex HMAC-SHA256(key, the payload string's bytes)>"}
//
// where the event is
//
//	{"event_id":"<uuid>","event_type":"content.changed","tags":["page:1","pages"],"occurred_at":"<RFC 3339>",
//	 "sent_at":"<RFC 3339>","request_id":"..."}
//
// The key is cache.invalidateKey (the same secret as the revalidate webhook may use). The signature is
// computed and checked over the payload string exactly as transmitted (never over a re-marshalled value).
// Sign produces the envelope for the relay and for tests.
//
// sent_at is the moment the relay PUBLISHes the message (set by Sign when left zero). occurred_at is when the
// outbox row was written; it is informational only, because a relay that was down or is backing off publishes
// events long after they occurred and those must still be applied. Freshness is judged on sent_at.
//
// A message is applied only if: the signature is valid; event_type is "content.changed"; sent_at is present,
// not older than MaxAge (default 10 min) and not more than one minute in the future (a captured valid message
// cannot be replayed later); its event_id was not applied before (a replay inside the window is ignored); and
// only tags of the public vocabulary (ValidTag) are acted on - the five internal tag shapes and anything
// unknown are ignored. In the unsigned dev mode a missing sent_at falls back to occurred_at.
const (
	DefaultChannel = "materm:cms.invalidate"
	EventType      = "content.changed"

	// DefaultMaxAge is how old an event may be.
	DefaultMaxAge = 10 * time.Minute
	// DefaultSecondDelay is the pause before the second delete (doc/08 section 2.7 item 2).
	DefaultSecondDelay = 2 * time.Second

	maxFutureSkew = time.Minute
	maxMessage    = 64 << 10
	maxTags       = 200
	maxSeen       = 10000
)

// Event is the signed payload.
type Event struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Tags       []string  `json:"tags"`
	OccurredAt time.Time `json:"occurred_at"`
	// SentAt is when the relay published the message; freshness (MaxAge) is judged on it.
	SentAt    time.Time `json:"sent_at"`
	RequestID string    `json:"request_id,omitempty"`
}

// Envelope is the message published on the channel.
type Envelope struct {
	Payload string `json:"payload"`
	Sig     string `json:"sig"`
}

// Sign builds the message for ev with key. A zero SentAt is set to the current time.
func Sign(key []byte, ev Event) ([]byte, error) {
	if ev.SentAt.IsZero() {
		ev.SentAt = time.Now()
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Payload: string(payload), Sig: mac(key, payload)})
}

func mac(key, payload []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Errors of Verify.
var (
	ErrMalformed    = errors.New("publiccache: malformed message")
	ErrBadSignature = errors.New("publiccache: bad signature")
	ErrStale        = errors.New("publiccache: event outside the accepted time window")
	ErrNotInterest  = errors.New("publiccache: not an invalidation event")
)

// Verify checks a message and returns its event. With a non-empty key the signature is mandatory. With an
// empty key (only the dev mode of the Subscriber) the signature is not checked and a bare event JSON is also
// accepted. The time window and event type are always checked.
func Verify(msg []byte, key []byte, now time.Time, maxAge time.Duration) (Event, error) {
	if len(msg) == 0 || len(msg) > maxMessage {
		return Event{}, ErrMalformed
	}
	var env Envelope
	payload := []byte(nil)
	if err := json.Unmarshal(msg, &env); err != nil {
		return Event{}, ErrMalformed
	}
	switch {
	case len(key) > 0:
		sig, err := hex.DecodeString(env.Sig)
		if err != nil || env.Payload == "" {
			return Event{}, ErrBadSignature
		}
		payload = []byte(env.Payload)
		want, _ := hex.DecodeString(mac(key, payload))
		if !hmac.Equal(sig, want) {
			return Event{}, ErrBadSignature
		}
	case env.Payload != "":
		payload = []byte(env.Payload)
	default:
		payload = msg // dev, unsigned: the event itself
	}
	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return Event{}, ErrMalformed
	}
	if ev.EventType != EventType {
		return Event{}, ErrNotInterest
	}
	if ev.EventID == "" || len(ev.EventID) > 64 || len(ev.Tags) > maxTags {
		return Event{}, ErrMalformed
	}
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	sent := ev.SentAt
	if sent.IsZero() {
		if len(key) > 0 {
			return Event{}, ErrMalformed // a signed message must say when it was sent
		}
		sent = ev.OccurredAt // dev, unsigned
	}
	if sent.IsZero() || now.Sub(sent) > maxAge || sent.Sub(now) > maxFutureSkew {
		return Event{}, ErrStale
	}
	return ev, nil
}

// PubSubClient is what the Subscriber needs from Redis (*redis.Client satisfies it).
type PubSubClient interface {
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// SubscriberOptions configure a Subscriber.
type SubscriberOptions struct {
	Client PubSubClient
	Cache  *Cache
	// Channel defaults to DefaultChannel. Tests use a random one so that packages sharing the scratch Redis do
	// not see each other's messages.
	Channel string
	// Key is the HMAC key (cache.invalidateKey). Required unless AllowUnsigned.
	Key []byte
	// AllowUnsigned lets the subscriber accept messages without a valid signature. ONLY for dev with a blank
	// key; NewSubscriber refuses a blank Key otherwise.
	AllowUnsigned bool
	MaxAge        time.Duration
	// SecondDelay is the pause before the second delete (default DefaultSecondDelay).
	SecondDelay time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

// Stats counts what the subscriber did (for tests and diagnostics; never logged with message content).
type Stats struct {
	Applied   int64 // events whose tags were invalidated
	Rejected  int64 // bad signature, malformed, stale, replayed
	Ignored   int64 // valid event without any tag of the public vocabulary
	Resyncs   int64 // generation bumps after a (re)subscribe
	TagsFound int64 // valid tags acted on
}

// Subscriber applies invalidation messages to a Cache.
type Subscriber struct {
	o       SubscriberOptions
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	ps      *redis.PubSub
	started atomic.Bool
	closed  atomic.Bool

	applied, rejected, ignored, resyncs, tags atomic.Int64

	mu   sync.Mutex
	seen map[string]time.Time
}

// ErrNoKey is returned by NewSubscriber when no key is given and AllowUnsigned is false.
var ErrNoKey = errors.New("publiccache: cache.invalidateKey is required (the subscriber only runs unsigned in dev)")

// NewSubscriber validates the options.
func NewSubscriber(o SubscriberOptions) (*Subscriber, error) {
	if o.Client == nil || o.Cache == nil {
		return nil, errors.New("publiccache: subscriber needs a Redis client and a cache")
	}
	if len(o.Key) == 0 && !o.AllowUnsigned {
		return nil, ErrNoKey
	}
	if o.Channel == "" {
		o.Channel = DefaultChannel
	}
	if o.MaxAge <= 0 {
		o.MaxAge = DefaultMaxAge
	}
	if o.SecondDelay <= 0 {
		o.SecondDelay = DefaultSecondDelay
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Subscriber{o: o, seen: map[string]time.Time{}}, nil
}

// Start subscribes and returns once Redis confirmed the subscription (so a publisher that runs after Start
// returns is always heard), then handles messages in the background until Close. Every confirmation - the
// first one and each re-subscription after a lost connection - bumps the generation, because pub/sub does not
// replay the messages missed meanwhile (doc/08 section 2.7 item 4). ctx only bounds the subscribe call.
func (s *Subscriber) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("publiccache: subscriber already started")
	}
	ps := s.o.Client.Subscribe(ctx, s.o.Channel)
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	defer rcancel()
	first, err := ps.Receive(rctx)
	if err != nil {
		_ = ps.Close()
		return fmt.Errorf("publiccache: subscribe: %w", err)
	}
	if sub, ok := first.(*redis.Subscription); !ok || sub.Kind != "subscribe" {
		_ = ps.Close()
		return errors.New("publiccache: unexpected reply to subscribe")
	}
	s.ps = ps
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	ch := ps.ChannelWithSubscriptions(redis.WithChannelHealthCheckInterval(10 * time.Second))
	s.resync(runCtx)
	s.wg.Add(1)
	go s.loop(runCtx, ch)
	return nil
}

// Close stops the subscriber: cancels the loop and any pending second delete, closes the subscription and
// waits for the goroutines. It is safe to call twice and before Start.
func (s *Subscriber) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	var err error
	if s.ps != nil && s.closed.CompareAndSwap(false, true) {
		err = s.ps.Close()
	}
	s.wg.Wait()
	return err
}

// Stats returns the counters.
func (s *Subscriber) Stats() Stats {
	return Stats{Applied: s.applied.Load(), Rejected: s.rejected.Load(), Ignored: s.ignored.Load(), Resyncs: s.resyncs.Load(), TagsFound: s.tags.Load()}
}

func (s *Subscriber) resync(ctx context.Context) {
	if err := s.o.Cache.Flush(ctx); err != nil {
		logs.WarnEvery("publiccache.resync", time.Minute, "publiccache: generation bump after subscribe failed", logs.Err(err))
		return
	}
	s.resyncs.Add(1)
}

func (s *Subscriber) loop(ctx context.Context, ch <-chan any) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			switch v := m.(type) {
			case *redis.Subscription:
				if v.Kind == "subscribe" {
					s.resync(ctx)
				}
			case *redis.Message:
				s.handle(ctx, []byte(v.Payload))
			}
		}
	}
}

// handle verifies and applies one message. It never panics on input and never logs the payload.
func (s *Subscriber) handle(ctx context.Context, msg []byte) {
	ev, err := Verify(msg, s.o.Key, s.o.Now(), s.o.MaxAge)
	if err != nil {
		if errors.Is(err, ErrNotInterest) {
			s.ignored.Add(1)
			return
		}
		s.rejected.Add(1)
		logs.WarnEvery("publiccache.reject", time.Minute, "publiccache: invalidation message rejected", logs.String("reason", err.Error()))
		return
	}
	if !s.firstTime(ev.EventID) {
		s.rejected.Add(1)
		logs.WarnEvery("publiccache.replay", time.Minute, "publiccache: invalidation event seen before, ignored")
		return
	}
	tags, _ := FilterTags(ev.Tags)
	if len(tags) == 0 {
		s.ignored.Add(1)
		return
	}
	s.tags.Add(int64(len(tags)))
	if _, err := s.o.Cache.Invalidate(ctx, tags); err != nil {
		logs.Error("publiccache: invalidation failed", logs.Err(err))
	}
	s.applied.Add(1)
	s.wg.Add(1)
	go func() { // the second delete
		defer s.wg.Done()
		t := time.NewTimer(s.o.SecondDelay)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			if _, err := s.o.Cache.Invalidate(ctx, tags); err != nil {
				logs.Error("publiccache: second invalidation failed", logs.Err(err))
			}
		}
	}()
}

// firstTime records an event id; false when it was applied within the window already.
func (s *Subscriber) firstTime(id string) bool {
	now := s.o.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) >= maxSeen {
		for k, t := range s.seen {
			if now.Sub(t) > s.o.MaxAge {
				delete(s.seen, k)
			}
		}
		if len(s.seen) >= maxSeen { // still full: forget an arbitrary half rather than grow
			i := 0
			for k := range s.seen {
				delete(s.seen, k)
				if i++; i >= maxSeen/2 {
					break
				}
			}
		}
	}
	if t, dup := s.seen[id]; dup && now.Sub(t) <= s.o.MaxAge {
		return false
	}
	s.seen[id] = now
	return true
}
