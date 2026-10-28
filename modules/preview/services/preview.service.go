// Package services is the logic of the preview module (P-19): rate limit, exchange the token for its frozen
// snapshot (cms_consume_preview_token) and render the snapshot through the assembler of P-06.
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/preview/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/preview/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

// EntityPages is the entity_type of a page revision. Every other type is answered like an unusable token
// until its adapter exists (posts: M5).
const EntityPages = "cms.pages"

// InvalidToken is the ONE answer for a token that cannot be used: unknown, expired, revoked, used up, bound
// to another path or to an entity this portal cannot preview. 401 INVALID_TOKEN (doc/08 C-24; the admin's
// 400 INVALID_TOKEN is for reset/invite links). pkg/apperror.InvalidToken() is the 400 one and stays untouched
// (the package is shared with the admin backend).
func InvalidToken() *apperror.Error {
	return &apperror.Error{Status: http.StatusUnauthorized, Code: apperror.CodeInvalidToken}
}

// Options carries the numbers of the preview.* config keys. Zero values take the defaults.
type Options struct {
	// CacheTTL keeps the consumed snapshot in Redis (default 10 s, doc/08 section 3.9). It saves database
	// writes (the function counts a use per call) and is NOT meant to show newer content.
	CacheTTL time.Duration
	// TokenPerMinute is the ceiling per token (60, doc/08 section 2.10); KeyPerMinute the ceiling per portal
	// key over all tokens (300).
	TokenPerMinute int
	KeyPerMinute   int
	Now            func() time.Time
}

