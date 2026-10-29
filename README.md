

# Materm Web Portal Backend

Go backend API for the Materm Web Portal website — built with Fiber v2. It is the **read API** of the public website (doc/08 section 2): it reads published content from PostgreSQL views with a read-only role and is called **only server-to-server** by the portal-frontend with the `X-Portal-Key` header. It never migrates or writes the schema. It provides the foundation (error/response envelopes, request id, access log, portal key guard, `/health`, `/ready`) and, since milestone M2, the 16 public read/submit endpoints below (site, pages, seo, blog, gallery, faq, forms) plus, since M4, the preview endpoint `GET /api/v1/preview/resolve` (P-19, PB-11): 17 in all.

---

Go
Fiber
GORM
Redis
JWT
Swagger
License
Coverage

## Tech Stack

- **Language**: Go 1.25.0
- **Framework**: Fiber v2
- **Database**: PostgreSQL with GORM (named connections: admin, sell, erp, device; only admin is connected today)
- **Cache**: Redis
- **Documentation**: Swagger/OpenAPI
- **Authentication**: shared secret in the `X-Portal-Key` header (server-to-server only, two keys for rotation)



## Quick Start



### 1. Prerequisites

- Go 1.25.0+
- PostgreSQL
- Redis
- [SOPS](https://github.com/getsops/sops) (for config encryption/decryption)
- [age](https://github.com/FiloSottile/age) (encryption backend used by SOPS)



### 2. Install Dependencies

```bash
make install
```



### 3. Configure Application

Decrypt `config.enc.yaml` to get `config.yaml` before running:

```bash
make decrypt
```

To re-encrypt after editing:

```bash
make encrypt
```

> **Note:** You need the age private key in `~/.config/sops/age/keys.txt` or set via the `SOPS_AGE_KEY` environment variable.

`config.yaml` reference (keys only; never commit real values). Sections the portal does not read (`database.sell/erp/device`, `jwt`, `mailer`, `bootstrap`) may stay in the shared template; they are ignored.

```yaml
server:
  port:             # HTTP port (required)
  readTimeout:      # duration with unit, e.g. 30s (blank = 30s)
  writeTimeout:     # duration with unit, e.g. 30s (blank = 30s)

database:
  pool:             # blank = 15 open / 5 idle / 30 min lifetime / 5 min idle time
    maxOpenConns:
    maxIdleConns:
    connMaxLifetimeMinutes:
    connMaxIdleTimeMinutes:
  admin:            # the one PostgreSQL connection; for the portal it is the READ-ONLY role cms_portal_ro
    host:
    port:
    user:
    password:
    dbname:
    sslmode:
    timezone:

redis:
  host:
  port:
  username:
  password:
  db:
  clearOnStart:     # dev only; deletes only the materm-portal: key prefix
```

Keys that are **not** in the template are registered with defaults in code and can be set in `config.yaml` or by environment variable (`.` replaced by `_`, upper case):

| Key | Default | Meaning |
|---|---|---|
| `server.env` | blank = `prod` | `dev` \| `staging` \| `prod`; anything but `dev` is strict |
| `server.corsOrigins` | `[]` | allowed browser origins; blank = no CORS answer at all (never `*`) |
| `server.trustedProxies`, `server.proxyHeader` | `[]`, blank | where the connection's client IP comes from |
| `portal.frontendKeys` | `[]` | accepted `X-Portal-Key` values, at most 2 (rotation), >= 32 printable ASCII bytes each; env `PORTAL_FRONTENDKEYS` (comma separated), never in the repo. Required outside dev; blank in dev refuses every `/api/v1` request |
| `file.path_ip` | blank | base URL of the file service (media URLs) |
| `media.staticPrefix` | `static/` | storage keys with this prefix are served by portal-frontend from `public/` |
| `redis.dialTimeout`, `redis.readTimeout`, `redis.writeTimeout` | `200ms`, `300ms`, `300ms` | fast-fail limits of one Redis attempt (10ms to 5s). A Redis outage must cost milliseconds per request, never a timeout of the frontend's 2 s fetch (B1) |
| `redis.maxRetries` | `0` | EXTRA attempts of a failed command (0 to 3; 0 = one attempt: go-redis's own default of 3 retries x 5 dials x 100 ms made one command on a dead Redis cost ~2 s) |
| `redis.breakerCooldown` | `3s` | circuit breaker (`pkg/redisguard`): after a transport failure Redis is not called at all for this long (500ms to 1m), every request is answered from Postgres; then ONE request probes it. On recovery the response cache bumps its generation once |
| `cache.enabled`, `cache.ttl` | `true`, `24h` | response cache |
| `cache.invalidateKey` | blank | HMAC secret of the cache-invalidation messages the admin backend publishes (>= 32 printable ASCII bytes; env `CACHE_INVALIDATEKEY`, never in the repo). Blank outside dev: the subscriber does not start (warning); blank in dev: it accepts unsigned messages |
| `cache.invalidateChannel` | `materm:cms.invalidate` | Redis pub/sub channel of the invalidation messages |
| `cache.invalidateMaxAge` | `10m` | oldest `occurred_at` an invalidation message may have (replay protection) |
| `rateLimit.readPerMinute` | `300` | read ceiling per client |
| `forms.dailyLimit`, `forms.minFillSeconds`, `forms.maxClockSkew` | `20`, `3`, `10m` | fallbacks of the form's own limits (`rate_limit_daily_max`, `min_fill_seconds`) and how far a `rendered_at` may lie in the future |
| `forms.keyPerMinute`, `forms.keyPerDay` | `30`, `1000` | submissions of ALL visitors together per portal key (doc/10 R-020: a leaked key plus a random `X-Client-IP` per call must not bypass the per-visitor counters); `keyPerMinute` must not exceed `keyPerDay` |
| `forms.dedupeTTL` | `120s` | an identical submission (same visitor, form and content) inside this window is answered `received` without a second insert (max `24h`) |
| `preview.cacheTTL` | `10s` | how long the snapshot of a consumed preview token stays in Redis (`preview:<sha256>`); a revoked link stops working within this time |
| `preview.tokenPerMinute`, `preview.keyPerMinute` | `60`, `300` | P-19 calls per preview token per minute (doc/08 section 2.10), and per portal key over all tokens (a caller that sends a different token each time is bounded too) |
| `metrics.port`, `metrics.bind` | `0` (off), `127.0.0.1` | Prometheus `GET /metrics` on a SEPARATE listener `bind:port` (never on `server.port`; no authentication, so keep it off every ingress; in a container set `METRICS_BIND=0.0.0.0` and publish the port to the monitoring network only). Names `materm_portal_*`: `http_requests_total` and `http_request_duration_seconds` (labels method, route template, status class), `rate_limited_total`, `cache_requests_total{result=hit\|miss\|bypass}`, `invalidate_events_total{result=applied\|rejected\|ignored}`, `redis_breaker_open` / `_trips_total` / `_skipped_total`, `db_pool_*`, plus `go_goroutines` and `go_memstats_alloc_bytes` |
| `site.defaultBaseUrl` | blank | fallback of the `site.base_url` setting |

> `config.enc.yaml` still holds an older schema and must be regenerated from the new `config.yaml` (`make encrypt`) once values are filled in.



### 4. Run Application

```bash
make run
```

The server validates its configuration first and refuses to start (naming the keys, never the values) when it is invalid.



## Project Structure

```
.
├── bootstrap/        # Config (env.go + Validate), Fiber pipeline, PostgreSQL (read-only), Redis
├── cmd/
│   ├── application/  # Dependency wiring (setup, repositories, services, controller, router)
│   └── server/       # Entry point (graceful shutdown)
├── modules/          # system (/health, /ready); content/ (view read-structs, repositories, readkit = the shared
│                     # HTTP/date/link helpers); site, pages, seo, blog, gallery, faq, forms (one folder each)
├── pkg/              # apperror, response, pagination, requestid, logs, ratelimit, rediskeys, portalkey,
│                     # routepolicy, cache, publiccache, localize, media, richtext, assemble (page sections),
│                     # iphash, clientip, testutil, utilities, ...
├── tests/            # Black-box tests (bootstrap DSN)
├── config.yaml       # Configuration (keys only in git)
├── config.enc.yaml   # Encrypted configuration (stale, see above)
└── .sops.yaml        # SOPS encryption rules
```



## API

- **Base path**: `/api/v1` (every route needs `X-Portal-Key`; a path that matches nothing is 401 without it, 404 with it)
- **Probes** (no key, root level): `GET /health` (liveness), `GET /ready` (readiness: the database answers AND the role can read the view `v_public_locales`; Redis down only makes it `200 degraded`, the database down or the view unreadable is `503 down`), aliases `/healthz`, `/readyz`
- **Errors**: `{success:false, message, code, errors?, request_id}`, identical to the admin backend

| Method | Path | Policy |
|---|---|---|
| `GET` | `/health`, `/healthz` | public |
| `GET` | `/ready`, `/readyz` | public |

Public content endpoints (all need `X-Portal-Key`; `?locale=` then `Accept-Language` then the default locale; a 200 carries `Cache-Control`, a weak `ETag` (304 on `If-None-Match`), `Content-Language`, `Vary: Accept-Language` when the header decided):

| Module | Endpoint | Id | Views it reads |
|---|---|---|---|
| site | `GET /public/locales` | P-01 | `v_public_locales` |
| site | `GET /public/settings?group=` | P-02 | `v_public_site_settings`, `v_public_locales` |
| site | `GET /public/ui-strings?ns=` | P-03 | `v_public_ui_strings` |
| site | `GET /public/menus/:key` | P-04 | `v_published_menu_items` (+ pages) |
| site | `GET /public/site` | P-05 | locales, settings, menus, UI strings in one answer |
| pages | `GET /public/pages/resolve?path=` | P-06 | `v_published_pages`, `v_published_page_sections` and, per section type, posts, albums, FAQ, forms (`pkg/assemble`) |
| blog | `GET /public/posts` | P-07 | `v_published_posts`, `v_published_categories`, `v_published_post_tags` |
| blog | `GET /public/posts/:slug` | P-08 | `v_published_posts`, `v_published_post_slugs` (308 to the visitor's slug), `v_published_post_media` |
| blog | `GET /public/categories`, `GET /public/tags` | P-09, P-10 | `v_published_categories`, `v_published_tags` |
| gallery | `GET /public/albums`, `GET /public/albums/:key` | P-11, P-12 | `v_published_albums`, `v_published_album_items` |
| faq | `GET /public/faq/:group_key` | P-13 | `v_published_faq_items` |
| forms | `GET /public/forms/:slug` | P-14 | `v_published_forms`, `v_published_form_fields` |
| forms | `POST /public/forms/:slug/submissions` | P-15 | the function `forms_submit_form` (the one write) |
| seo | `GET /public/sitemap` | P-16 | `v_sitemap` |

Preview endpoint (needs `X-Portal-Key` like everything under `/api/v1`; not under `/public`):

| Module | Endpoint | Id | Reads |
|---|---|---|---|
| preview | `GET /preview/resolve` (header `X-Preview-Token`, optional `?locale=`, `?path=`) | P-19 | the function `cms_consume_preview_token` (EXECUTE only, the token is hashed in Go), then the same published views as P-06 for what the frozen page snapshot refers to |

`X-Preview-Token` is the raw token the admin issued (never in the query string; a `?token=` is ignored). A usable token answers `200 {kind:"page", locale, page:<the body of P-06>, preview:{entity_type, entity_id, locale, warnings[]}}` for a page link, or `200 {kind:"post", locale, post:<the flat body of P-08>, preview:{...}}` for a post link (`cms.posts` snapshot, `seo.noindex` always true; for a post `?path=` is `/blog/<slug>` of any language of the post), rendered from the snapshot frozen when the link was issued; unknown, expired, revoked, bound to another `?path=` or to an entity type without an adapter (albums, FAQ groups and forms have none, doc/05 PT-18) all answer the same `401 INVALID_TOKEN`; a missing or malformed header is `422`; `429` with `Retry-After` past the ceilings. Every answer is `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow, noarchive`, `Vary: X-Preview-Token`; it never reads or writes the response cache. A token works any number of times until it expires.

Page sections of `GET /public/pages/resolve` get a `resolved` member per section type (`blog_strip`, `gallery_cards`, `photo_story`, `contact_section`, `faq`, `post_list`, ...). P-15 never reveals spam handling (a bot gets the same `200 {"status":"received"}`), rate limits answer 429 with `Retry-After`, and the submission answer is `Cache-Control: no-store`.

## Makefile Commands

```bash
make install   # Download Go modules and install Swagger + golangci-lint CLIs
make run       # Run the application
make build     # Build binary to bin/app
make swag      # Generate Swagger documentation
make test      # Run all unit tests (no database, no Redis)
make test-db   # Run the tests that need the scratch Postgres/Redis (see below)
make lint      # Run golangci-lint (v2, with the dbtest tag)
make decrypt   # Decrypt config.enc.yaml → config.yaml
make encrypt   # Encrypt config.yaml → config.enc.yaml
```



## Testing

```bash
make test
go test -v -run TestFunctionName ./path/to/package/...
make testdb-up && make test-db   # DB/Redis tests, scratch containers only
```

Tests that need PostgreSQL or Redis carry the build tag `dbtest` and run only on the scratch containers of the admin backend repository (`../materm-admin-backend/scripts/testdb.sh`, ports 55433 / 56380); the guards in `pkg/testutil` refuse the database `materm` and ports 5432 / 6379.

The schema of these tests is the reference SQL of the design (`../doc/sql`, built by `pkg/testutil/schemadb`). One test, `TestSeededPagesEqualTheFrontendBundle` (`tests/publicapi`), needs the real web content seed, which only the admin backend can load: prepare a scratch database with the admin repository's `bootstrap.Migrate()` + `bootstrap.Seed`, then run `MATERM_APP_SEEDED_DSN=<its superuser DSN> make test-db`. Without the variable it is skipped (reported as skipped, not passed).



## Common Issues

**Port in use:**

```bash
lsof -ti:3000 | xargs kill -9
```



## Contributors

This project exists thanks to all the people who contribute.


|                                                   |                                                 |                                             |
| ------------------------------------------------- | ----------------------------------------------- | ------------------------------------------- |
| **[MaxSoudPouk](https://github.com/MaxSoudPouk)** | **[Ssoulisack](https://github.com/Ssoulisack)** | **[Souny99](https://github.com/Souny99)** |




## License

Apache 2.0