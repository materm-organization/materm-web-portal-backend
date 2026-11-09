package bootstrap

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Env mirrors config.yaml. Keys are camelCase in the file; viper lower-cases keys and mapstructure matches
// case-insensitively, so the tags below are written exactly as they appear in config.yaml.
//
// Any value can be overridden by an environment variable: the key path is joined with "_" and upper-cased
// (e.g. DATABASE_ADMIN_HOST, SERVER_PORT, PORTAL_FRONTENDKEYS).
//
// config.yaml is the user's blank template. Keys the template does not have (server.env, server.corsOrigins,
// portal.*, media.*, cache.*, ...: doc/08 section 2.9) are registered with viper SetDefault in registerDefaults:
// Unmarshal only sees known keys, so without that registration an environment override of such a key would
// be ignored silently. Sections the portal does not use (database.sell/erp/device, mailer, jwt, bootstrap)
// are not part of Env at all: whatever the template holds there is ignored.
type Env struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Portal   PortalConfig   `mapstructure:"portal"`
	File     FileConfig     `mapstructure:"file"`
	Media    MediaConfig    `mapstructure:"media"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Forms    FormsConfig    `mapstructure:"forms"`
	Preview  PreviewConfig  `mapstructure:"preview"`
	Site     SiteConfig     `mapstructure:"site"`
	Metrics  MetricsConfig  `mapstructure:"metrics"`
}

// Deployment environments of server.env.
const (
	EnvDev     = "dev"
	EnvStaging = "staging"
	EnvProd    = "prod"
)

// ServerConfig holds HTTP server settings.
//
// ReadTimeout and WriteTimeout are decoded by viper's default hook, so they must be written as Go duration
// strings in config.yaml (e.g. "30s", "1m"). A bare number such as 30 would be read as 30 nanoseconds. Zero
// or blank becomes 30s in Defaults.
//
// Env is the deployment mode: dev | staging | prod. A blank value means prod, so a forgotten SERVER_ENV can
// only make start-up stricter. CORSOrigins is a comma separated list when set from the environment; blank
// means no CORS answer at all (every caller is another server, doc/08 section 2.9). TrustedProxies /
// ProxyHeader decide where the connection's client IP comes from: leave them blank to ignore forwarding
// headers.
type ServerConfig struct {
	Port           int           `mapstructure:"port"`
	ReadTimeout    time.Duration `mapstructure:"readTimeout"`
	WriteTimeout   time.Duration `mapstructure:"writeTimeout"`
	Env            string        `mapstructure:"env"`
	CORSOrigins    []string      `mapstructure:"corsOrigins"`
	TrustedProxies []string      `mapstructure:"trustedProxies"`
	ProxyHeader    string        `mapstructure:"proxyHeader"`
}

// Label is the environment name for log lines: a blank server.env means prod.
func (s ServerConfig) Label() string {
	if s.Env == "" {
		return "prod"
	}
	return s.Env
}

// DatabaseConfig holds the shared pool settings and the one connection the portal uses. Admin is the
// historical key name (the template is shared with the admin backend); for the portal it is the READ-ONLY
// role cms_portal_ro (doc/08 section 2.6). The portal never migrates, seeds or writes through GORM models.
// The template's sell/erp/device connections are not read.
type DatabaseConfig struct {
	Pool  PoolConfig   `mapstructure:"pool"`
	Admin DBConnection `mapstructure:"admin"`
}

// PoolConfig holds database/sql pool settings. A zero value becomes the default in Defaults
// (15 open / 5 idle / 30 min lifetime / 5 min idle time: doc/03 section 12, doc/08 section 2.6).
type PoolConfig struct {
	MaxOpenConns           int `mapstructure:"maxOpenConns"`
	MaxIdleConns           int `mapstructure:"maxIdleConns"`
	ConnMaxLifetimeMinutes int `mapstructure:"connMaxLifetimeMinutes"`
	ConnMaxIdleTimeMinutes int `mapstructure:"connMaxIdleTimeMinutes"`
}

// DBConnection describes one PostgreSQL connection. SSLMode and Timezone are optional and are omitted from
// the DSN when empty.
type DBConnection struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	SSLMode  string `mapstructure:"sslmode"`
	Timezone string `mapstructure:"timezone"`
}

// Missing returns the names (never the values) of the fields a usable connection needs but lacks.
func (c DBConnection) Missing() []string {
	var m []string
	if c.Host == "" {
		m = append(m, "host")
	}
	if c.Port == 0 {
		m = append(m, "port")
	}
	if c.User == "" {
		m = append(m, "user")
	}
	if c.Password == "" {
		m = append(m, "password")
	}
	if c.DBName == "" {
		m = append(m, "dbname")
	}
	return m
}

// Target is "host:port/dbname" for messages; it never contains the user or password.
func (c DBConnection) Target() string {
	return fmt.Sprintf("%s:%d/%s", c.Host, c.Port, c.DBName)
}

// RedisConfig addresses the (shared) Redis. ClearOnStart deletes only the keys under this service's prefix
// (materm-portal:) and only in dev.
type RedisConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Username     string `mapstructure:"username"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	ClearOnStart bool   `mapstructure:"clearOnStart"`

	// Fast-fail settings (B1 of the first cross-system run): a Redis outage must cost milliseconds per request,
	// never a dial retry loop. They come from SetDefault (env REDIS_DIALTIMEOUT, ...), not from config.yaml.
	// DialTimeout / ReadTimeout / WriteTimeout bound one attempt; MaxRetries is the number of EXTRA attempts of
	// a failed command (0 = one attempt); BreakerCooldown is how long the circuit stays open after a failure
	// before one request probes Redis again (pkg/redisguard).
	DialTimeout     time.Duration `mapstructure:"dialTimeout"`
	ReadTimeout     time.Duration `mapstructure:"readTimeout"`
	WriteTimeout    time.Duration `mapstructure:"writeTimeout"`
	MaxRetries      int           `mapstructure:"maxRetries"`
	BreakerCooldown time.Duration `mapstructure:"breakerCooldown"`
}

