package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// blankTemplate is the shape of the user's config.yaml: keys only, no values, and none of the new keys of
// doc/08 section 2.9 (plus sections the portal ignores).
const blankTemplate = `
server:
  port:
  readTimeout:
  writeTimeout:
database:
  pool:
    maxOpenConns:
  admin:
    host:
    port:
    user:
    password:
    dbname:
    sslmode:
    timezone:
  sell:
    host:
redis:
  host:
  port:
  clearOnStart:
jwt:
  secret:
  refreshSecret:
mailer:
  host:
bootstrap:
  adminUsername:
  adminPassword:
`

// goodKey1/goodKey2 are throw-away test values of 32+ printable bytes.
const (
	goodKey1 = "test-key-0123456789abcdef0123456789-A"
	goodKey2 = "test-key-0123456789abcdef0123456789-B"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// clearEnv blanks every variable a test may inherit from the developer's shell (t.Setenv restores them).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		for _, p := range []string{"SERVER_", "DATABASE_", "REDIS_", "PORTAL_", "FILE_", "MEDIA_", "CACHE_", "RATELIMIT_", "FORMS_", "PREVIEW_", "SITE_", "METRICS_"} {
			if strings.HasPrefix(k, p) {
				t.Setenv(k, "")
			}
		}
	}
}

// validEnv is a complete, valid production configuration built in code (no file).
func validEnv() *Env {
	e := &Env{
		Server:   ServerConfig{Port: 3000, Env: "prod"},
		Database: DatabaseConfig{Admin: DBConnection{Host: "h", Port: 5432, User: "u", Password: "p", DBName: "d", SSLMode: "require"}},
		Redis:    RedisConfig{Host: "r", Port: 6379},
		Portal:   PortalConfig{FrontendKeys: []string{goodKey1}},
	}
	e.Defaults()
	return e
}

func problems(t *testing.T, e *Env) []string {
	t.Helper()
	_, err := e.Validate()
	if err == nil {
		return nil
	}
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("Validate returned %T", err)
	}
	return ce.Problems
}

