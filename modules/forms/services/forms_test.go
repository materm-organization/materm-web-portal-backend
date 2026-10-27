package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	contentrepositories "github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
	formsrepositories "github.com/materm-organization/materm-web-portal-backend/modules/forms/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/iphash"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

const contactSchema = `{"type":"object","additionalProperties":false,"required":["name","email","topic","message"],"properties":{
 "name":{"type":"string","minLength":2,"maxLength":100,"x-pii":true},
 "email":{"type":"string","format":"email","maxLength":254,"x-pii":true},
 "phone":{"type":"string","maxLength":30,"pattern":"^[0-9 +()\\-.]+$","x-pii":true},
 "topic":{"type":"string","enum":["booking","events","camping","other"]},
 "message":{"type":"string","minLength":10,"maxLength":2000,"x-pii":true}}}`

func ip(i int) *int { return &i }

func contactForm() entities.PublishedForm {
	hp := "company"
	return entities.PublishedForm{
		FormID: 7, Slug: "contact", JSONSchema: entities.JSONB(contactSchema),
		FieldOrder:    entities.StringArray{"name", "email", "phone", "topic", "message"},
		HoneypotField: &hp, RateLimitMax: ip(5), RateLimitWindowS: ip(600), RateLimitDailyMax: ip(20),
		MinFillSeconds: ip(3), MaxPayloadBytes: ip(16384),
	}
}

// ---- fakes -----------------------------------------------------------------------------------------------------

type fakeForms struct {
	form *entities.PublishedForm
}

func (f fakeForms) Find(_ context.Context, _, slug string) (*entities.PublishedForm, error) {
	if f.form == nil || f.form.Slug != slug {
		return nil, contentrepositories.ErrNotFound
	}
	c := *f.form
	return &c, nil
}
func (f fakeForms) Fields(context.Context, string, string) ([]entities.PublishedFormField, error) {
	return nil, nil
}

type fakeSubmitter struct {
	mu   sync.Mutex
	subs []formsrepositories.Submission
	err  error
}

func (f *fakeSubmitter) Submit(_ context.Context, s formsrepositories.Submission) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.subs = append(f.subs, s)
	return "0192e5a0-0000-7000-8000-000000000001", nil
}

type fakeLimiter struct {
	mu     sync.Mutex
	counts map[string]int64
	hits   []string
	err    error
}

func (f *fakeLimiter) Hit(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return ratelimit.Result{}, f.err
	}
	if f.counts == nil {
		f.counts = map[string]int64{}
	}
	f.hits = append(f.hits, key)
	f.counts[key]++
	c := f.counts[key]
	return ratelimit.Result{Allowed: c <= int64(limit), Count: c, Limit: limit, RetryAfter: window}, nil
}
func (f *fakeLimiter) Count(context.Context, string) (int64, time.Duration, error) { return 0, 0, nil }
func (f *fakeLimiter) Reset(context.Context, string) error                         { return nil }

// fakeRedis implements the three commands the service uses (salt Get/SetNX, marker SetNX/Del).
type fakeRedis struct {
	redis.Cmdable
	mu   sync.Mutex
	data map[string]string
	down bool
}

func newFakeRedis() *fakeRedis { return &fakeRedis{data: map[string]string{}} }

func (f *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return redis.NewStringResult("", errors.New("connection refused"))
	}
	v, ok := f.data[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(v, nil)
}

func (f *fakeRedis) SetNX(ctx context.Context, key string, value interface{}, _ time.Duration) *redis.BoolCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return redis.NewBoolResult(false, errors.New("connection refused"))
	}
	if _, ok := f.data[key]; ok {
		return redis.NewBoolResult(false, nil)
	}
	switch v := value.(type) {
	case []byte:
		f.data[key] = string(v)
	case string:
		f.data[key] = v
	}
	return redis.NewBoolResult(true, nil)
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.data, k)
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

type rig struct {
	svc     FormService
	sub     *fakeSubmitter
	lim     *fakeLimiter
	rds     *fakeRedis
	forms   *fakeForms
	keys    rediskeys.Keys
	now     time.Time
	options Options
}

