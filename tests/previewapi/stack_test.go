// Package previewapi tests P-19 (GET /api/v1/preview/resolve) through HTTP with the real middleware pipeline,
// controller, service and assembler. The offline tests here run over the frontend bundle (pkg/testutil/seedfake)
// and an in-memory stand-in for cms_consume_preview_token that follows the SQL function line by line; the
// dbtest-tagged files run the real function on the scratch schema.
package previewapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	previewcontroller "github.com/materm-organization/materm-web-portal-backend/modules/preview/controller"
	previewrepos "github.com/materm-organization/materm-web-portal-backend/modules/preview/repositories"
	previewroutes "github.com/materm-organization/materm-web-portal-backend/modules/preview/routes"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/apitest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

const tokenHeader = "X-Preview-Token"

// makeToken derives a 43-character base64url token (the shape the admin issues) from a seed.
func makeToken(seed string) string {
	sum := sha256.Sum256([]byte("preview-test:" + seed))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:43]
}

// ---- in-memory cms_consume_preview_token ----------------------------------------------------------------------

type tokenEntry struct {
	c       previewrepos.Consumed
	expires time.Time
	revoked bool
	uses    int
}

// fakeTokens mirrors the SQL function: usable when not revoked and not expired; every successful call counts a
// use; max_uses is NULL for every link the admin issues, so there is no limit.
type fakeTokens struct {
	mu      sync.Mutex
	entries map[string]*tokenEntry
	calls   int
}

func newFakeTokens() *fakeTokens { return &fakeTokens{entries: map[string]*tokenEntry{}} }

func (f *fakeTokens) add(token string, c previewrepos.Consumed, ttl time.Duration) *tokenEntry {
	sum := sha256.Sum256([]byte(token))
	e := &tokenEntry{c: c, expires: time.Now().Add(ttl)}
	f.mu.Lock()
	f.entries[hex.EncodeToString(sum[:])] = e
	f.mu.Unlock()
	return e
}

func (f *fakeTokens) Consume(_ context.Context, hash []byte) (*previewrepos.Consumed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	e, ok := f.entries[hex.EncodeToString(hash)]
	if !ok || e.revoked || !e.expires.After(time.Now()) {
		return nil, previewrepos.ErrInvalid
	}
	e.uses++
	c := e.c
	return &c, nil
}

func (f *fakeTokens) usesOf(e *tokenEntry) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return e.uses
}

// ---- in-memory limiter ------------------------------------------------------------------------------------------

type memLimiter struct {
	mu     sync.Mutex
	counts map[string]int
	fail   bool
}

func newMemLimiter() *memLimiter { return &memLimiter{counts: map[string]int{}} }

func (l *memLimiter) Hit(_ context.Context, key string, limit int, _ time.Duration) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return ratelimit.Result{}, ratelimit.ErrUnavailable
	}
	l.counts[key]++
	n := l.counts[key]
	return ratelimit.Result{Allowed: n <= limit, Count: int64(n), Limit: limit, RetryAfter: 30 * time.Second}, nil
}
func (l *memLimiter) Count(context.Context, string) (int64, time.Duration, error) { return 0, 0, nil }
func (l *memLimiter) Reset(context.Context, string) error                         { return nil }

// ---- shared sections --------------------------------------------------------------------------------------------

type fakeShared struct {
	rows []entities.PublishedPageSection
}

