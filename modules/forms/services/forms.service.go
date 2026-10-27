// Package services holds the logic of the forms module: the form definition (P-14) and the submission
// (P-15, the one write path of the portal).
package services

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	contentrepositories "github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
	formsrepositories "github.com/materm-organization/materm-web-portal-backend/modules/forms/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/validate"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/iphash"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

// Fallbacks and bounds that the database does not provide (the view columns can be NULL, and the config keys
// of doc/08 section 2.9 are the second source).
const (
	// defaultWindowMax / defaultWindow are the seeded rate_limit_max / rate_limit_window_s.
	defaultWindowMax = 5
	defaultWindow    = 600 * time.Second
	// defaultMaxPayload is the seeded max_payload_bytes.
	defaultMaxPayload = 16384
	// envelopeSlack is what the body may exceed max_payload_bytes by (doc/08 section 2.3: + 2 KB shell).
	envelopeSlack = 2048
	// maxElapsedMS caps elapsed_ms (7 days) before it becomes a time.Duration.
	maxElapsedMS = int64(7 * 24 * time.Hour / time.Millisecond)
	// maxSourcePath / maxConsentVersion are the column limits.
	maxSourcePath     = 300
	maxConsentVersion = 40
)

// Options carries the numbers that are not in the database. Zero values take the defaults of doc/08 section
// 2.9; the integrator fills the first three from Env.Forms (forms.dailyLimit, forms.minFillSeconds,
// forms.maxClockSkew) and may expose the rest as new keys.
type Options struct {
	// DailyLimit is the fallback of rate_limit_daily_max (forms.dailyLimit, default 20).
	DailyLimit int
	// MinFillSeconds is the fallback of min_fill_seconds (forms.minFillSeconds, default 3).
	MinFillSeconds int
	// MaxClockSkew is how far a client timestamp may lie in the future (forms.maxClockSkew, default 10m).
	MaxClockSkew time.Duration
	// KeyPerMinute / KeyPerDay cap the submissions of ALL visitors together per portal key (R-020;
	// proposed keys forms.keyPerMinute = 30, forms.keyPerDay = 1000).
	KeyPerMinute int
	KeyPerDay    int
	// DedupeTTL is how long an identical submission of the same visitor is treated as a retry (120 s).
	DedupeTTL time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

func (o Options) withDefaults() Options {
	if o.DailyLimit <= 0 {
		o.DailyLimit = 20
	}
	if o.MinFillSeconds <= 0 {
		o.MinFillSeconds = 3
	}
	if o.MaxClockSkew <= 0 {
		o.MaxClockSkew = 10 * time.Minute
	}
	if o.KeyPerMinute <= 0 {
		o.KeyPerMinute = 30
	}
	if o.KeyPerDay <= 0 {
		o.KeyPerDay = 1000
	}
	if o.DedupeTTL <= 0 {
		o.DedupeTTL = 120 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Visitor is who sent the submission, as the controller found out (pkg/clientip). The address is canonical
// and is only ever hashed.
type Visitor struct {
	IP        string
	UserAgent string
	// KeyID names the portal key that made the call (a truncated hash), for the aggregate cap.
	KeyID string
}

// SubmitInput is a parsed P-15 call.
type SubmitInput struct {
	Slug string
	// Locale is already negotiated (an active locale).
	Locale string
	// BodySize is the length of the raw request body.
	BodySize int
	Request  models.SubmissionRequest
	Visitor  Visitor
}

// FormService serves P-14 and P-15.
type FormService interface {
	Definition(ctx context.Context, locale, slug string) (*models.FormDefinition, error)
	// Submit validates and stores a submission. A nil error means "received" - for a spam submission too.
	Submit(ctx context.Context, in SubmitInput) error
}

// Deps are the collaborators of the service.
type Deps struct {
	Forms     contentrepositories.FormRepository
	Submitter formsrepositories.SubmissionRepository
	Cache     *publiccache.Cache
	Hasher    *iphash.Hasher
	Limiter   ratelimit.Limiter
	// Redis is used by the duplicate marker; nil makes every submission fail with 503 (it already does, in
	// the limiter).
	Redis   redis.Cmdable
	Keys    rediskeys.Keys
	Options Options
}

type formService struct {
	d      Deps
	o      Options
	limits *Limits
	dedupe *Dedupe
}

// NewFormService builds the service.
func NewFormService(d Deps) FormService {
	o := d.Options.withDefaults()
	return &formService{
		d: d, o: o,
		limits: NewLimits(d.Limiter, d.Keys, o.Now, o.KeyPerMinute, o.KeyPerDay),
		dedupe: NewDedupe(d.Redis, d.Keys, o.DedupeTTL),
	}
}

// ---- P-14 ---------------------------------------------------------------------------------------------------

// Definition returns the public definition of a published form (404 when it is not published).
func (s *formService) Definition(ctx context.Context, locale, slug string) (*models.FormDefinition, error) {
	if !readkit.KeyPattern.MatchString(slug) {
		return nil, apperror.NotFound()
	}
	def, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: "form", Locale: locale, Params: map[string]string{"slug": slug}},
		func(ctx context.Context) (*models.FormDefinition, []string, error) {
			f, err := s.d.Forms.Find(ctx, locale, slug)
			if err != nil {
				return nil, nil, notFoundOr(err)
			}
			fields, err := s.d.Forms.Fields(ctx, locale, slug)
			if err != nil {
				return nil, nil, err
			}
			d, err := BuildDefinition(*f, fields)
			if err != nil {
				return nil, nil, apperror.Internal(err)
			}
			return d, []string{formTag(f.FormID), "locales"}, nil
		})
	return def, err
}

func formTag(id int64) string { return "form:" + strconv.FormatInt(id, 10) }

func notFoundOr(err error) error {
	if errors.Is(err, contentrepositories.ErrNotFound) {
		return apperror.NotFound()
	}
	return err
}

// core is what the submit path needs of a form. It is language neutral (the view repeats it per locale), so
// one cached copy per locale is enough, tagged form:<id> like the public definition.
type core struct {
	FormID         int64           `json:"form_id"`
	JSONSchema     json.RawMessage `json:"json_schema"`
	FieldOrder     []string        `json:"field_order"`
	Honeypot       string          `json:"honeypot"`
	RateMax        *int            `json:"rate_max"`
	RateWindowS    *int            `json:"rate_window_s"`
	DailyMax       *int            `json:"daily_max"`
	MinFillSeconds *int            `json:"min_fill_seconds"`
	MaxPayload     *int            `json:"max_payload"`
}

func (s *formService) core(ctx context.Context, locale, slug string) (*core, error) {
	c, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: "formcore", Locale: locale, Params: map[string]string{"slug": slug}},
		func(ctx context.Context) (*core, []string, error) {
			f, err := s.d.Forms.Find(ctx, locale, slug)
			if err != nil {
				return nil, nil, notFoundOr(err)
			}
			c := &core{
				FormID: f.FormID, JSONSchema: f.JSONSchema.Bytes(), FieldOrder: f.FieldOrder, Honeypot: deref(f.HoneypotField),
				RateMax: f.RateLimitMax, RateWindowS: f.RateLimitWindowS, DailyMax: f.RateLimitDailyMax,
				MinFillSeconds: f.MinFillSeconds, MaxPayload: f.MaxPayloadBytes,
			}
			return c, []string{formTag(f.FormID), "locales"}, nil
		})
	return c, err
}