func newRig(t *testing.T) *rig {
	t.Helper()
	form := contactForm()
	r := &rig{
		sub: &fakeSubmitter{}, lim: &fakeLimiter{}, rds: newFakeRedis(), forms: &fakeForms{form: &form},
		keys: rediskeys.New("test:"), now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC),
	}
	r.options = Options{Now: func() time.Time { return r.now }}
	r.build()
	return r
}

func (r *rig) build() {
	r.svc = NewFormService(Deps{
		Forms: r.forms, Submitter: r.sub, Cache: publiccache.New(publiccache.Options{}),
		Hasher: iphash.New(r.rds, r.keys).WithClock(r.options.Now), Limiter: r.lim, Redis: r.rds, Keys: r.keys, Options: r.options,
	})
}

func validRequest() models.SubmissionRequest {
	ms := int64(8200)
	return models.SubmissionRequest{
		Locale: "en", SourcePath: "/contact", ElapsedMS: &ms,
		Data: map[string]any{"name": "Noy Phommasan", "email": "noy@example.com", "phone": "", "topic": "booking", "message": "Hello, I would like to book a tent."},
	}
}

func input(req models.SubmissionRequest) SubmitInput {
	return SubmitInput{Slug: "contact", Locale: "en", BodySize: 400, Request: req,
		Visitor: Visitor{IP: "203.0.113.7", UserAgent: "Mozilla/5.0 (test)", KeyID: "keyid0123456789a"}}
}

func status(err error) int {
	if err == nil {
		return 200
	}
	return apperror.Classify(err).Status
}

// ---- tests -----------------------------------------------------------------------------------------------------

func TestSubmitStoresTheCleanedMessageWithoutTheAddress(t *testing.T) {
	r := newRig(t)
	req := validRequest()
	req.Data["name"] = "  Noy\tPhommasan  " // sanitised before it is stored
	if err := r.svc.Submit(context.Background(), input(req)); err != nil {
		t.Fatal(err)
	}
	if len(r.sub.subs) != 1 {
		t.Fatalf("%d submissions", len(r.sub.subs))
	}
	s := r.sub.subs[0]
	if s.SpamScore != 0 || s.FormSlug != "contact" || s.Locale != "en" || s.SourcePath != "/contact" || s.UserAgent != "Mozilla/5.0 (test)" {
		t.Errorf("%+v", s)
	}
	var data map[string]any
	if err := json.Unmarshal(s.DataJSON, &data); err != nil {
		t.Fatal(err)
	}
	if data["name"] != "Noy Phommasan" || data["phone"] != "" || data["topic"] != "booking" {
		t.Errorf("data %v", data)
	}
	if len(s.IPHash) != 32 {
		t.Errorf("ip hash is %d bytes", len(s.IPHash))
	}
	// the address is nowhere: not in the row, not in a rate-limit key, not in the marker
	everything := string(s.DataJSON) + s.UserAgent + s.SourcePath + string(s.IPHash)
	for _, k := range r.lim.hits {
		everything += k
	}
	for k := range r.rds.data {
		everything += k
	}
	if strings.Contains(everything, "203.0.113.7") || strings.Contains(everything, "203.0.113") {
		t.Fatal("the raw IP address leaked")
	}
}