func (o Options) withDefaults() Options {
	if o.TokenPerMinute <= 0 {
		o.TokenPerMinute = 60
	}
	if o.KeyPerMinute <= 0 {
		o.KeyPerMinute = 300
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Input is a parsed P-19 call. The token never leaves this struct except as a hash.
type Input struct {
	Token string
	// Path is the optional ?path: when set it must equal the path of the previewed page.
	Path string
	// QueryLocale is a well-formed ?locale ("" = none); AcceptLanguage the header.
	QueryLocale    string
	AcceptLanguage string
	// KeyID names the portal key (a truncated hash) for the aggregate limit.
	KeyID string
}

// Result is the answer and the locale negotiation (for Content-Language / Vary).
type Result struct {
	Data *models.PreviewData
	Neg  localize.Negotiation
}

// PreviewService serves P-19.
type PreviewService interface {
	Resolve(ctx context.Context, in Input) (*Result, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Tokens    repositories.TokenRepository
	Assembler *assemble.Assembler
	Chains    readkit.ChainSource
	// Redis holds the snapshot cache; nil = no cache (every call consumes the token).
	Redis redis.Cmdable
	// Limiter may be nil (no limit). A limiter that fails lets the call through: this is a read that needs a
	// secret token, unlike the form write, which fails closed.
	Limiter ratelimit.Limiter
	Keys    rediskeys.Keys
	Options Options
}

type previewService struct {
	d Deps
	o Options
}

// NewPreviewService builds the service.
func NewPreviewService(d Deps) PreviewService {
	return &previewService{d: d, o: d.Options.withDefaults()}
}

// cached is what is kept in Redis under preview:<sha256>: the function's answer for a USABLE token (never a
// negative answer).
type cached struct {
	EntityType string          `json:"t"`
	EntityID   int64           `json:"i"`
	Locale     *string         `json:"l"`
	Snapshot   json.RawMessage `json:"s"`
}

func (s *previewService) Resolve(ctx context.Context, in Input) (*Result, error) {
	sum := sha256.Sum256([]byte(in.Token))
	full := hex.EncodeToString(sum[:])

	if err := s.limit(ctx, full[:32], in.KeyID); err != nil {
		return nil, err
	}
	tok, err := s.consume(ctx, sum[:], full)
	if err != nil {
		return nil, err
	}
	if tok.EntityType != EntityPages {
		// TODO(M5): cms.posts. Logged by type only (never the token).
		logs.WarnEvery("preview.entity."+tok.EntityType, time.Minute, "preview: entity type has no adapter yet",
			logs.String("entity_type", tok.EntityType))
		return nil, InvalidToken()
	}
	raw, err := assemble.ParsePageSnapshot(tok.Snapshot)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if in.Path != "" && in.Path != raw.Page.Path {
		return nil, InvalidToken() // the token is for another page
	}

	neg, err := s.negotiate(ctx, in, tok.LocaleCode)
	if err != nil {
		return nil, err
	}
	res, err := s.d.Assembler.PreviewPage(ctx, raw, assemble.PreviewOptions{
		EntityID: tok.EntityID, Locale: neg.Code, Chain: neg.Chain, Now: s.o.Now(),
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Neg: neg,
		Data: &models.PreviewData{
			Locale: neg.Code, Kind: models.KindPage, Page: res.Data,
			Preview: models.Info{EntityType: tok.EntityType, EntityID: tok.EntityID, Locale: neg.Code, Warnings: res.Warnings},
		},
	}, nil
}

// negotiate: an explicit ?locale wins, then the locale the link was issued for, then Accept-Language, then the
// default.
func (s *previewService) negotiate(ctx context.Context, in Input, tokenLocale *string) (localize.Negotiation, error) {
	chain, err := s.d.Chains.Chain(ctx)
	if err != nil {
		return localize.Negotiation{}, apperror.ServiceUnavailable(err, 0)
	}
	q, accept := in.QueryLocale, in.AcceptLanguage
	if q == "" && tokenLocale != nil && *tokenLocale != "" {
		q = *tokenLocale
	}
	neg, aerr := chain.Negotiate(q, accept)
	if aerr != nil {
		// the issuer's locale is not a valid code any more: fall back to the headers
		neg, aerr = chain.Negotiate("", accept)
		if aerr != nil {
			return localize.Negotiation{}, aerr
		}
	}
	return neg, nil
}

// limit counts the call against the portal key (all tokens) and the token. Redis problems do not block.
func (s *previewService) limit(ctx context.Context, hash32, keyID string) error {
	if s.d.Limiter == nil {
		return nil
	}
	now := s.o.Now().UTC()
	untilNextMinute := now.Truncate(time.Minute).Add(time.Minute).Sub(now)
	for _, c := range []struct {
		key   string
		limit int
	}{
		{s.d.Keys.PreviewKeyRL(keyID, now), s.o.KeyPerMinute},
		{s.d.Keys.PreviewTokenRL(hash32, now), s.o.TokenPerMinute},
	} {
		res, err := s.d.Limiter.Hit(ctx, c.key, c.limit, time.Minute)
		if err != nil {
			// fail open: a read that already needs a secret token (unlike the form write, which fails closed)
			logs.WarnEvery("preview.limiter", time.Minute, "preview: rate limiter unavailable, call let through")
			continue
		}
		if !res.Allowed {
			wait := res.RetryAfter
			if untilNextMinute > 0 && untilNextMinute < wait {
				wait = untilNextMinute
			}
			return apperror.RateLimited(wait)
		}
	}
	return nil
}

// consume returns the frozen revision of a usable token: from the 10 s Redis copy, else from the function.
func (s *previewService) consume(ctx context.Context, hash []byte, full string) (*repositories.Consumed, error) {
	key := s.d.Keys.Preview(full)
	useCache := s.d.Redis != nil && s.o.CacheTTL > 0
	if useCache {
		b, err := s.d.Redis.Get(ctx, key).Bytes()
		switch {
		case err == nil:
			var c cached
			if json.Unmarshal(b, &c) == nil && c.EntityType != "" {
				return &repositories.Consumed{EntityType: c.EntityType, EntityID: c.EntityID, LocaleCode: c.Locale, Snapshot: c.Snapshot}, nil
			}
		case !errors.Is(err, redis.Nil):
			logs.WarnEvery("preview.cache", time.Minute, "preview: snapshot cache unavailable")
		}
	}
	t, err := s.d.Tokens.Consume(ctx, hash)
	if errors.Is(err, repositories.ErrInvalid) {
		return nil, InvalidToken()
	}
	if err != nil {
		return nil, err
	}
	if useCache {
		if b, merr := json.Marshal(cached{EntityType: t.EntityType, EntityID: t.EntityID, Locale: t.LocaleCode, Snapshot: t.Snapshot}); merr == nil {
			if serr := s.d.Redis.Set(ctx, key, b, s.o.CacheTTL).Err(); serr != nil {
				logs.WarnEvery("preview.cache", time.Minute, "preview: snapshot cache unavailable")
			}
		}
	}
	return t, nil
}