// minFill is the form's min_fill_seconds; 0 is a real value ("never flag a fast fill"), only NULL falls back to
// forms.minFillSeconds.
func minFill(c *core, o Options) time.Duration {
	if c.MinFillSeconds != nil && *c.MinFillSeconds >= 0 {
		return time.Duration(*c.MinFillSeconds) * time.Second
	}
	return time.Duration(o.MinFillSeconds) * time.Second
}

func orInt(p *int, fallback int) int {
	if p != nil && *p > 0 {
		return *p
	}
	return fallback
}

// ---- P-15 ---------------------------------------------------------------------------------------------------

// Submit runs the submission in the order of doc/08 section 6.2 (the form is looked up before the size is
// checked because the limit belongs to the form):
//
//	form definition (404) -> size (413) -> rate limits (429 / 503) -> sanitise + validate (422) -> spam score ->
//	duplicate marker -> forms_submit_form -> received.
//
// A spam submission (honeypot, too fast, stamp from the future) is stored with its score and answered exactly
// like a normal one: no signal reaches a bot.
func (s *formService) Submit(ctx context.Context, in SubmitInput) error {
	if !readkit.KeyPattern.MatchString(in.Slug) {
		return apperror.NotFound()
	}
	c, err := s.core(ctx, in.Locale, in.Slug)
	if err != nil {
		return err
	}
	maxPayload := orInt(c.MaxPayload, defaultMaxPayload)
	if in.BodySize > maxPayload+envelopeSlack {
		return apperror.PayloadTooLarge()
	}

	// the visitor, as a salted hash; no Redis means no salt: the submission is refused (503), never stored unlimited
	ip := in.Visitor.IP
	if ip == "" {
		ip = "0.0.0.0"
	}
	sum, err := s.d.Hasher.Hash(ctx, ip)
	if err != nil {
		logs.WarnEvery("forms.iphash", time.Minute, "forms: the IP salt is unavailable, submission refused")
		return apperror.ServiceUnavailable(err, 5*time.Second)
	}
	if lerr := s.limits.Check(ctx, Plan{
		Slug: in.Slug, IPShort: sum.Short(), KeyID: in.Visitor.KeyID,
		WindowMax: orInt(c.RateMax, defaultWindowMax), Window: time.Duration(orInt(c.RateWindowS, int(defaultWindow/time.Second))) * time.Second,
		DailyMax: orInt(c.DailyMax, s.o.DailyLimit),
	}); lerr != nil {
		return lerr
	}

	schema, err := validate.Compile(c.JSONSchema, c.FieldOrder, in.Slug)
	if err != nil {
		return apperror.Internal(err)
	}
	data, honeypot := splitHoneypot(in.Request.Data, c.Honeypot)
	res := schema.Check(data)
	if len(res.Errors) > 0 {
		return apperror.Validation(res.Errors...)
	}

	now := s.o.Now()
	verdict := Score(buildSpamInput(in.Request, honeypot, minFill(c, s.o), now, s.o.MaxClockSkew))

	payload, err := json.Marshal(res.Data) // object keys are sorted: the same content always gives the same bytes
	if err != nil {
		return apperror.Internal(err)
	}
	if len(payload) > maxPayload {
		return apperror.PayloadTooLarge()
	}

	marker := s.dedupe.Key(in.Slug, sum.Short(), payload)
	fresh, err := s.dedupe.Claim(ctx, marker)
	if err != nil {
		return apperror.ServiceUnavailable(err, 5*time.Second)
	}
	if !fresh {
		return nil // an identical submission of this visitor was just stored: answer "received" again
	}

	sub := formsrepositories.Submission{
		FormSlug: in.Slug, Locale: in.Locale, DataJSON: payload, IPHash: sum.Bytes(),
		UserAgent: in.Visitor.UserAgent, SpamScore: verdict.Score,
		SourcePath: cleanSourcePath(in.Request.SourcePath),
	}
	if cn := in.Request.Consent; cn != nil {
		sub.ConsentVersion = validate.SanitizeText(cn.Version, maxConsentVersion, false)
		if at, perr := time.Parse(time.RFC3339, cn.At); perr == nil {
			sub.ConsentAt = &at
		}
	}
	if _, err := s.d.Submitter.Submit(ctx, sub); err != nil {
		s.dedupe.Release(ctx, marker)
		return submitError(err, len(payload), maxPayload)
	}
	return nil
}