func TestSpamIsStoredAndStillAnswered(t *testing.T) {
	cases := []struct {
		name  string
		mod   func(*models.SubmissionRequest)
		score int16
	}{
		{"honeypot in the body", func(r *models.SubmissionRequest) { r.Honeypot = "http://spam.example" }, 100},
		{"honeypot posted inside data", func(r *models.SubmissionRequest) { r.Data["company"] = "ACME" }, 100},
		{"honeypot in data, empty value", func(r *models.SubmissionRequest) { r.Data["company"] = "" }, 0},
		{"honeypot in data, non-string", func(r *models.SubmissionRequest) { r.Data["company"] = 5 }, 100},
		{"too fast", func(r *models.SubmissionRequest) { ms := int64(1000); r.ElapsedMS = &ms }, 90},
		{"exactly the minimum", func(r *models.SubmissionRequest) { ms := int64(3000); r.ElapsedMS = &ms }, 0},
		{"no elapsed_ms", func(r *models.SubmissionRequest) { r.ElapsedMS = nil }, 90},
		{"negative elapsed", func(r *models.SubmissionRequest) { ms := int64(-5); r.ElapsedMS = &ms }, 90},
		{"stamp from the future", func(r *models.SubmissionRequest) { r.RenderedAt = "2026-10-27T10:11:00Z" }, 90},
		{"stamp within the skew", func(r *models.SubmissionRequest) { r.RenderedAt = "2026-10-27T10:09:00Z" }, 0},
		{"stamp in the past", func(r *models.SubmissionRequest) { r.RenderedAt = "2026-10-26T10:00:00Z" }, 0},
		{"stamp is not a timestamp", func(r *models.SubmissionRequest) { r.RenderedAt = "yesterday" }, 90},
		{"honeypot beats too fast", func(r *models.SubmissionRequest) {
			ms := int64(10)
			r.ElapsedMS, r.Honeypot = &ms, "x"
		}, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			req := validRequest()
			c.mod(&req)
			if err := r.svc.Submit(context.Background(), input(req)); err != nil {
				t.Fatalf("a spam submission must be answered like any other, got %v", err)
			}
			if len(r.sub.subs) != 1 || r.sub.subs[0].SpamScore != c.score {
				t.Fatalf("stored %+v, want score %d", r.sub.subs, c.score)
			}
			if strings.Contains(string(r.sub.subs[0].DataJSON), "company") {
				t.Errorf("the honeypot key was stored: %s", r.sub.subs[0].DataJSON)
			}
		})
	}
}

func TestMinFillZeroIsARealValue(t *testing.T) {
	r := newRig(t)
	r.forms.form.MinFillSeconds = ip(0)
	r.build()
	req := validRequest()
	req.ElapsedMS = nil
	if err := r.svc.Submit(context.Background(), input(req)); err != nil {
		t.Fatal(err)
	}
	if r.sub.subs[0].SpamScore != 0 {
		t.Errorf("min_fill_seconds = 0 must never flag, got %d", r.sub.subs[0].SpamScore)
	}
	// NULL falls back to forms.minFillSeconds
	r2 := newRig(t)
	r2.forms.form.MinFillSeconds = nil
	r2.build()
	if err := r2.svc.Submit(context.Background(), input(req)); err != nil {
		t.Fatal(err)
	}
	if r2.sub.subs[0].SpamScore != 90 {
		t.Errorf("NULL min_fill_seconds uses the config fallback (3 s): %d", r2.sub.subs[0].SpamScore)
	}
}

func TestScore(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	future := now.Add(11 * time.Minute)
	near := now.Add(9 * time.Minute)
	v := Score(SpamInput{Honeypot: "x", Elapsed: time.Second, MinFill: 3 * time.Second, RenderedAt: &future, Now: now, MaxSkew: 10 * time.Minute})
	if v.Score != 100 || len(v.Reasons) != 3 {
		t.Errorf("all triggers: %+v", v)
	}
	if v := Score(SpamInput{Elapsed: 5 * time.Second, MinFill: 3 * time.Second, RenderedAt: &near, Now: now, MaxSkew: 10 * time.Minute}); v.Score != 0 || len(v.Reasons) != 0 {
		t.Errorf("clean: %+v", v)
	}
	if v := Score(SpamInput{Elapsed: 0, MinFill: 0, Now: now}); v.Score != 0 {
		t.Errorf("min_fill 0 never flags: %+v", v)
	}
	if v := Score(SpamInput{Elapsed: time.Second, MinFill: 3 * time.Second}); v.Score != ScoreTooFast || v.Reasons[0] != ReasonTooFast {
		t.Errorf("fast: %+v", v)
	}
}