// PortalConfig holds the secret of the server-to-server contract: FrontendKeys are the accepted values of the
// X-Portal-Key header (at most two, so that a key can be rotated without downtime). The values come from the
// environment (PORTAL_FRONTENDKEYS, comma separated) and are never committed, logged or put into an error.
type PortalConfig struct {
	FrontendKeys []string `mapstructure:"frontendKeys"`
}

// FileConfig: the portal needs only the public base URL of the file service to build media URLs
// (doc/08 section 2.9). Env override: FILE_PATH_IP.
type FileConfig struct {
	PathIP string `mapstructure:"path_ip"`
}

// MediaConfig: StaticPrefix marks storage keys of the first-day media that portal-frontend serves from its
// own public/ folder (doc/04 section 8.3).
//
// BaseURL (media.baseUrl, env MEDIA_BASE_URL) is the public base URL that is put in front of a stored file
// path to answer an absolute media URL; it is configuration only and never stored in the database. When it is
// blank the older key file.path_ip is used (see MediaBaseURL).
type MediaConfig struct {
	StaticPrefix string `mapstructure:"staticPrefix"`
	BaseURL      string `mapstructure:"baseUrl"`
}

// MediaBaseURL is the base of every file-service media URL: media.baseUrl (MEDIA_BASE_URL) when set, else
// file.path_ip. Blank = no file service media URLs (only the static/ placeholders get one).
func (e *Env) MediaBaseURL() string {
	if b := strings.TrimSpace(e.Media.BaseURL); b != "" {
		return b
	}
	return strings.TrimSpace(e.File.PathIP)
}

// CacheConfig: Enabled=false turns the response cache off (debugging); TTL is the upper bound of staleness.
//
// The invalidate* keys configure the subscriber that clears the cache when the admin backend publishes a
// signed message (doc/08 section 2.7, doc/10 R-017): InvalidateKey is the HMAC secret shared with the admin
// relay (>= 32 printable ASCII bytes; env CACHE_INVALIDATEKEY, never committed; blank outside dev = the
// subscriber does not start, in dev = it accepts unsigned messages), InvalidateChannel the Redis channel and
// InvalidateMaxAge how old a message may be before it is rejected as a replay.
type CacheConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	TTL               time.Duration `mapstructure:"ttl"`
	InvalidateKey     string        `mapstructure:"invalidateKey"`
	InvalidateChannel string        `mapstructure:"invalidateChannel"`
	InvalidateMaxAge  time.Duration `mapstructure:"invalidateMaxAge"`
}

