# CLAUDE.md — Materm Web Portal Backend

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

This repo is the **read API of the public Materm website** (design: `../doc/08-portal-integration.md` section 2; decisions: `../doc/00-decisions.md` section 10.4). It is no longer a skeleton of the old project: the leftover `modules/auth`, `pkg/middleware` and the `jwt.*`/`GlobalEnv` code are gone. State: PB-1 (cleanup + shared kernel) and PB-2 (bootstrap, portal key, probes) are done; the content modules (`site`, `pages`, `blog`, ...; PB-3 and later, milestone M2) do not exist yet, so the only routes are the probes.

Rules that shape everything here:

- **Server-to-server only.** The only caller is the portal-frontend server, which sends `X-Portal-Key`. Every route under `/api/v1` needs it (guard mounted once on the group, so even unknown paths are 401 without it); only `/health` and `/ready` are open. There is no user, no JWT, no login.
- **Read-only.** The database role is `cms_portal_ro` (SELECT on the `v_public_*`/`v_published_*` views, EXECUTE on `forms_submit_form` and `cms_consume_preview_token`). The portal **never migrates, seeds or writes through GORM models**: no `AutoMigrate`, no SQL runner. The admin backend owns the schema.
- **Same error/response shapes as the admin backend.** `pkg/apperror`, `pkg/response`, `pkg/pagination`, `pkg/requestid` are copies of the admin packages and are kept identical on purpose (doc/06 section 1.3): change them in both repos or in neither.
- Hard rules for agents: never connect to the database `materm` / container `postgres-maxdb` / `redis-stack`; never print or commit `config.yaml` values, `.env*`, `config.enc.yaml`.

## Commands

See `Makefile`: `run`, `build`, `test`, `test-db`, `testdb-up`, `testdb-status`, `lint`, `check-config`, `test-coverage`, `encrypt`, `decrypt`, `docker-test*`, `swag`.

- `make test` — unit tests of every package (`./...`), no database, no Redis.
- `make test-db` — adds the tests with build tag `dbtest` (`go test -tags=dbtest ./...`). They run **only** on the scratch Postgres (127.0.0.1:55433) and Redis (127.0.0.1:56380) that belong to the **admin backend repo** (`../materm-admin-backend/scripts/testdb.sh`). This repo never starts/stops them: `make testdb-up` delegates to the admin Makefile (a no-op when they already run) and `test-db` loads the admin repo's git-ignored `.env.test` into the test command's shell without printing it (`TESTDB_ENV` overrides the path). Tests create random `materm_test_*` databases and random Redis key prefixes. The guards in `pkg/testutil/{dbtest,redistest}` refuse database `materm`, ports 5432/6379 and any cluster without the scratch marker.
- `make lint` — golangci-lint v2 with `--build-tags=dbtest`; linters: errcheck, govet (all but fieldalignment), staticcheck, nilerr, bodyclose, ineffassign, unused, contextcheck, gosec, rowserrcheck, sqlclosecheck (tests are exempt from errcheck/contextcheck/bodyclose/gosec; `pkg/apperror` from gosec G101).
- Single test: `go test -v -run TestName ./path/...`.

> **config.yaml rule:** before every commit clear **all values** (keys only; `make check-config` runs the admin repo's script and fails printing key paths only). It holds real credentials once filled in.
>
> **config.enc.yaml is stale** (old project's schema and age key). Regenerate with `make encrypt` after a Materm age key is in `.sops.yaml`; never `make decrypt` over the new `config.yaml`.

## Configuration

`bootstrap/env.go`: `Env` = `server`, `database` (`pool` + `admin`, the **read-only** `cms_portal_ro` connection; `sell/erp/device`, `jwt`, `mailer`, `bootstrap` in the shared template are ignored), `redis`, `portal`, `file`, `media`, `cache`, `rateLimit`, `forms`, `preview`, `site` (the keys of doc/08 section 2.9; defaults in the README table). Keys the template lacks are registered with `SetDefault` so an environment variable (`PORTAL_FRONTENDKEYS`, `SERVER_ENV`, ...) reaches them. `Defaults()` repairs blanks; `Validate()` returns warnings plus a `ConfigError` listing every violated rule, **naming keys, never values**.

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
  → SetupServices(repos, strict)
    → SetupControllers(services)
      → SetupRouter(fiber, controllers, portalkey.Verifier)  // returns the routepolicy Registry and the /api/v1 Group
```

`cmd/application/{repositories,services,controller,router,setup}.go` are the **only** places that wire dependencies by hand (hotspot files: one owner per milestone). Content modules register on the `api` group returned by `SetupRouter`.

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

Prefix `materm-portal:` (`constants.AppKeyPrefix`); **never FLUSH** (the Redis is shared with the admin backend, prefix `materm-admin:`): `cache.DeleteAll` deletes by prefix. `pkg/rediskeys` names every key family of doc/08 section 2.7 (`gen`, `c:<gen>:…`, `tag:`, `locales`, `preview:`, `rl:read:`, `rl:form:`, `ipsalt:`); raw IPs are hashed (`ratelimit.Hash`), no key carries personal data. `pkg/ratelimit` (Redis Lua fixed window, `Middleware` with explicit `FailOpen`) is ported but **not mounted**: callers always have a key, so only the form limiter (PB-9) will use it.

### `pkg/`

| Path | Purpose |
|---|---|
| `apperror/` `response/` `pagination/` `requestid/` | shared kernel, identical to the admin backend |
| `logs/` | zap JSON to stdout + `logs/YYYY-MM-DD.log` (`LOG_FILE=off` for containers/tests); `Info/Warn/WarnEvery/Error`, field helpers, `WithRequest`/`FromContext` (request id only — no user id here) |
| `portalkey/` `routepolicy/` | see above |
| `ratelimit/` `rediskeys/` `cache/` | Redis building blocks |
| `testutil/{dbtest,redistest}` | scratch-only DB/Redis helpers with hard guards |
| `constants/` `constants/services/` | `MainRoute`, `HealthCheckRoute`; `AppKeyPrefix`, `RootKey`, `ExpireTime`, `DBQueryTimeout` (3 s) |
| `utilities/{convertor,format,generator,helper,validator}`, `circuitbreaker/`, `httpclient/` | legacy helpers from the old project, kept (validator has `Struct/Issues` for apperror field errors) |

### Tests

`tests/bootstrap` (DSN) plus tests next to the code: env/Validate, pipeline (404 envelope, panic hygiene, request id, body limit over a real listener, CORS incl. empty list, security headers, route template), portalkey, routepolicy, system service/controller, application wiring (`cmd/application`), and every ported package. `dbtest`-tagged: role check, Redis clear-on-start own prefix only, boot against scratch PG/Redis (`/ready` ok/degraded, creates nothing in the database), ratelimit Lua.

### Adding a module (PB-3 and later)

1. Create `modules/<name>/{controller,entities,models,routes,services,repositories}/`.
2. Register the repository/service/controller in `cmd/application/{repositories,services,controller}.go` and the routes in `router.go` on the `api` group with `routepolicy.PortalKey()`.
3. Entities map views, never tables; never write through GORM (the one write is `forms_submit_form` via `db.Raw`).

---

## Git

> `config.yaml` is committed with keys only (no values). Clear its values before staging it, and `git add` specific files rather than `git add .`. No `Co-Authored-By` / "Generated with" lines; only the user commits.

Tags follow SemVer; current version: first line of the `version` file.