func hasProblem(ps []string, substr string) bool {
	for _, p := range ps {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

// Every key of doc/08 section 2.9 must be reachable by an environment variable although the template has
// none of them.
func TestEnvOverridesBlankTemplate(t *testing.T) {
	clearEnv(t)
	dir := writeConfig(t, blankTemplate)
	t.Setenv("SERVER_PORT", "4100")
	t.Setenv("SERVER_ENV", "dev")
	t.Setenv("SERVER_CORSORIGINS", "http://a.test, http://b.test")
	t.Setenv("SERVER_TRUSTEDPROXIES", "10.0.0.1,10.0.0.2")
	t.Setenv("SERVER_PROXYHEADER", "X-Forwarded-For")
	t.Setenv("DATABASE_ADMIN_HOST", "db.example.test")
	t.Setenv("DATABASE_ADMIN_PORT", "5433")
	t.Setenv("PORTAL_FRONTENDKEYS", goodKey1+", "+goodKey2)
	t.Setenv("FILE_PATH_IP", "https://files.example.test/")
	t.Setenv("MEDIA_STATICPREFIX", "assets/")
	t.Setenv("CACHE_ENABLED", "false")
	t.Setenv("CACHE_TTL", "2h")
	t.Setenv("RATELIMIT_READPERMINUTE", "120")
	t.Setenv("FORMS_DAILYLIMIT", "7")
	t.Setenv("FORMS_MINFILLSECONDS", "5")
	t.Setenv("FORMS_MAXCLOCKSKEW", "1m")
	t.Setenv("FORMS_KEYPERMINUTE", "11")
	t.Setenv("FORMS_KEYPERDAY", "222")
	t.Setenv("FORMS_DEDUPETTL", "45s")
	t.Setenv("PREVIEW_CACHETTL", "30s")
	t.Setenv("SITE_DEFAULTBASEURL", "https://materm.example.test")
	t.Setenv("REDIS_CLEARONSTART", "true")

	env, err := LoadEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	if env.Server.Port != 4100 || env.Server.Env != "dev" || env.Server.ProxyHeader != "X-Forwarded-For" {
		t.Errorf("server: %+v", env.Server)
	}
	if got := strings.Join(env.Server.CORSOrigins, "|"); got != "http://a.test|http://b.test" {
		t.Errorf("corsOrigins = %q", got)
	}
	if got := strings.Join(env.Server.TrustedProxies, "|"); got != "10.0.0.1|10.0.0.2" {
		t.Errorf("trustedProxies = %q", got)
	}
	if a := env.Database.Admin; a.Host != "db.example.test" || a.Port != 5433 {
		t.Errorf("admin: %+v", a)
	}
	// PORTAL_FRONTENDKEYS: split on the comma, spaces trimmed => two keys (rotation)
	if got := env.Portal.FrontendKeys; len(got) != 2 || got[0] != goodKey1 || got[1] != goodKey2 {
		t.Errorf("frontendKeys: %d entries", len(got))
	}
	if env.File.PathIP != "https://files.example.test/" || env.Media.StaticPrefix != "assets/" {
		t.Errorf("file/media: %+v %+v", env.File, env.Media)
	}
	if env.Cache.Enabled || env.Cache.TTL != 2*time.Hour {
		t.Errorf("cache: %+v", env.Cache)
	}
	if env.RateLimit.ReadPerMinute != 120 {
		t.Errorf("rateLimit: %+v", env.RateLimit)
	}
	if f := env.Forms; f.DailyLimit != 7 || f.MinFillSeconds != 5 || f.MaxClockSkew != time.Minute || f.KeyPerMinute != 11 || f.KeyPerDay != 222 || f.DedupeTTL != 45*time.Second {
		t.Errorf("forms: %+v", f)
	}
	if env.Preview.CacheTTL != 30*time.Second || env.Site.DefaultBaseURL != "https://materm.example.test" || !env.Redis.ClearOnStart {
		t.Errorf("preview/site/redis: %+v %+v %+v", env.Preview, env.Site, env.Redis)
	}
}

// The registered defaults of doc/08 section 2.9, and the pool defaults of doc/08 section 2.6.
func TestDefaults(t *testing.T) {
	clearEnv(t)
	env, err := LoadEnv(writeConfig(t, blankTemplate))
	if err != nil {
		t.Fatal(err)
	}
	if env.Server.Env != EnvProd || !env.Strict() {
		t.Errorf("a blank server.env must mean prod, got %q", env.Server.Env)
	}
	if env.Server.ReadTimeout != 30*time.Second || env.Server.WriteTimeout != 30*time.Second {
		t.Errorf("timeouts: %+v", env.Server)
	}
	if len(env.Server.CORSOrigins) != 0 || len(env.Portal.FrontendKeys) != 0 {
		t.Errorf("cors/keys must default to empty: %+v %+v", env.Server.CORSOrigins, env.Portal.FrontendKeys)
	}
	if p := env.Database.Pool; p.MaxOpenConns != 15 || p.MaxIdleConns != 5 || p.ConnMaxLifetimeMinutes != 30 || p.ConnMaxIdleTimeMinutes != 5 {
		t.Errorf("pool: %+v", p)
	}
	if env.Media.StaticPrefix != "static/" || !env.Cache.Enabled || env.Cache.TTL != 24*time.Hour || env.RateLimit.ReadPerMinute != 300 {
		t.Errorf("media/cache/rateLimit: %+v %+v %+v", env.Media, env.Cache, env.RateLimit)
	}
	if f := env.Forms; f.DailyLimit != 20 || f.MinFillSeconds != 3 || f.MaxClockSkew != 10*time.Minute || f.KeyPerMinute != 30 || f.KeyPerDay != 1000 || f.DedupeTTL != 120*time.Second {
		t.Errorf("forms: %+v", f)
	}
	if env.Preview.CacheTTL != 10*time.Second || env.Site.DefaultBaseURL != "" || env.File.PathIP != "" {
		t.Errorf("preview/site/file: %+v %+v %+v", env.Preview, env.Site, env.File)
	}
}

func TestLoadEnvMissingFile(t *testing.T) {
	if _, err := LoadEnv(t.TempDir()); err == nil {
		t.Fatal("a missing config.yaml must be an error")
	}
}

func TestValidateAcceptsAGoodProdConfig(t *testing.T) {
	warnings, err := validEnv().Validate()
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if !hasProblem(warnings, "trustedProxies") {
		t.Errorf("a prod config without trusted proxies must warn, got %v", warnings)
	}
}

func TestValidateFailFast(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Env)
		want   string
	}{
		"bad env":         {func(e *Env) { e.Server.Env = "qa" }, "server.env"},
		"no port":         {func(e *Env) { e.Server.Port = 0 }, "server.port"},
		"db missing":      {func(e *Env) { e.Database.Admin = DBConnection{} }, "database.admin: missing host, port, user, password, dbname"},
		"sslmode disable": {func(e *Env) { e.Database.Admin.SSLMode = "disable" }, "database.admin.sslmode"},
		"no redis":        {func(e *Env) { e.Redis = RedisConfig{} }, "redis: host and port"},
		"clearOnStart":    {func(e *Env) { e.Redis.ClearOnStart = true }, "redis.clearOnStart"},
		"no keys":         {func(e *Env) { e.Portal.FrontendKeys = nil }, "portal.frontendKeys: at least one"},
		"three keys":      {func(e *Env) { e.Portal.FrontendKeys = []string{goodKey1, goodKey2, goodKey1 + "x"} }, "at most 2"},
		"short key":       {func(e *Env) { e.Portal.FrontendKeys = []string{"short"} }, "too short"},
		"duplicate keys":  {func(e *Env) { e.Portal.FrontendKeys = []string{goodKey1, goodKey1} }, "duplicates"},
		"key with space":  {func(e *Env) { e.Portal.FrontendKeys = []string{goodKey1 + " has a space"} }, "printable ASCII"},
		"cors wildcard":   {func(e *Env) { e.Server.CORSOrigins = []string{"*"} }, "wildcard"},
		"cors with path":  {func(e *Env) { e.Server.CORSOrigins = []string{"https://a.test/app"} }, "origin like"},
		"cors no scheme":  {func(e *Env) { e.Server.CORSOrigins = []string{"a.test"} }, "origin like"},
		"path_ip":         {func(e *Env) { e.File.PathIP = "ftp://x" }, "file.path_ip"},
		"media base url":  {func(e *Env) { e.Media.BaseURL = "ftp://x" }, "media.baseUrl"},
		"base url":        {func(e *Env) { e.Site.DefaultBaseURL = "not a url" }, "site.defaultBaseUrl"},
		"key caps":        {func(e *Env) { e.Forms.KeyPerMinute, e.Forms.KeyPerDay = 50, 10 }, "forms.keyPerMinute"},
		"dedupe ttl":      {func(e *Env) { e.Forms.DedupeTTL = 48 * time.Hour }, "forms.dedupeTTL"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := validEnv()
			tc.mutate(e)
			if ps := problems(t, e); !hasProblem(ps, tc.want) {
				t.Errorf("want a problem containing %q, got %v", tc.want, ps)
			}
		})
	}
}