func TestInvalidInputIs422AndNothingIsStored(t *testing.T) {
	r := newRig(t)
	req := validRequest()
	req.Data["name"], req.Data["message"], req.Data["extra"] = "x", "short", "1"
	err := r.svc.Submit(context.Background(), input(req))
	ae, ok := apperror.AsError(err)
	if !ok || ae.Status != 422 || ae.Code != apperror.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	got := map[string]string{}
	for _, e := range ae.Errors {
		if e.In != "body" {
			t.Errorf("location %q", e.In)
		}
		got[e.Path] = e.Code
	}
	want := map[string]string{"/data/name": "required", "/data/message": "messageShort", "/data/extra": "unknown_field"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if len(r.sub.subs) != 0 {
		t.Fatal("an invalid submission was stored")
	}
	// the typed text is not in the answer
	b, _ := json.Marshal(ae.Errors)
	if strings.Contains(string(b), "short") && !strings.Contains(string(b), "messageShort") {
		t.Errorf("echoed value: %s", b)
	}
}

func TestUnknownOrBadFormIs404(t *testing.T) {
	r := newRig(t)
	for _, slug := range []string{"nope", "Bad Slug", "../x", ""} {
		in := input(validRequest())
		in.Slug = slug
		if got := status(r.svc.Submit(context.Background(), in)); got != 404 {
			t.Errorf("%q: %d", slug, got)
		}
	}
	if len(r.lim.hits) != 0 {
		t.Error("a request for a form that does not exist must not create rate-limit keys")
	}
}

func TestPayloadTooLarge(t *testing.T) {
	r := newRig(t)
	in := input(validRequest())
	in.BodySize = 16384 + 2048 + 1
	if got := status(r.svc.Submit(context.Background(), in)); got != 413 {
		t.Errorf("body over max_payload_bytes + 2 KB: %d", got)
	}
	in.BodySize = 16384 + 2048
	if got := status(r.svc.Submit(context.Background(), in)); got != 200 {
		t.Errorf("body at the limit: %d", got)
	}
	// a small payload limit on the form is honoured for the stored data as well
	r2 := newRig(t)
	r2.forms.form.MaxPayloadBytes = ip(256)
	req := validRequest()
	req.Data["message"] = strings.Repeat("m", 600)
	in2 := input(req)
	in2.BodySize = 100
	if got := status(r2.svc.Submit(context.Background(), in2)); got != 413 {
		t.Errorf("cleaned data over max_payload_bytes: %d", got)
	}
}

func TestRateLimits(t *testing.T) {
	r := newRig(t)
	// per visitor: 5 per window, then 429 with Retry-After; the aggregate counters are not the cause
	for i := 1; i <= 5; i++ {
		req := validRequest()
		req.Data["message"] = strings.Repeat("different ", i) + "message"
		if err := r.svc.Submit(context.Background(), input(req)); err != nil {
			t.Fatalf("submission %d: %v", i, err)
		}
	}
	req := validRequest()
	req.Data["message"] = "a sixth message from the same visitor"
	err := r.svc.Submit(context.Background(), input(req))
	ae, ok := apperror.AsError(err)
	if !ok || ae.Status != 429 || ae.Code != apperror.CodeRateLimited || ae.RetryAfter <= 0 {
		t.Fatalf("sixth: %v", err)
	}
	if len(r.sub.subs) != 5 {
		t.Fatalf("%d stored", len(r.sub.subs))
	}
	// another visitor is not affected
	other := input(req)
	other.Visitor.IP = "198.51.100.20"
	if err := r.svc.Submit(context.Background(), other); err != nil {
		t.Fatalf("other visitor: %v", err)
	}
	// the keys carry the prefix and hashes only
	for _, k := range r.lim.hits {
		if !strings.HasPrefix(k, "test:rl:form") {
			t.Errorf("key outside the form families: %s", k)
		}
	}
}

func TestAggregateCapPerPortalKeyStopsARotatingAddressFlood(t *testing.T) {
	r := newRig(t)
	r.options.KeyPerMinute = 3
	r.build()
	var denied int
	for i := 0; i < 6; i++ {
		in := input(validRequest())
		in.Visitor.IP = "198.51.100." + string(rune('1'+i)) // a different "visitor" every time (leaked key)
		in.Request.Data["message"] = strings.Repeat("flood ", i+2)
		if err := r.svc.Submit(context.Background(), in); err != nil {
			if ae, _ := apperror.AsError(err); ae == nil || ae.Status != 429 {
				t.Fatalf("%v", err)
			}
			denied++
		}
	}
	if denied != 3 || len(r.sub.subs) != 3 {
		t.Fatalf("denied %d stored %d, want 3 and 3", denied, len(r.sub.subs))
	}
	// a refused call stopped at the aggregate counter: it did not also inflate the per-visitor counters
	var aggregate, perVisitor int
	for _, k := range r.lim.hits {
		if strings.Contains(k, "rl:form-key:") {
			aggregate++
		} else {
			perVisitor++
		}
	}
	if aggregate != 6+3 /* day counter hit only while the minute counter allows */ || perVisitor != 3*2 {
		t.Errorf("aggregate %d, per visitor %d", aggregate, perVisitor)
	}
	// a different portal key has its own budget
	in := input(validRequest())
	in.Visitor.KeyID = "otherkeyid0123456"
	in.Request.Data["message"] = "from the second key"
	if err := r.svc.Submit(context.Background(), in); err != nil {
		t.Fatalf("second key: %v", err)
	}
}

func TestRetryAfterIsTheLongestWait(t *testing.T) {
	r := newRig(t)
	r.forms.form.RateLimitMax = ip(1) // window: 1 per 600 s; daily: 1 per day
	r.forms.form.RateLimitDailyMax = ip(1)
	r.build()
	req := validRequest()
	if err := r.svc.Submit(context.Background(), input(req)); err != nil {
		t.Fatal(err)
	}
	req.Data["message"] = "a second, different message"
	ae, _ := apperror.AsError(r.svc.Submit(context.Background(), input(req)))
	if ae == nil || ae.Status != 429 {
		t.Fatalf("%v", ae)
	}
	// both refused: the daily wait (until 00:00 UTC, 14 h) is longer than the window wait (10 min)
	if ae.RetryAfter != 14*time.Hour {
		t.Errorf("Retry-After %s, want 14h", ae.RetryAfter)
	}
}

func TestWindowRetryAfterEndsWithTheAlignedWindow(t *testing.T) {
	r := newRig(t)
	r.forms.form.RateLimitMax = ip(1) // 1 per 600 s window; the daily cap (20) is not reached
	r.now = time.Date(2026, 10, 27, 10, 9, 50, 0, time.UTC)
	r.build()
	req := validRequest()
	if err := r.svc.Submit(context.Background(), input(req)); err != nil {
		t.Fatal(err)
	}
	req.Data["message"] = "a second, different message"
	ae, _ := apperror.AsError(r.svc.Submit(context.Background(), input(req)))
	// the window 10:00:00-10:10:00 ends in 10 s, although the counter was armed for 600 s
	if ae == nil || ae.Status != 429 || ae.RetryAfter != 10*time.Second {
		t.Fatalf("%+v", ae)
	}
}

func TestRedisDownFailsClosed(t *testing.T) {
	// the IP salt cannot be read
	r := newRig(t)
	r.rds.down = true
	err := r.svc.Submit(context.Background(), input(validRequest()))
	if ae, _ := apperror.AsError(err); ae == nil || ae.Status != 503 || ae.Code != apperror.CodeServiceUnavailable {
		t.Fatalf("salt: %v", err)
	}
	// the limiter fails
	r = newRig(t)
	r.lim.err = errors.New("redis: connection refused")
	err = r.svc.Submit(context.Background(), input(validRequest()))
	if ae, _ := apperror.AsError(err); ae == nil || ae.Status != 503 {
		t.Fatalf("limiter: %v", err)
	}
	if len(r.sub.subs) != 0 {
		t.Fatal("stored without limits")
	}
	// no Redis configured at all
	form := contactForm()
	svc := NewFormService(Deps{Forms: fakeForms{&form}, Submitter: &fakeSubmitter{}, Cache: publiccache.New(publiccache.Options{}),
		Hasher: iphash.New(nil, rediskeys.New("test:")), Limiter: ratelimit.NewRedis(nil), Keys: rediskeys.New("test:")})
	if got := status(svc.Submit(context.Background(), input(validRequest()))); got != 503 {
		t.Fatalf("no redis: %d", got)
	}
}

func TestIdenticalRetryIsStoredOnce(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 3; i++ {
		if err := r.svc.Submit(context.Background(), input(validRequest())); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if len(r.sub.subs) != 1 {
		t.Fatalf("%d rows for 3 identical submissions", len(r.sub.subs))
	}
	// another visitor sending the same text is a different submission
	other := input(validRequest())
	other.Visitor.IP = "198.51.100.77"
	if err := r.svc.Submit(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if len(r.sub.subs) != 2 {
		t.Fatalf("%d rows", len(r.sub.subs))
	}
}

func TestFailedInsertReleasesTheMarker(t *testing.T) {
	r := newRig(t)
	r.sub.err = errors.New("forms_submit_form failed (sqlstate 08006)")
	err := r.svc.Submit(context.Background(), input(validRequest()))
	if ae, _ := apperror.AsError(err); ae != nil && ae.Status != 500 {
		t.Fatalf("%v", err)
	}
	if status(err) != 500 {
		t.Fatalf("status %d", status(err))
	}
	r.sub.err = nil
	if err := r.svc.Submit(context.Background(), input(validRequest())); err != nil {
		t.Fatal(err)
	}
	if len(r.sub.subs) != 1 {
		t.Fatalf("the retry after a failure must be stored: %d", len(r.sub.subs))
	}
}

func TestSubmitErrorMapping(t *testing.T) {
	cases := []struct {
		err         error
		size, limit int
		want        int
	}{
		{formsrepositories.ErrFormUnavailable, 10, 100, 404},
		{formsrepositories.ErrInvalidPayload, 200, 100, 413},
		{formsrepositories.ErrInvalidPayload, 50, 100, 422},
		{errors.New("forms_submit_form failed (sqlstate 40001)"), 1, 1, 500},
		{context.DeadlineExceeded, 1, 1, 503},
	}
	for _, c := range cases {
		if got := status(submitError(c.err, c.size, c.limit)); got != c.want {
			t.Errorf("%v: %d, want %d", c.err, got, c.want)
		}
	}
}

func TestSubmitCleansConsentAndSourcePath(t *testing.T) {
	r := newRig(t)
	req := validRequest()
	req.SourcePath = "/contact\n\x00" + strings.Repeat("p", 400)
	req.Consent = &models.Consent{Version: "2026-01\n", At: "2026-10-27T09:59:00Z"}
	if err := r.svc.Submit(context.Background(), input(req)); err != nil {
		t.Fatal(err)
	}
	s := r.sub.subs[0]
	if len(s.SourcePath) > 300 || strings.ContainsAny(s.SourcePath, "\n\x00") {
		t.Errorf("source path %q", s.SourcePath)
	}
	if s.ConsentVersion != "2026-01" || s.ConsentAt == nil || !s.ConsentAt.Equal(time.Date(2026, 10, 27, 9, 59, 0, 0, time.UTC)) {
		t.Errorf("consent %q %v", s.ConsentVersion, s.ConsentAt)
	}
}

func TestDedupeKeyHoldsOnlyHashes(t *testing.T) {
	d := NewDedupe(nil, rediskeys.New("test:"), time.Minute)
	k := d.Key("contact", "abcdef0123456789abcdef0123456789", []byte(`{"email":"noy@example.com"}`))
	if !strings.HasPrefix(k, "test:rl:form:contact:abcdef0123456789abcdef0123456789:dup:") || strings.Contains(k, "noy") || strings.Contains(k, "example") {
		t.Fatalf("%s", k)
	}
	if k != d.Key("contact", "abcdef0123456789abcdef0123456789", []byte(`{"email":"noy@example.com"}`)) {
		t.Fatal("not stable")
	}
	if k == d.Key("contact", "abcdef0123456789abcdef0123456789", []byte(`{"email":"x@example.com"}`)) {
		t.Fatal("content does not matter")
	}
	if _, err := d.Claim(context.Background(), k); err == nil {
		t.Fatal("a Dedupe without Redis must fail, not allow")
	}
}

func TestSplitHoneypotDoesNotMutateTheInput(t *testing.T) {
	in := map[string]any{"a": "1", "company": "x"}
	out, hp := splitHoneypot(in, "company")
	if hp != "x" || len(out) != 1 || len(in) != 2 {
		t.Fatalf("%v %q %v", out, hp, in)
	}
	if out, hp := splitHoneypot(in, ""); hp != "" || len(out) != 2 {
		t.Fatalf("no honeypot configured: %v %q", out, hp)
	}
}

// ---- P-14 ----------------------------------------------------------------------------------------------------------

func TestBuildDefinition(t *testing.T) {
	f := contactForm()
	f.ResolvedLocale, f.Title, f.SubmitLabel = "en", "Send a message", "Send message"
	desc, send := "We reply soon.", "Sending…"
	f.Description, f.SendingLabel = &desc, &send
	f.Messages = entities.JSONB(`{"required":"This field is required","messageShort":"Please write at least 10 characters","n":5}`)
	hp := "company"
	f.HoneypotField = &hp
	ph := "Your name"
	fields := []entities.PublishedFormField{
		{FieldKey: "name", Label: "Full name", Placeholder: &ph},
		{FieldKey: "topic", Label: "What is it about?", OptionLabels: entities.JSONB(`{"booking":"Booking & stays","other":"Something else"}`)},
		{FieldKey: "email", Label: "Email"},
		{FieldKey: "message", Label: "Message"},
	}
	def, err := BuildDefinition(f, fields)
	if err != nil {
		t.Fatal(err)
	}
	if def.Slug != "contact" || def.HoneypotField != "company" || def.MinFillSeconds != 3 || def.Title != "Send a message" || def.Description != desc {
		t.Errorf("%+v", def)
	}
	if len(def.Messages) != 2 || def.Messages["n"] != "" {
		t.Errorf("only string messages survive: %v", def.Messages)
	}
	var keys []string
	for _, fl := range def.Fields {
		keys = append(keys, fl.Key)
	}
	if strings.Join(keys, ",") != "name,email,phone,topic,message" {
		t.Errorf("field order %v", keys)
	}
	byKey := map[string]models.FormField{}
	for _, fl := range def.Fields {
		byKey[fl.Key] = fl
	}
	if !byKey["name"].Required || byKey["phone"].Required || byKey["name"].Label != "Full name" || byKey["name"].Placeholder == nil {
		t.Errorf("name/phone: %+v %+v", byKey["name"], byKey["phone"])
	}
	if byKey["phone"].Label != "phone" {
		t.Errorf("a field without a translation is labelled with its key: %q", byKey["phone"].Label)
	}
	for k, fl := range byKey {
		if _, has := fl.Schema["x-pii"]; has {
			t.Errorf("%s: x-pii leaked", k)
		}
	}
	opts := byKey["topic"].Options
	if len(opts) != 4 || opts[0].Value != "booking" || opts[0].Label != "Booking & stays" || opts[1].Label != "events" || opts[3].Label != "Something else" {
		t.Errorf("options (schema order; value as label when untranslated): %+v", opts)
	}
	if byKey["name"].Options != nil {
		t.Error("only enum fields have options")
	}
	// nothing that belongs to the server is in the JSON
	b, _ := json.Marshal(def)
	for _, banned := range []string{"rate_limit", "max_payload", "recipient", "retention", "x-pii", "x-error", "daily"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("the definition mentions %q: %s", banned, b)
		}
	}
	var generic map[string]any
	_ = json.Unmarshal(b, &generic)
	for _, k := range []string{"slug", "resolved_locale", "title", "description", "submit_label", "sending_label", "success", "messages", "honeypot_field", "fields", "success_redirect_url"} {
		if _, ok := generic[k]; !ok {
			t.Errorf("frontend member %s missing", k)
		}
	}
}

func TestBuildDefinitionRefusesABrokenSchema(t *testing.T) {
	f := contactForm()
	f.JSONSchema = entities.JSONB(`[1,2]`)
	if _, err := BuildDefinition(f, nil); err == nil {
		t.Fatal("a schema that is not an object must be refused")
	}
}