// submitError maps the outcome of forms_submit_form (doc/08 section 6.2): P0002 -> 404, 22023 -> 413 when the
// payload is over the limit and 422 otherwise, anything else -> 500 (its text names a SQLSTATE at most).
func submitError(err error, size, limit int) error {
	switch {
	case errors.Is(err, formsrepositories.ErrFormUnavailable):
		return apperror.NotFound()
	case errors.Is(err, formsrepositories.ErrInvalidPayload):
		if size > limit {
			return apperror.PayloadTooLarge()
		}
		return apperror.Validation(apperror.Field("/data", "invalid_payload"))
	}
	return err
}

// splitHoneypot takes the honeypot value out of the submitted data. A bot that posts the hidden field inside
// `data` (instead of the body's honeypot) must not get a 422 "unknown_field" that tells it which field is the
// trap: the key is removed and its value counts as the honeypot value. The input map is not modified.
func splitHoneypot(data map[string]any, field string) (map[string]any, string) {
	if field == "" {
		return data, ""
	}
	v, ok := data[field]
	if !ok {
		return data, ""
	}
	out := make(map[string]any, len(data))
	for k, val := range data {
		if k != field {
			out[k] = val
		}
	}
	switch t := v.(type) {
	case nil:
		return out, ""
	case string:
		return out, t
	}
	return out, "x" // any other non-null value in the trap field is a bot
}

func buildSpamInput(req models.SubmissionRequest, dataHoneypot string, minFill time.Duration, now time.Time, skew time.Duration) SpamInput {
	in := SpamInput{Honeypot: req.Honeypot, MinFill: minFill, Now: now, MaxSkew: skew}
	if in.Honeypot == "" {
		in.Honeypot = dataHoneypot
	}
	if req.ElapsedMS != nil && *req.ElapsedMS > 0 {
		ms := *req.ElapsedMS
		if ms > maxElapsedMS {
			ms = maxElapsedMS // a huge value must not wrap around when converted to a Duration
		}
		in.Elapsed = time.Duration(ms) * time.Millisecond
	}
	if req.RenderedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, req.RenderedAt)
		if err != nil {
			in.StampInvalid = true
		} else {
			in.RenderedAt = &t
		}
	}
	return in
}

func cleanSourcePath(p string) string {
	return validate.SanitizeText(p, maxSourcePath, false)
}