// Dev is lenient where the doc says so: blank keys are allowed (the server then fails closed), sslmode
// disable and clearOnStart only warn, a blank Redis is fine.
func TestValidateDevIsLenient(t *testing.T) {
	e := validEnv()
	e.Server.Env = EnvDev
	e.Portal.FrontendKeys = nil
	e.Database.Admin.SSLMode = "disable"
	e.Redis = RedisConfig{ClearOnStart: true}
	warnings, err := e.Validate()
	if err != nil {
		t.Fatalf("dev config rejected: %v", err)
	}
	for _, want := range []string{"portal.frontendKeys", "sslmode", "clearOnStart"} {
		if !hasProblem(warnings, want) {
			t.Errorf("missing a warning about %s: %v", want, warnings)
		}
	}
	// but a malformed key is an error in dev as well
	e.Portal.FrontendKeys = []string{"short"}
	if !hasProblem(problems(t, e), "too short") {
		t.Error("a short key must be refused in dev too")
	}
}

// Messages name keys and rules, never a value: neither the keys, nor the password, nor the CORS origin.
func TestValidateMessagesNeverContainValues(t *testing.T) {
	e := validEnv()
	e.Database.Admin.Password = "s3cr3t-db-password-value"
	e.Database.Admin.SSLMode = "disable"
	e.Portal.FrontendKeys = []string{"short-secret-key-value", goodKey1, goodKey1}
	e.Server.CORSOrigins = []string{"https://leak.example.test/path"}
	e.File.PathIP = "ftp://leak-file.example.test"
	warnings, err := e.Validate()
	if err == nil {
		t.Fatal("expected problems")
	}
	all := err.Error() + " " + strings.Join(warnings, " ")
	for _, secret := range []string{"s3cr3t-db-password-value", "short-secret-key-value", goodKey1, "leak.example.test", "leak-file.example.test"} {
		if strings.Contains(all, secret) {
			t.Errorf("a value leaked into the validation messages: %q", secret)
		}
	}
}