// FormsConfig: limits of the contact form that are not in the database (doc/08 section 6.3).
type FormsConfig struct {
	DailyLimit     int           `mapstructure:"dailyLimit"`
	MinFillSeconds int           `mapstructure:"minFillSeconds"`
	MaxClockSkew   time.Duration `mapstructure:"maxClockSkew"`
	// KeyPerMinute / KeyPerDay cap the submissions of ALL visitors together per portal key (doc/10 R-020: a
	// leaked key plus a random X-Client-IP per call defeats the per-visitor counters).
	KeyPerMinute int `mapstructure:"keyPerMinute"`
	KeyPerDay    int `mapstructure:"keyPerDay"`
	// DedupeTTL is how long an identical submission of the same visitor counts as a retry of the first one.
	DedupeTTL time.Duration `mapstructure:"dedupeTTL"`
}

// PreviewConfig: CacheTTL is how long a consumed preview snapshot is kept in Redis; TokenPerMinute caps the
// P-19 calls of one preview token per minute (doc/08 section 2.10: 60) and KeyPerMinute the calls of one portal
// key, all tokens together, so that a caller who sends a different token each time is bounded too.
type PreviewConfig struct {
	CacheTTL       time.Duration `mapstructure:"cacheTTL"`
	TokenPerMinute int           `mapstructure:"tokenPerMinute"`
	KeyPerMinute   int           `mapstructure:"keyPerMinute"`
}

// MetricsConfig: the Prometheus endpoint (Q-B10). Port 0 (the default) = off. When set, GET /metrics is served
// on Bind:Port by a SEPARATE listener (never on server.port): it has no authentication, so keep that port off
// every public route/ingress (loopback by default; in a container set Bind to 0.0.0.0 and expose the port only
// to the monitoring network).
type MetricsConfig struct {
	Port int    `mapstructure:"port"`
	Bind string `mapstructure:"bind"`
}

// Enabled reports whether the metrics listener should start.
func (m MetricsConfig) Enabled() bool { return m.Port > 0 }

// SiteConfig: DefaultBaseURL is the fallback of the site.base_url setting (canonical URLs).
type SiteConfig struct {
	DefaultBaseURL string `mapstructure:"defaultBaseUrl"`
}

// NewEnv loads ./config.yaml (plus environment overrides), applies Defaults and returns the Env. It does
// not validate: call Validate (the server does).
func NewEnv() (*Env, error) { return loadEnv("./") }

// LoadEnv is NewEnv for a config.yaml in dir (tests and tools that do not run in the repository root).
func LoadEnv(dir string) (*Env, error) { return loadEnv(dir) }

// loadEnv reads config.yaml from dir. A missing file is an error (the template is always shipped).
func loadEnv(dir string) (*Env, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(dir)
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	registerDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("can't read config.yaml: %w", err)
	}
	var env Env
	if err := v.Unmarshal(&env); err != nil {
		return nil, fmt.Errorf("environment can't be loaded: %w", err)
	}
	env.Defaults()
	return &env, nil
}

// Default values of the keys of doc/08 section 2.9.
const (
	defaultStaticPrefix = "static/"
	defaultCacheTTL     = 24 * time.Hour
	defaultDailyLimit   = 20
	defaultMinFill      = 3
	defaultMaxClockSkew = 10 * time.Minute
	defaultKeyPerMinute = 30
	defaultKeyPerDay    = 1000
	defaultDedupeTTL    = 120 * time.Second
	defaultPreviewTTL   = 10 * time.Second
	defaultPreviewToken = 60
	defaultPreviewKey   = 300
	defaultMetricsBind  = "127.0.0.1"

	defaultRedisDialTimeout = 200 * time.Millisecond
	defaultRedisIOTimeout   = 300 * time.Millisecond
	defaultRedisBreakerCool = 3 * time.Second
	minRedisTimeout         = 10 * time.Millisecond
	maxRedisTimeout         = 5 * time.Second
	minRedisBreakerCooldown = 500 * time.Millisecond
	maxRedisBreakerCooldown = time.Minute
	maxRedisMaxRetries      = 3

	// DefaultInvalidateChannel equals publiccache.DefaultChannel (a test keeps them in step; bootstrap does not
	// import the cache package).
	DefaultInvalidateChannel = "materm:cms.invalidate"
	defaultInvalidateMaxAge  = 10 * time.Minute
)

