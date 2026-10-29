# CLAUDE.md — Materm Web Portal Backend

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

This repo is the **read API of the public Materm website** (design: `../doc/08-portal-integration.md` section 2; decisions: `../doc/00-decisions.md` section 10.4). It is no longer a skeleton of the old project: the leftover `modules/auth`, `pkg/middleware` and the `jwt.*`/`GlobalEnv` code are gone. State: PB-1 (cleanup + shared kernel), PB-2 (bootstrap, portal key, probes), PB-3 (read structs of the 21 views, `pkg/localize`, `pkg/media`, `pkg/richtext`), PB-4 (`pkg/publiccache` + the `cms.invalidate` subscriber) and the milestone-M2 content modules (PB-5 to PB-10: `site`, `pages` + `pkg/assemble`, `seo`, `blog`, `gallery`, `faq`, `forms`; all wired in `cmd/application` by T1.2.12) are done: 16 public endpoints under `/api/v1/public`, the preview endpoint `GET /api/v1/preview/resolve` (P-19, PB-11, milestone M4: `modules/preview`, `pkg/assemble/raw.go`; 17 endpoints) plus the probes. Preview of POSTS (`cms.posts` snapshots, doc/05 PT-18) is built (T1.5.8, M5): `kind:"post"`, see Preview below.

Rules that shape everything here:

- **Server-to-server only.** The only caller is the portal-frontend server, which sends `X-Portal-Key`. Every route under `/api/v1` needs it (guard mounted once on the group, so even unknown paths are 401 without it); only `/health` and `/ready` are open. `/ready` answers 200 `degraded` when only Redis is down and 503 only when the database is down - "down" means the connection fails OR the role cannot read the view `v_public_locales` (the only object it touches; never a base table) - because the portal reads from the database and the cache just degrades. There is no user, no JWT, no login.
- **Read-only.** The database role is `cms_portal_ro` (SELECT on the `v_public_*`/`v_published_*` views, EXECUTE on `forms_submit_form` and `cms_consume_preview_token`). The portal **never migrates, seeds or writes through GORM models**: no `AutoMigrate`, no SQL runner. The admin backend owns the schema.
- **Same error/response shapes as the admin backend.** `pkg/apperror`, `pkg/response`, `pkg/pagination`, `pkg/requestid` are copies of the admin packages and are kept identical on purpose (doc/06 section 1.3): change them in both repos or in neither.
- Hard rules for agents: never connect to the database `materm` / container `postgres-maxdb` / `redis-stack`; never print or commit `config.yaml` values, `.env*`, `config.enc.yaml`.

## Commands

See `Makefile`: `run`, `build`, `test`, `test-db`, `testdb-up`, `testdb-status`, `lint`, `check-config`, `test-coverage`, `encrypt`, `decrypt`, `docker-test*`, `swag`.

- `make test` — unit tests of every package (`./...`), no database, no Redis.
- `make test-db` — adds the tests with build tag `dbtest` (`go test -tags=dbtest ./...`). They run **only** on the scratch Postgres (127.0.0.1:55433) and Redis (127.0.0.1:56380) that belong to the **admin backend repo** (`../materm-admin-backend/scripts/testdb.sh`). This repo never starts/stops them: `make testdb-up` delegates to the admin Makefile (a no-op when they already run) and `test-db` loads the admin repo's git-ignored `.env.test` into the test command's shell without printing it (`TESTDB_ENV` overrides the path). Tests create random `materm_test_*` databases and random Redis key prefixes. `TestSeededPagesEqualTheFrontendBundle` needs the web content seed that only the admin backend can load: it is skipped unless `MATERM_APP_SEEDED_DSN` points at a scratch database prepared by the admin repo's `bootstrap.Migrate()` + `bootstrap.Seed` (`schemadb.Seeded` applies the same guards and creates only a throw-away `cms_portal_ro` member role). The guards in `pkg/testutil/{dbtest,redistest}` refuse database `materm`, ports 5432/6379 and any cluster without the scratch marker.
- `make lint` — golangci-lint v2 with `--build-tags=dbtest`; linters: errcheck, govet (all but fieldalignment), staticcheck, nilerr, bodyclose, ineffassign, unused, contextcheck, gosec, rowserrcheck, sqlclosecheck (tests are exempt from errcheck/contextcheck/bodyclose/gosec; `pkg/apperror` from gosec G101).
- Single test: `go test -v -run TestName ./path/...`.