func TestDBConnectionHelpers(t *testing.T) {
	c := DBConnection{Host: "h", Port: 5432, User: "secret-user", Password: "secret-pw", DBName: "d"}
	if got := c.Target(); got != "h:5432/d" || strings.Contains(got, "secret") {
		t.Errorf("Target = %q", got)
	}
	if m := (DBConnection{}).Missing(); len(m) != 5 {
		t.Errorf("Missing = %v", m)
	}
}

func TestInvalidationDefaultsAndEnvOverride(t *testing.T) {
	clearEnv(t)
	env, err := LoadEnv(writeConfig(t, blankTemplate))
	if err != nil {
		t.Fatal(err)
	}
	if env.Cache.InvalidateKey != "" || env.Cache.InvalidateChannel != "materm:cms.invalidate" || env.Cache.InvalidateMaxAge != 10*time.Minute {
		t.Errorf("defaults: %+v", env.Cache)
	}
	t.Setenv("CACHE_INVALIDATEKEY", " "+goodKey1+" ")
	t.Setenv("CACHE_INVALIDATECHANNEL", "mine:chan")
	t.Setenv("CACHE_INVALIDATEMAXAGE", "2m")
	env, err = LoadEnv(writeConfig(t, blankTemplate))
	if err != nil {
		t.Fatal(err)
	}
	if env.Cache.InvalidateKey != goodKey1 || env.Cache.InvalidateChannel != "mine:chan" || env.Cache.InvalidateMaxAge != 2*time.Minute {
		t.Errorf("env override: %+v", env.Cache)
	}
}