// registerDefaults makes the keys the user's template lacks known to viper and gives them their default
// (see Env). A key that is blank in the file keeps the registered default only when the file omits it;
// Defaults repairs blanks and non-positive values afterwards.
func registerDefaults(v *viper.Viper) {
	v.SetDefault("server.env", "")
	v.SetDefault("server.corsOrigins", []string{})
	v.SetDefault("server.trustedProxies", []string{})
	v.SetDefault("server.proxyHeader", "")
	v.SetDefault("portal.frontendKeys", []string{})
	v.SetDefault("file.path_ip", "")
	v.SetDefault("media.staticPrefix", defaultStaticPrefix)
	v.SetDefault("media.baseUrl", "")
	_ = v.BindEnv("media.baseUrl", "MEDIA_BASE_URL")
	v.SetDefault("redis.dialTimeout", defaultRedisDialTimeout)
	v.SetDefault("redis.readTimeout", defaultRedisIOTimeout)
	v.SetDefault("redis.writeTimeout", defaultRedisIOTimeout)
	v.SetDefault("redis.maxRetries", 0)
	v.SetDefault("redis.breakerCooldown", defaultRedisBreakerCool)
	v.SetDefault("cache.enabled", true)
	v.SetDefault("cache.ttl", defaultCacheTTL)
	v.SetDefault("cache.invalidateKey", "")
	v.SetDefault("cache.invalidateChannel", DefaultInvalidateChannel)
	v.SetDefault("cache.invalidateMaxAge", defaultInvalidateMaxAge)
	v.SetDefault("forms.dailyLimit", defaultDailyLimit)
	v.SetDefault("forms.minFillSeconds", defaultMinFill)
	v.SetDefault("forms.maxClockSkew", defaultMaxClockSkew)
	v.SetDefault("forms.keyPerMinute", defaultKeyPerMinute)
	v.SetDefault("forms.keyPerDay", defaultKeyPerDay)
	v.SetDefault("forms.dedupeTTL", defaultDedupeTTL)
	v.SetDefault("preview.cacheTTL", defaultPreviewTTL)
	v.SetDefault("preview.tokenPerMinute", defaultPreviewToken)
	v.SetDefault("preview.keyPerMinute", defaultPreviewKey)
	v.SetDefault("site.defaultBaseUrl", "")
	v.SetDefault("metrics.port", 0)
	v.SetDefault("metrics.bind", defaultMetricsBind)
}