> **config.yaml rule:** before every commit clear **all values** (keys only; `make check-config` runs the admin repo's script and fails printing key paths only). It holds real credentials once filled in.
>
> **config.enc.yaml is stale** (old project's schema and age key). Regenerate with `make encrypt` after a Materm age key is in `.sops.yaml`; never `make decrypt` over the new `config.yaml`.

## Configuration

`bootstrap/env.go`: `Env` = `server`, `database` (`pool` + `admin`, the **read-only** `cms_portal_ro` connection; `sell/erp/device`, `jwt`, `mailer`, `bootstrap` in the shared template are ignored), `redis`, `portal`, `file`, `media`, `cache` (also `invalidateKey` / `invalidateChannel` / `invalidateMaxAge`, see Redis), `rateLimit`, `forms`, `preview`, `site` (the keys of doc/08 section 2.9; defaults in the README table). `forms` also has `keyPerMinute` (30), `keyPerDay` (1000: submissions of all visitors together per portal key, R-020) and `dedupeTTL` (120s: an identical retry is answered `received` without a second insert); `Validate` rejects `keyPerMinute > keyPerDay` and `dedupeTTL > 24h`. `config.yaml` itself is NOT changed by these keys (they come from `SetDefault`, so an env var such as `FORMS_KEYPERMINUTE` reaches them). Keys the template lacks are registered with `SetDefault` so an environment variable (`PORTAL_FRONTENDKEYS`, `SERVER_ENV`, ...) reaches them. `Defaults()` repairs blanks; `Validate()` returns warnings plus a `ConfigError` listing every violated rule, **naming keys, never values**.

- `server.env` blank = `prod`; `dev` is the only lenient mode (`Strict()` = not dev). `server.port` is required.
- `portal.frontendKeys`: at most 2 (rotation), each >= 32 printable ASCII bytes, distinct. Required outside dev. Blank in dev is allowed but fail-closed: every `/api/v1` request is 401 (no dev bypass; set `PORTAL_FRONTENDKEYS` to call the API).
- `server.corsOrigins`: blank = **no CORS middleware at all** (Fiber's `cors` turns an empty list into `*`, so it is not mounted); entries must be origins, never `*`. Methods `GET,POST,OPTIONS`, headers `Content-Type,Accept-Language,X-Request-Id`, no credentials; `X-Portal-Key` is deliberately not allowed.
- Outside dev: Redis host/port required, `redis.clearOnStart` and `database.admin.sslmode: disable` are errors. `redis.clearOnStart` in dev deletes only keys under `materm-portal:`.
- Durations need a unit (`30s`).

## Architecture

### Startup flow

`cmd/server/main.go` → `bootstrap.App()` (load + `Validate()` + Fiber + Redis) → `application.Setup(ctx, app)` → graceful shutdown on SIGINT/SIGTERM (15 s). `Setup` (also used by the boot tests) runs in order:

```
SetupRepositories(ctx, env, redis)   // PostgreSQL (+ CheckRuntimeRole), cache repo
  → SetupServices(repos, env)
    → SetupControllers(services)
      → SetupRouter(fiber, controllers, portalkey.Verifier)  // returns the routepolicy Registry and the /api/v1 Group
```

`cmd/application/{repositories,services,controller,router,setup}.go` are the **only** places that wire dependencies by hand (hotspot files: one owner per milestone). `Repositories` carries the Redis handle as `RC` (a NIL interface when Redis is absent, never a typed-nil `*redis.Client`: every consumer takes `RC`), `Keys` (`rediskeys.New("")`) and `Submissions` (the `forms_submit_form` caller); `SetupServices` receives `env` (it needs `site.defaultBaseUrl` and `forms.*`). All content controllers share the ONE `*localize.Provider` (`Services.Locales`). Each module registers its routes in `router.go` on the `api` group returned by `SetupRouter`; the tests that build `Services` by hand use the stubs in `cmd/application/stubs_test.go`, and `TestAllPublicEndpointsAreMounted` pins the list of the 17 endpoints (P-01 ... P-16 and P-19).

### Bootstrap (`bootstrap/`)

| File | Purpose |
|---|---|
| `env.go` | typed config, `SetDefault` registration, `Defaults()`, `Validate()` |
| `app.go` | `App()` → `Application{Env, Fiber, Redis, Swagger}`; swagger config only in non-strict |
| `fiber.go` | `NewFiber`: `requestid` → JSON access log (request_id, method, route template, status, latency_ms, internal; **never** query/body/IP/UA/headers) → recover (stack to log only) → helmet → CORS allow-list (only when configured) → 3 s request deadline (`constants.DBQueryTimeout`, = role `statement_timeout`); body limit 1 MB; trusted proxies only from `server.trustedProxies`; `apperror.Handler` is the ErrorHandler |
| `postgres.go` | `DBConnection.DSN()` (keeps the time-zone fix), `NewPostgresConnection` (quiet GORM logger: Warn level, parameters hidden; password scrubbed from errors), `CheckRuntimeRole` (outside dev a superuser, the database owner or a role that can SELECT `cms_pages` is refused; a role other than `cms_portal_ro` is a warning). **No Migrate.** |
| `redis.go` | `InitializeRedis(cfg, strict)`: strict = error if unreachable; dev = warn and return `(nil, nil)` (degraded: `cache.NewRedisRepository(nil)` is `cache.Unavailable()`) |
| `swagger.go` | `NewSwaggerConfig(port)`; annotations only (not wired until PB-13) |

### Module structure

`modules/<name>/{controller,entities,models,routes,services,repositories}`, package names = subfolder. Layer contract: repositories handle entities only (read-structs of views, `TableName()` returns the view name, no json tags), services map entity → DTO, controllers handle DTOs only. Interfaces `<Name>Ctrl/Service/Repository`, constructors `New<Name>…`. Routes are registered only through a `routepolicy.Group` (below). Current module: `system` (`/health`, `/healthz` liveness — touches nothing; `/ready`, `/readyz` — DB ping ∧ (Redis ping ∨ dev-degraded), 1 s per check in parallel, result cached 5 s, body `{success,data:{status:ok|degraded|down}}`, 503 carries the standard error shape plus `data.status:"down"` and never names a dependency).

### Route policy and portal key

- `pkg/portalkey`: `New(keys)` → `Verifier`; `Match` hashes both sides with sha256 and compares every configured key with `subtle.ConstantTimeCompare` without early exit (no length leak); no configured key = reject everything; `Middleware()` answers 401 `UNAUTHENTICATED` (`Cache-Control: no-store`, same answer for missing/wrong) and sets Locals `internal=true` on success (`portalkey.Internal(c)`). The key is never logged or echoed.
- `pkg/routepolicy`: `NewRoot` (probes, only `Public()`) and `NewAPI(app, "/api/v1", guard, reg)` (guard mounted once on the prefix; only `PortalKey()`). A mismatched policy panics at start-up. `UncoveredRoutes(app, reg)` backs the coverage test (`cmd/application/application_test.go`); `TestRegisteredPoliciesAreEnforced` walks the registry and checks 401/!401 per policy, so adding a module needs no test edit.

### Response / error helpers

`pkg/response` (`OK`, `OKMessage`, `Created`, `List`) and `pkg/apperror` (constructors `NotFound()`, `Validation(...)`, `RateLimited(d)`, ...; handlers just `return` the error, `apperror.Handler` writes `{success:false,message,code,errors?,details?,request_id}`; `message` follows `Accept-Language` en/la, clients decide on `code`). Never write error bodies or `fiber.Map` by hand (the `/ready` 503 is the one documented exception). `pkg/pagination`: offset `ParseValues`/`Spec`/`NewMeta` (422 with `in:"query"` errors, allow-listed sort) and keyset `LoadMoreQuery`.

### Redis

Prefix `materm-portal:` (`constants.AppKeyPrefix`); **never FLUSH** (the Redis is shared with the admin backend, prefix `materm-admin:`): `cache.DeleteAll` deletes by prefix. `pkg/rediskeys` names every key family of doc/08 section 2.7 (`gen`, `c:<gen>:…`, `tag:`, `locales`, `preview:`, `rl:read:`, `rl:form:`, `ipsalt:`); raw IPs are hashed (`ratelimit.Hash`), no key carries personal data. `pkg/ratelimit` (Redis Lua fixed window, `Middleware` with explicit `FailOpen`) is not mounted as middleware: callers always have a key, so only the form limiter (PB-9, fails closed) and the preview limiter (P-19, per token + per portal key, fails OPEN, counters `rl:preview:<hash32>:<minute>` / `rl:preview-key:<keyid16>:<minute>`) use it. `preview:<sha256 hex>` holds the consumed snapshot for `preview.cacheTTL` (10 s).

**Fast-fail on a Redis outage (B1/B4 of the first cross-system run).** The client is built ONLY by `bootstrap.NewRedisClient` (no connect; `InitializeRedis` calls it): `redis.dialTimeout/readTimeout/writeTimeout` (200/300/300 ms), `redis.maxRetries` (0 = one attempt; config 0 is mapped to go-redis `-1` because go-redis reads 0 as 3), `DialerRetries: 1` and `DialerRetryTimeout: 1ms` (go-redis reads 0 as 5 dials and sleeps 100 ms after EVERY failed dial), `PoolTimeout` = read timeout. `pkg/redisguard` adds a circuit breaker as a client hook (so the cache, rate limiters, IP salt, duplicate marker, preview cache and the `/ready` ping all share it): a transport failure (refused, timeout, EOF, LOADING/BUSY/...; NOT `redis.Nil`, other server replies, the caller's own cancelled/expired context, pool wait timeout) opens it for `redis.breakerCooldown` (3 s); open = commands fail at once with `redisguard.ErrOpen` (an ordinary error, so publiccache logs `Postgres (Redis failed)` and answers from the database); after the cool-down ONE request probes (the probe's context is marked so go-redis's connection handshake, which runs through the same hooks, is not refused). `Guard.Epoch()` grows every time it closes after being open; `publiccache.Cache` sees a new epoch at its next snapshot and bumps `gen` once (messages published during the outage are lost, so nothing cached before it may be served after it); the subscriber's resync after a resubscribe bypasses the breaker (`redisguard.WithBypass`) and retries with a back-off. `redisguard.InstallLibraryLogger()` routes go-redis's own log lines through `logs.WarnEvery` (one per kind a minute). Measured: `GET /public/pages/resolve` with Redis down 2.1 s -> 12 ms mean / 25 ms worst. Tests: `pkg/redisguard`, `bootstrap/redis_test.go`, `pkg/publiccache/outage_test.go`, dbtest `cmd/application/redis_outage_db_test.go` (real `Setup` behind a killable TCP proxy to the scratch Redis).

**Cache invalidation (PB-4).** `pkg/publiccache` caches finished response DTOs (`Get[T](ctx, cache, Spec, loader)`; loader returns the value and the public tags it depends on). Redis down / nil / `cache.enabled=false` = the loader answers (singleflight still shares concurrent loads); a Redis error mid-request is logged and the database answer is returned. A load remembers `gen` and `inv` before it reads the database and a single Lua script refuses to store when either moved (doc/10 R-024), so a slow request can never store an answer older than an invalidation. The subscriber (`cmd/application/invalidation.go`, started by `Setup`, stopped by `Runtime.Close`) listens on `cache.invalidateChannel` (default `materm:cms.invalidate`) for `{"payload":"<event JSON string>","sig":"<hex HMAC-SHA256(cache.invalidateKey, payload)>"}`; the event carries a signed `sent_at` (set by the relay at PUBLISH time; `Sign` fills it) and the subscriber rejects a bad signature, a missing `sent_at`, a `sent_at` older than `cache.invalidateMaxAge` (10 min) or more than 1 min in the future, and a repeated `event_id` (`occurred_at` is informational: a relay that was down publishes old events late and they must apply); it acts only on the public tag vocabulary (`publiccache.ValidTag`: the 5 internal shapes `shared_section:`, `menu_id:`, `menu_item:`, `category:`, `collection_id:` are ignored); tags `locales`/`*` bump `gen`; every other tag unlinks its tag set atomically, and again after 2 s (double delete). Every (re)subscribe bumps `gen` (pub/sub does not replay). Blank `cache.invalidateKey`: outside dev the subscriber does not start (warning), in dev it starts and accepts UNSIGNED messages. `publiccache.Sign` builds the envelope for the admin relay (T1.3.1).

### `pkg/`

| Path | Purpose |
|---|---|
| `apperror/` `response/` `pagination/` `requestid/` | shared kernel, identical to the admin backend |
| `logs/` | zap JSON to stdout + `logs/YYYY-MM-DD.log` (`LOG_FILE=off` for containers/tests; with `server.env: dev` stdout shows one readable line per request instead (`15:04:05 | 200 | 1.6s | 127.0.0.1 | GET | /api/v1/... | -`, never the query string; the JSON line then goes to the file only; `LOG_CONSOLE=pretty|off` overrides; `NO_COLOR` turns the colors off)); `Info/Warn/WarnEvery/Error`, field helpers, `WithRequest`/`FromContext` (request id only — no user id here). In dev (`LOG_SOURCE=on|off` overrides) the read-through cache also logs where an answer came from, in the KKL style: `🚀 - Cache Key` (`cache_key`) then `🚀 - <resource> From Redis` / `From Postgres` (`logs.CacheKey`, `logs.Source`; used by every `publiccache.Get`). `make run` also reports each start-up step with an emoji line (`🚀 Starting`, `✅ Config loaded`, `postgres` / `redis connection success`, `cache invalidation subscriber started`, `✅ Modules wired`, `🌐 Listening` with the URL) |
| `portalkey/` `routepolicy/` | see above |
| `ratelimit/` `rediskeys/` `cache/` | Redis building blocks (`rediskeys.Inv()` = invalidation counter) |
| `redisguard/` | circuit breaker hook on the Redis client (fast-fail while Redis is down), throttled go-redis logger |
| `publiccache/` | response cache with generation + tag sets, stampede protection, signed invalidation subscriber (above) |
| `localize/` | `Chain` from `v_public_locales` (never `v_locale_resolution`: not granted), `Negotiate` (`?locale` -> `Accept-Language` -> default; `lo` = `la`; 422 `in:"query"` on a bad code), `Collapse` of `x-localized` leaves with compiled `Hints`, `Provider` (process copy 60 s) |
| `media/` | `Builder.URL/Item/Resolve`: `file.path_ip` + `/` + `storage_key`; keys under `media.staticPrefix` become `/<rest>`; `CollectIDs` finds `media_id` at any depth |
| `richtext/` | `Resolve(doc, Lite\|Full, Resolver)`: allow-list sanitiser producing a JSON doc (no HTML), server-side link resolution, collects image `media_id`s; `CollectLinks` + `MapResolver` for batched lookups |
| `assemble/` | page assembler of P-06: section plan, one resolver per section type, rich-text link batching, the shared `BuildForm` / `BuildFAQItems`, settings and locale helpers (no HTTP: the HTTP helpers are `modules/content/readkit`) |
| `iphash/` `clientip/` | salted daily IP hash (Redis salt, never the raw address in a key) and the visitor address/user agent from the trusted forwarding headers (`X-Client-IP`, `X-Client-User-Agent`) |
| `testutil/schemadb` | builds the FULL reference schema (`../doc/sql/migrations/*.sql` + seeds + `fixture.sql`) on the scratch Postgres as owner `cms_migrator` and gives a read-only member of `cms_portal_ro`; `Shared(t)` once per test binary, `Teardown()` in `TestMain`; `Seeded(t)` opens an externally prepared scratch database (env `MATERM_APP_SEEDED_DSN`, built by the admin repo's Migrate + Seed) with the same guards |
| `testutil/{dbtest,redistest}` | scratch-only DB/Redis helpers with hard guards |
| `constants/` `constants/services/` | `MainRoute`, `HealthCheckRoute`; `AppKeyPrefix`, `RootKey`, `ExpireTime`, `DBQueryTimeout` (3 s) |
| `utilities/{convertor,format,generator,helper,validator}`, `circuitbreaker/`, `httpclient/` | legacy helpers from the old project, kept (validator has `Struct/Issues` for apperror field errors) |

### Tests

`tests/bootstrap` (DSN) plus tests next to the code: env/Validate, pipeline (404 envelope, panic hygiene, request id, body limit over a real listener, CORS incl. empty list, security headers, route template), portalkey, routepolicy, system service/controller, application wiring (`cmd/application`), and every ported package. `dbtest`-tagged: role check, Redis clear-on-start own prefix only, boot against scratch PG/Redis (`/ready` ok/degraded, creates nothing in the database), ratelimit Lua; PB-3/PB-4: the read-only role reads the 21 views and nothing else (and has exactly the privileges of `cms_portal_ro`), every entity matches its view's columns, every repository against fixture rows, the cache flow (hit/miss, tag sets, generation, R-024, double delete, subscriber with signed/bad/stale/replayed messages, resubscribe after a killed connection) on a random Redis prefix, and `Setup` starting/stopping the subscriber.

Test helpers (`pkg/testutil`): `apitest` (the HTTP pipeline with an api group, no database), `portalapp` (the same plus a `Get/Do` client with the portal key), `seedfake` (in-memory repositories over the frontend bundle `materm-web-portal-frontend/src/content/cms-seed`, used by the offline goldens), `jsondiff` (golden comparison with masks/tolerances), `schemadb`, `dbtest`, `redistest`. `tests/publicapi` (site, pages, seo) and `tests/readapi` (blog, gallery, faq, forms) run the modules end to end on the scratch schema; `cmd/application/boot_db_test.go` boots the real `Setup` on it and calls all 17 endpoints.

### `modules/content` (PB-3)

Shared by every content module (a deviation from "one entities/repositories per module": the views are used across endpoints, e.g. assets by pages, posts and gallery). `entities/` = read-structs of the 21 granted views (explicit `gorm:"column:"`, no json tags, nullable columns are pointers, `JSONB`/`StringArray` scanners; `PublishedPostCard` is `v_published_posts` without `body`). `repositories/` = `Set` (`repositories.New(db)`) with one interface per area (Locales, UIStrings, Settings, Menus, Pages, Taxonomy, Posts, Albums, FAQ, Forms, Collections, SEO, Assets); finders return `repositories.ErrNotFound`; every query filters `requested_locale` (the caller passes a locale already normalised by `localize`) and selects explicit columns. `NewMediaLookup(Assets)` adapts to `media.Lookup`. `Setup` exposes `Repositories.{Content,PublicCache,Media}` and `Services.{Locales,Media,MediaLookup}`.

### Content modules (M2)

One folder each under `modules/`, layout `{controller,models,routes,services}` (`forms` also has `repositories` for the one write and `validate`). Handlers wrap every GET in `readkit.Guard` (Cache-Control of error answers) and answer with `readkit.Send` (weak ETag, 304, `Content-Language`, `Vary`). Cache tags follow doc/08 section 2.8.

| Module | Endpoints | Views / function |
|---|---|---|
| `site` | P-01 `/public/locales`, P-02 `/public/settings`, P-03 `/public/ui-strings`, P-04 `/public/menus/:key`, P-05 `/public/site` | `v_public_locales`, `v_public_site_settings`, `v_public_ui_strings`, `v_published_menu_items` |
| `pages` | P-06 `/public/pages/resolve?path=` | `v_published_pages`, `v_published_page_sections`, then what each section type needs via `pkg/assemble` |
| `seo` | P-16 `/public/sitemap` | `v_sitemap` |
| `blog` | P-07 `/public/posts`, P-08 `/public/posts/:slug` (2-step slug, 308), P-09 `/public/categories`, P-10 `/public/tags` | `v_published_posts`, `v_published_categories`, `v_published_tags`, `v_published_post_tags`, `v_published_post_media`, `v_published_post_slugs` |
| `gallery` | P-11 `/public/albums`, P-12 `/public/albums/:key` | `v_published_albums`, `v_published_album_items` |
| `faq` | P-13 `/public/faq/:group_key` | `v_published_faq_items` |
| `forms` | P-14 `/public/forms/:slug`, P-15 `POST /public/forms/:slug/submissions` | `v_published_forms`, `v_published_form_fields`, function `forms_submit_form` |

**One implementation of each shared piece (T1.2.12 de-duplication; do not re-introduce a second copy):**

- HTTP helpers (`Negotiate`, `Send`, `Guard`, `NoStore`, `NotFound`, `NoneMatch`, `ETag`, `SendRedirect`, `MediaList`, `ResolveLinks`) and the date formatter (`DateFormats.Format`, Asia/Vientiane, numeric month when `months` has fewer than 12 entries) live only in `modules/content/readkit`. `pkg/assemble.DateFormats` is a type alias of it.
- The public form definition is built ONLY by `assemble.BuildForm` (field_order first, then leftover properties alphabetically; `x-pii` and `x-error` removed). P-14 (`modules/forms`) embeds it and adds `min_fill_seconds`; `resolved.form` of a `contact_section` does not carry that member (a test pins both shapes). A form whose `json_schema` is not an object is a 500 on P-14 and is left out of the section.
- FAQ items are built ONLY by `assemble.BuildFAQItems` (`faq.FaqItem` is an alias of `assemble.FAQItem`).
- A post card is built ONLY by `blog/services.ItemFromCard`; `blog_strip` of `pkg/assemble` takes `href`, `date`, `date_time`, the image and its alt from it. `HeroAlt` is on `PublishedPostCard`: the card image gets the hero's alt only when the card falls back to the hero (no teaser).
- Redis keys of the forms (`rl:form-key:<keyid16>:m|d:<...>`, `rl:form:<slug>:<iphash32>:dup:<sha256_32>`) are named in `pkg/rediskeys`, never concatenated from `Keys.Prefix()`.

### Preview (P-19, PB-11)

`GET /api/v1/preview/resolve`, `routepolicy.PortalKey()`, **not** wrapped in `readkit.Guard`/`Send` (those make 404 cacheable and add ETag/public Cache-Control). Flow (`modules/preview/{controller,services,repositories,models,routes}`): the controller sets `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow, noarchive`, `Vary: X-Preview-Token` BEFORE anything can fail (so errors carry them), reads the token ONLY from the header (missing/length 32-256/charset `[A-Za-z0-9._~-]` wrong -> 422 `in:"header"`; `?path` over 300 -> 422), then the service: sha256 of the token's ASCII bytes (hashed in Go; the raw token never reaches SQL, Redis or a log) -> rate limit (per portal key 300/min, per token 60/min, fail open) -> `preview:<sha256>` Redis copy (10 s, only usable tokens) or `cms_consume_preview_token` via `db.Raw` (the hash is bound through a `driver.Valuer`: a plain `[]byte` right after `(` is expanded by GORM like an `IN (?)` list) -> entity type must be `cms.pages` (`?path` must equal `page.path`) or `cms.posts` (`?path` must be `/blog/<slug>` of ANY translation in the snapshot: the cookie of the frontend keeps the path when the editor switches language), anything else (and a post token when `Deps.Posts` is nil) is the one 401 `INVALID_TOKEN` (built as `&apperror.Error{Status: 401, ...}`: `apperror.InvalidToken()` is the admin's 400 and the package stays identical to the admin's) -> locale (`?locale`, then the locale of the link, then `Accept-Language`, then the default) -> `Assembler.PreviewPage`. `pkg/assemble/raw.go` is the Raw adapter: `ParsePageSnapshot` (lenient, errors never quote content) -> `PageInput` (sections with `is_visible=false` dropped, sorted by `sort_order`, shared sections filled from the PUBLISHED content via `PageRepository.SharedSections` or left out with a warning, translation chosen like `v_published_pages`, page always noindex, `page.updated_at` = render time, `public_id` from the live row when published) and the SAME `Assembler.Page`. `PageInput.Warnings != nil` is preview mode: a resolver that answers "skip" keeps the section with `resolved: {}` and a `reference_not_published` warning; also `post_not_published`, `link_page_not_published`, `section_type_unknown`, `shared_section_unavailable` (paths are JSON Pointers into the snapshot). Tests: `tests/previewapi` (offline over the bundle + an in-memory stand-in for the SQL function; `dbtest` files on the real function, including a snapshot built with the admin's own baseline SQL). `pkg/logs.Capture` redirects the JSON log in tests.

**Posts (T1.5.8, PT-18).** `modules/preview/services/post.go` (`PostRenderer`) + `pkg/assemble/raw_post.go` (`ParsePostSnapshot`, `RawPost`). The snapshot relied on is the admin's `modules/cms/posts/services/snapshot.go` `Snapshot`: `post{category_id, author_id, hero_media_id, teaser_media_id, is_featured[, published_at]}`, `translations[]{locale_code, slug, title, excerpt, label, location_label, body, gallery_heading, gallery_description, hero_alt, seo_title, seo_description, og_image_id, is_enabled}`, `tag_ids[]`, `media[]{id, media_id, sort_order, translations[]{locale_code, alt, caption}}`. The response `data` is `{locale, kind:"post", post: <PostDetail of P-08>, preview{...}}`: the page is built by the SAME code as P-08 (`blogservices.Composer.Compose`, which `blogService.detail` also calls: one builder, fed from the views for a published post and from the snapshot for a preview). Differences from the public page: `seo.noindex` is true, `updated_at` = render time, the translation is the first ENABLED one in chain order (else any enabled, else any with a slug), category/tags/links come from the PUBLISHED data (an unpublished category or tag is left out with `reference_not_published` at `/post/category_id` / `/tag_ids/<i>`; body links to unpublished pages/posts get `link_page_not_published` / `post_not_published` at `/translations/<i>/body`), `public_id`, `published_at` (unless the snapshot carries one) and the author come from the LIVE row via `Posts.ByIDs` only when the post is published (an unpublished post has no public id, a render-time date and no author: no granted view exposes them), the canonical URL is on the default language's slug. Warnings never quote snapshot values. `Deps.Posts == nil` keeps the old behaviour (post token = unusable). Tests: `tests/previewapi/preview_post_test.go` (offline), `db_post_dbtest_test.go` (real function + real views: preview equals P-08 for the fixture post in both languages) and a post token in `cmd/application/boot_db_test.go`. **Known gap:** `cms_consume_preview_token` has no `deleted_at` check on the post/page (the portal role cannot read base tables and no granted view shows a trashed unpublished entity); the admin revokes the live links when it trashes (`AfterTrash`), the portal adds nothing.

### Adding a module (PB-11 and later)

1. Create `modules/<name>/{controller,entities,models,routes,services,repositories}/`.
2. Register the repository/service/controller in `cmd/application/{repositories,services,controller}.go` and the routes in `router.go` on the `api` group with `routepolicy.PortalKey()`.
3. Entities map views, never tables; never write through GORM (the one write is `forms_submit_form` via `db.Raw`).

---

## Git

> `config.yaml` is committed with keys only (no values). Clear its values before staging it, and `git add` specific files rather than `git add .`. No `Co-Authored-By` / "Generated with" lines; only the user commits.

Tags follow SemVer; current version: first line of the `version` file.