func TestValidateInvalidation(t *testing.T) {
	// blank key: a warning outside dev (the process still starts), a different warning in dev
	e := validEnv()
	warnings, err := e.Validate()
	if err != nil || !hasProblem(warnings, "cache.invalidateKey: blank, the cache invalidation subscriber is off") {
		t.Errorf("prod blank key: %v %v", warnings, err)
	}
	e.Server.Env = EnvDev
	warnings, _ = e.Validate()
	if !hasProblem(warnings, "UNSIGNED") {
		t.Errorf("dev blank key: %v", warnings)
	}
	// a good key is silent
	e = validEnv()
	e.Cache.InvalidateKey = goodKey2
	warnings, err = e.Validate()
	if err != nil || hasProblem(warnings, "invalidateKey") {
		t.Errorf("good key: %v %v", warnings, err)
	}
	for name, tc := range map[string]struct {
		mutate func(*Env)
		want   string
	}{
		"short key":    {func(e *Env) { e.Cache.InvalidateKey = "short" }, "cache.invalidateKey: too short"},
		"key space":    {func(e *Env) { e.Cache.InvalidateKey = goodKey1 + " x" }, "cache.invalidateKey: only printable"},
		"bad channel":  {func(e *Env) { e.Cache.InvalidateChannel = "has space" }, "cache.invalidateChannel"},
		"glob channel": {func(e *Env) { e.Cache.InvalidateChannel = "materm:*" }, "cache.invalidateChannel"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := validEnv()
			tc.mutate(bad)
			ps := problems(t, bad)
			if !hasProblem(ps, tc.want) {
				t.Errorf("want %q, got %v", tc.want, ps)
			}
		})
	}
	// the value never appears in a message
	e = validEnv()
	e.Cache.InvalidateKey = "leaky-short"
	_, err = e.Validate()
	if err == nil || strings.Contains(err.Error(), "leaky-short") {
		t.Errorf("value leaked or no error: %v", err)
	}
	e = validEnv()
	e.Cache.InvalidateMaxAge = 2 * time.Hour
	if w, _ := e.Validate(); !hasProblem(w, "invalidateMaxAge") {
		t.Errorf("max age warning: %v", w)
	}
}

func TestMetricsConfig(t *testing.T) {
	if e := validEnv(); e.Metrics.Enabled() || e.Metrics.Bind != "127.0.0.1" {
		t.Fatalf("metrics must be off and loopback by default: %+v", e.Metrics)
	}
	for name, tc := range map[string]struct {
		mutate func(*Env)
		want   string
	}{
		"same as public": {func(e *Env) { e.Metrics.Port = e.Server.Port }, "metrics.port: must differ"},
		"negative":       {func(e *Env) { e.Metrics.Port = -1 }, "metrics.port"},
		"too big":        {func(e *Env) { e.Metrics.Port = 70000 }, "metrics.port"},
		"bad bind":       {func(e *Env) { e.Metrics.Port, e.Metrics.Bind = 9100, "not-an-ip" }, "metrics.bind"},
	} {
		t.Run(name, func(t *testing.T) {
			e := validEnv()
			tc.mutate(e)
			if ps := problems(t, e); !hasProblem(ps, tc.want) {
				t.Errorf("want %q, got %v", tc.want, ps)
			}
		})
	}
	good := validEnv()
	good.Metrics.Port, good.Metrics.Bind = 9100, "0.0.0.0"
	if ps := problems(t, good); len(ps) != 0 {
		t.Errorf("valid metrics config rejected: %v", ps)
	}

	// reachable from the environment although the template has no such key
	clearEnv(t)
	t.Setenv("METRICS_PORT", "9100")
	t.Setenv("METRICS_BIND", "10.0.0.5")
	loaded, err := LoadEnv(writeConfig(t, blankTemplate))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Metrics.Port != 9100 || loaded.Metrics.Bind != "10.0.0.5" {
		t.Errorf("env override: %+v", loaded.Metrics)
	}
}

// MEDIA_BASE_URL (media.baseUrl) is the media base; file.path_ip stays as the fallback.
func TestMediaBaseURL(t *testing.T) {
	clearEnv(t)
	e := &Env{}
	if got := e.MediaBaseURL(); got != "" {
		t.Fatalf("blank config: got %q", got)
	}
	e.File.PathIP = "http://old.test"
	if got := e.MediaBaseURL(); got != "http://old.test" {
		t.Fatalf("fallback to file.path_ip: got %q", got)
	}
	e.Media.BaseURL = " http://files.test "
	if got := e.MediaBaseURL(); got != "http://files.test" {
		t.Fatalf("media.baseUrl wins: got %q", got)
	}

	t.Setenv("MEDIA_BASE_URL", "http://env.test:3310")
	dir := writeConfig(t, blankTemplate)
	env, err := LoadEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	if env.Media.BaseURL != "http://env.test:3310" || env.MediaBaseURL() != "http://env.test:3310" {
		t.Fatalf("MEDIA_BASE_URL not read: %q", env.Media.BaseURL)
	}
}