// Defaults fills blank values: pool limits, timeouts and the doc/08 section 2.9 keys. It never invents a
// secret: portal.frontendKeys stays as configured. It is idempotent.
func (e *Env) Defaults() {
	e.Server.Env = strings.ToLower(strings.TrimSpace(e.Server.Env))
	if e.Server.Env == "" {
		e.Server.Env = EnvProd // blank = strict
	}
	if e.Server.ReadTimeout <= 0 {
		e.Server.ReadTimeout = 30 * time.Second
	}
	if e.Server.WriteTimeout <= 0 {
		e.Server.WriteTimeout = 30 * time.Second
	}
	e.Server.CORSOrigins = trimList(e.Server.CORSOrigins)
	e.Server.TrustedProxies = trimList(e.Server.TrustedProxies)
	e.Portal.FrontendKeys = trimList(e.Portal.FrontendKeys)

	p := &e.Database.Pool
	if p.MaxOpenConns <= 0 {
		p.MaxOpenConns = 15
	}
	if p.MaxIdleConns <= 0 {
		p.MaxIdleConns = 5
	}
	if p.ConnMaxLifetimeMinutes <= 0 {
		p.ConnMaxLifetimeMinutes = 30
	}
	if p.ConnMaxIdleTimeMinutes <= 0 {
		p.ConnMaxIdleTimeMinutes = 5
	}

	if e.Redis.DialTimeout <= 0 {
		e.Redis.DialTimeout = defaultRedisDialTimeout
	}
	if e.Redis.ReadTimeout <= 0 {
		e.Redis.ReadTimeout = defaultRedisIOTimeout
	}
	if e.Redis.WriteTimeout <= 0 {
		e.Redis.WriteTimeout = defaultRedisIOTimeout
	}
	if e.Redis.MaxRetries < 0 {
		e.Redis.MaxRetries = 0
	}
	if e.Redis.BreakerCooldown <= 0 {
		e.Redis.BreakerCooldown = defaultRedisBreakerCool
	}

	if strings.TrimSpace(e.Media.StaticPrefix) == "" {
		e.Media.StaticPrefix = defaultStaticPrefix
	}
	if e.Cache.TTL <= 0 {
		e.Cache.TTL = defaultCacheTTL
	}
	e.Cache.InvalidateKey = strings.TrimSpace(e.Cache.InvalidateKey)
	e.Cache.InvalidateChannel = strings.TrimSpace(e.Cache.InvalidateChannel)
	if e.Cache.InvalidateChannel == "" {
		e.Cache.InvalidateChannel = DefaultInvalidateChannel
	}
	if e.Cache.InvalidateMaxAge <= 0 {
		e.Cache.InvalidateMaxAge = defaultInvalidateMaxAge
	}
	if e.Forms.DailyLimit <= 0 {
		e.Forms.DailyLimit = defaultDailyLimit
	}
	if e.Forms.MinFillSeconds <= 0 {
		e.Forms.MinFillSeconds = defaultMinFill
	}
	if e.Forms.MaxClockSkew <= 0 {
		e.Forms.MaxClockSkew = defaultMaxClockSkew
	}
	if e.Forms.KeyPerMinute <= 0 {
		e.Forms.KeyPerMinute = defaultKeyPerMinute
	}
	if e.Forms.KeyPerDay <= 0 {
		e.Forms.KeyPerDay = defaultKeyPerDay
	}
	if e.Forms.DedupeTTL <= 0 {
		e.Forms.DedupeTTL = defaultDedupeTTL
	}
	if e.Preview.CacheTTL <= 0 {
		e.Preview.CacheTTL = defaultPreviewTTL
	}
	if e.Preview.TokenPerMinute <= 0 {
		e.Preview.TokenPerMinute = defaultPreviewToken
	}
	if e.Preview.KeyPerMinute <= 0 {
		e.Preview.KeyPerMinute = defaultPreviewKey
	}
	e.Site.DefaultBaseURL = strings.TrimSpace(e.Site.DefaultBaseURL)
	e.Metrics.Bind = strings.TrimSpace(e.Metrics.Bind)
	if e.Metrics.Bind == "" {
		e.Metrics.Bind = defaultMetricsBind
	}
}

func trimList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Strict reports whether the stricter staging/prod rules apply (anything but dev).
func (e *Env) Strict() bool { return e.Server.Env != EnvDev }

// ConfigError lists the configuration problems; messages name the key and the rule, never a value.
type ConfigError struct{ Problems []string }

func (e *ConfigError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}

// Portal key rules. The key is compared in constant time (pkg/portalkey); these rules keep it unguessable
// and safe to carry in an HTTP header.
const (
	minPortalKeyBytes = 32
	maxPortalKeys     = 2
)

// headerSafe is the character set a portal key may use (printable ASCII without space).
var headerSafe = regexp.MustCompile(`^[\x21-\x7e]+$`)