func (f fakeShared) SharedSections(_ context.Context, ids []int64) ([]entities.PublishedPageSection, error) {
	var out []entities.PublishedPageSection
	for _, r := range f.rows {
		for _, id := range ids {
			if r.SharedSectionID != nil && *r.SharedSectionID == id {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// ---- the stack ---------------------------------------------------------------------------------------------------

type stack struct {
	App      *apitest.App
	Tokens   *fakeTokens
	Limiter  *memLimiter
	DB       *seedfake.DB
	Set      *repositories.Set
	Provider *localize.Provider
	Public   *assemble.Assembler
}

type opts struct {
	set     *repositories.Set // nil = the in-memory bundle
	redis   redis.Cmdable
	shared  assemble.SharedSource
	tokens  previewrepos.TokenRepository
	limiter ratelimit.Limiter
	keys    rediskeys.Keys
	svc     func(*previewservices.Deps)
}

func newStack(t testing.TB, o ...func(*opts)) *stack {
	t.Helper()
	cfg := opts{keys: rediskeys.New("test-preview:")}
	st := &stack{Tokens: newFakeTokens(), Limiter: newMemLimiter()}
	cfg.tokens, cfg.limiter = st.Tokens, st.Limiter
	for _, f := range o {
		f(&cfg)
	}
	set := cfg.set
	if set == nil {
		st.DB = seedfake.Load(t)
		set = st.DB.Set()
	}
	return st.build(t, set, cfg)
}

func (st *stack) build(t testing.TB, set *repositories.Set, cfg opts) *stack {
	locales := localize.NewProvider(func(ctx context.Context) ([]localize.Locale, error) {
		rows, err := set.Locales.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]localize.Locale, 0, len(rows))
		for _, l := range rows {
			fb := ""
			if l.FallbackCode != nil {
				fb = *l.FallbackCode
			}
			out = append(out, localize.Locale{Code: l.Code, FallbackCode: fb, IsDefault: l.IsDefault})
		}
		return out, nil
	}, localize.DefaultProviderTTL)
	deps := assemble.Deps{
		Locales: set.Locales, Pages: set.Pages, Posts: set.Posts, Taxonomy: set.Taxonomy, Albums: set.Albums, FAQ: set.FAQ,
		Forms: set.Forms, Settings: set.Settings, Media: media.NewBuilder(media.Config{}), MediaLookup: repositories.NewMediaLookup(set.Assets),
		Shared: cfg.shared,
	}
	st.Public, st.Set, st.Provider = assemble.New(deps), set, locales
	lookup := repositories.NewMediaLookup(set.Assets)
	composer := blogservices.NewComposer(blogservices.Deps{Content: set, Media: media.NewBuilder(media.Config{}), Lookup: lookup, Locales: locales})
	d := previewservices.Deps{
		Posts:  previewservices.NewPostRenderer(previewservices.PostSources{Set: set, Composer: composer}),
		Tokens: cfg.tokens, Assembler: assemble.New(deps), Chains: locales, Limiter: cfg.limiter, Keys: cfg.keys, Redis: cfg.redis,
	}
	if cfg.svc != nil {
		cfg.svc(&d)
	}
	ctrl := previewcontroller.NewPreviewCtrl(previewservices.NewPreviewService(d))
	st.App = apitest.New(func(api *routepolicy.Group) { previewroutes.Register(api, ctrl) })
	return st
}

// snapshotOf returns the bytes of the bundled page source: the image doc/04 section 5.2 describes.
func snapshotOf(t testing.TB, key string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(seedfake.Dir(), "source", "page."+key+".json")) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// issue registers a token for a page snapshot and returns the token.
func (st *stack) issue(seed string, entityID int64, locale string, snapshot []byte) string {
	tok := makeToken(seed)
	var loc *string
	if locale != "" {
		loc = &locale
	}
	st.Tokens.add(tok, previewrepos.Consumed{EntityType: "cms.pages", EntityID: entityID, LocaleCode: loc, Snapshot: snapshot}, time.Hour)
	return tok
}

func (st *stack) resolve(t testing.TB, token string, query string, extra ...string) *apitest.Response {
	t.Helper()
	h := append([]string{}, extra...)
	if token != "" {
		h = append(h, tokenHeader+": "+token)
	}
	return st.App.Get(t, "/api/v1/preview/resolve"+query, h...)
}

func okStatus(r *apitest.Response) bool { return r.Status == http.StatusOK }

func sha256sum(s string) [32]byte { return sha256.Sum256([]byte(s)) }