// Validate checks the config the server needs (doc/08 section 2.9) and returns the warnings plus an error
// that lists every violated rule. Checks that need a connection (Redis reachable, the database role) happen
// at start-up, not here. No message contains a value.
func (e *Env) Validate() (warnings []string, err error) {
	var bad []string
	fail := func(f string, a ...any) { bad = append(bad, fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) }
	strict := e.Strict()

	switch e.Server.Env {
	case EnvDev, EnvStaging, EnvProd:
	default:
		fail("server.env: must be dev, staging or prod")
	}
	if e.Server.Port < 1 || e.Server.Port > 65535 {
		fail("server.port: required, 1-65535 (a blank port makes the server listen on a random port)")
	}

	if m := e.Database.Admin.Missing(); len(m) > 0 {
		fail("database.admin: missing %s", strings.Join(m, ", "))
	}
	switch strings.ToLower(e.Database.Admin.SSLMode) {
	case "disable":
		if strict {
			fail("database.admin.sslmode: disable is not allowed outside dev")
		} else {
			warn("database.admin.sslmode: disable (acceptable for local development only)")
		}
	case "":
		if strict {
			warn("database.admin.sslmode: blank (driver default 'prefer'); use verify-full in production")
		}
	}

	if strict {
		if e.Redis.Host == "" || e.Redis.Port == 0 {
			fail("redis: host and port are required outside dev")
		}
		if e.Redis.ClearOnStart {
			fail("redis.clearOnStart: must be false outside dev (the Redis is shared)")
		}
	} else if e.Redis.ClearOnStart {
		warn("redis.clearOnStart: true clears the materm-portal: key prefix at every start")
	}

	e.validateRedisTimeouts(fail, warn)
	e.validateCORS(fail)
	e.validatePortalKeys(strict, fail, warn)
	e.validateFile(strict, fail, warn)
	e.validateInvalidation(strict, fail, warn)

	if strict && len(e.Server.TrustedProxies) == 0 {
		warn("server.trustedProxies: blank, forwarding headers are ignored (the client address is the proxy's behind a reverse proxy)")
	}
	if c := e.Site.DefaultBaseURL; c != "" {
		if u, perr := url.Parse(c); perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail("site.defaultBaseUrl: must be an http(s) URL with a host")
		}
	}
	if e.Metrics.Port < 0 || e.Metrics.Port > 65535 {
		fail("metrics.port: 0 (off) or 1-65535")
	} else if e.Metrics.Enabled() && e.Metrics.Port == e.Server.Port {
		fail("metrics.port: must differ from server.port (metrics are never served on the public listener)")
	}
	if e.Metrics.Enabled() && net.ParseIP(e.Metrics.Bind) == nil && e.Metrics.Bind != "localhost" {
		fail("metrics.bind: must be an IP address (or localhost)")
	}
	if e.Forms.MaxClockSkew > time.Hour {
		warn("forms.maxClockSkew: longer than 1h weakens the rendered_at check")
	}
	if e.Forms.KeyPerMinute > e.Forms.KeyPerDay {
		fail("forms.keyPerMinute: must not exceed forms.keyPerDay")
	}
	if e.Forms.DedupeTTL > 24*time.Hour {
		fail("forms.dedupeTTL: at most 24h (an identical message sent again after that is a new submission)")
	}

	if len(bad) > 0 {
		return warnings, &ConfigError{Problems: bad}
	}
	return warnings, nil
}

// validateCORS: blank is valid (no CORS answer). Entries must be origins (scheme://host[:port]), never "*".
func (e *Env) validateCORS(fail func(string, ...any)) {
	for _, o := range e.Server.CORSOrigins {
		if strings.Contains(o, "*") {
			fail("server.corsOrigins: wildcard origins are not allowed")
			return
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			fail("server.corsOrigins: every entry must be an origin like https://host[:port] (no path, query or credentials)")
			return
		}
	}
}

// validatePortalKeys checks portal.frontendKeys. Outside dev at least one key is required. In dev blank keys
// are allowed but the server then refuses every /api/v1 request (fail closed): the warning says so.
func (e *Env) validatePortalKeys(strict bool, fail, warn func(string, ...any)) {
	keys := e.Portal.FrontendKeys
	if len(keys) == 0 {
		if strict {
			fail("portal.frontendKeys: at least one key is required outside dev (set PORTAL_FRONTENDKEYS)")
		} else {
			warn("portal.frontendKeys: blank in dev, every /api/v1 request is refused with 401 (set PORTAL_FRONTENDKEYS to call the API)")
		}
		return
	}
	if len(keys) > maxPortalKeys {
		fail("portal.frontendKeys: at most %d keys (current and next, for rotation)", maxPortalKeys)
	}
	seen := make(map[[sha256.Size]byte]struct{}, len(keys))
	for i, k := range keys {
		switch {
		case len(k) < minPortalKeyBytes:
			fail("portal.frontendKeys[%d]: too short (min %d bytes)", i, minPortalKeyBytes)
		case !headerSafe.MatchString(k):
			fail("portal.frontendKeys[%d]: only printable ASCII without spaces is allowed", i)
		}
		sum := sha256.Sum256([]byte(k))
		if _, dup := seen[sum]; dup {
			fail("portal.frontendKeys[%d]: duplicates another key", i)
		}
		seen[sum] = struct{}{}
	}
}

// validateFile: media.baseUrl and file.path_ip are optional; when set each must be an http(s) URL with a host,
// and outside dev an https one (an http image on an https site is blocked by the browser as mixed content).
// When both are blank the file-service media URLs cannot be built: the media map of a page stays empty
// without any error, so the start warns (the media.baseUrl env is MEDIA_BASE_URL, the admin backend's
// counterpart is file.path_ip).
func (e *Env) validateFile(strict bool, fail, warn func(string, ...any)) {
	for _, c := range []struct{ key, val string }{{"media.baseUrl", e.Media.BaseURL}, {"file.path_ip", e.File.PathIP}} {
		if strings.TrimSpace(c.val) == "" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(c.val))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail("%s: must be an http(s) URL with a host", c.key)
			continue
		}
		if strict && u.Scheme != "https" {
			fail("%s: must be https outside dev (an http image on an https site is blocked as mixed content)", c.key)
		}
	}
	if e.MediaBaseURL() == "" {
		warn("media.baseUrl and file.path_ip are both blank: media files of the file service get no URL and pages show no images; set MEDIA_BASE_URL to the public https origin of the file service")
	}
}

// invalidateChannelPattern is the shape of cache.invalidateChannel (printable, no glob characters).
var invalidateChannelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{0,99}$`)

// validateInvalidation checks the cache.invalidate* keys. Outside dev a blank key is only a warning: the
// subscriber then stays off and entries expire by cache.ttl; the process itself still starts.
func (e *Env) validateInvalidation(strict bool, fail, warn func(string, ...any)) {
	c := e.Cache
	switch k := c.InvalidateKey; {
	case k == "":
		if strict {
			warn("cache.invalidateKey: blank, the cache invalidation subscriber is off (entries expire only by cache.ttl); set CACHE_INVALIDATEKEY")
		} else {
			warn("cache.invalidateKey: blank in dev, the invalidation subscriber accepts UNSIGNED messages")
		}
	case len(k) < minPortalKeyBytes:
		fail("cache.invalidateKey: too short (min %d bytes)", minPortalKeyBytes)
	case !headerSafe.MatchString(k):
		fail("cache.invalidateKey: only printable ASCII without spaces is allowed")
	}
	if !invalidateChannelPattern.MatchString(c.InvalidateChannel) {
		fail("cache.invalidateChannel: must be 1-100 characters of letters, digits and : . _ -")
	}
	if c.InvalidateMaxAge > time.Hour {
		warn("cache.invalidateMaxAge: longer than 1h widens the replay window of invalidation messages")
	}
}

// validateRedisTimeouts checks the fast-fail keys of redis.*: a timeout outside 10ms-5s is refused (too small
// fails healthy requests, too large brings back the slow outage), as is a cool-down outside 500ms-1m.
func (e *Env) validateRedisTimeouts(fail func(string, ...any), warn func(string, ...any)) {
	for _, t := range []struct {
		name string
		d    time.Duration
	}{{"redis.dialTimeout", e.Redis.DialTimeout}, {"redis.readTimeout", e.Redis.ReadTimeout}, {"redis.writeTimeout", e.Redis.WriteTimeout}} {
		if t.d != 0 && (t.d < minRedisTimeout || t.d > maxRedisTimeout) { // 0 = not defaulted yet
			fail("%s: must be between %s and %s", t.name, minRedisTimeout, maxRedisTimeout)
		}
	}
	if e.Redis.MaxRetries > maxRedisMaxRetries {
		fail("redis.maxRetries: must be between 0 and %d", maxRedisMaxRetries)
	}
	if e.Redis.BreakerCooldown != 0 && (e.Redis.BreakerCooldown < minRedisBreakerCooldown || e.Redis.BreakerCooldown > maxRedisBreakerCooldown) {
		fail("redis.breakerCooldown: must be between %s and %s", minRedisBreakerCooldown, maxRedisBreakerCooldown)
	}
	if e.Redis.ReadTimeout > time.Second || e.Redis.DialTimeout > time.Second {
		warn("redis.dialTimeout/readTimeout: above 1s a Redis outage can again exceed the frontend's 2s fetch timeout")
	}
}
