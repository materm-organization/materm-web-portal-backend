# CLAUDE.md — Materm Web Portal Backend

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

This repo is the Materm Web Portal backend skeleton (Fiber/GORM/Redis). **`auth` is the only business module** under `modules/`; shared infrastructure lives in `bootstrap/` and `pkg/` so new modules can be added the same way.

## Commands

See `Makefile` for all available targets (`install`, `run`, `build`, `swag`, `test`, `test-coverage`, `lint`, `encrypt`, `decrypt`, `docker-test*`).

Run a single test: `go test -v -run TestFunctionName ./path/to/package/...`

> **Config decryption** requires SOPS with an age key at `~/.config/sops/age/keys.txt` (this path isn't in the Makefile itself) — decrypt before first run.

> **config.yaml rule:** Before every `git commit` / `git push`, clear **all values** from `config.yaml` — leave keys only (e.g. `host:` with no value). The file contains real credentials once filled in. Never commit values.

> **config.enc.yaml is stale:** it was committed as-is and still holds an older schema that does not match the keys in `config.yaml`. Regenerate it with `make encrypt` (after filling the new `config.yaml`, with a Materm-owned age key in `.sops.yaml`) before relying on it.

## Architecture

### Startup flow

`cmd/server/main.go` → `bootstrap.App()` loads config + Redis → `cmd/application` wires everything in four explicit steps:

```
SetupRepositories(dbConfig, redis)
  → SetupServices(repos)
    → SetupControllers(services)
      → SetupRouter(fiber, controllers)
```

Each file in `cmd/application/` (`repositories.go`, `services.go`, `controller.go`, `router.go`) is the **only place that manually wires dependencies**. `SetupRepositories` currently only opens the PostgreSQL `admin` connection (with `database.pool` settings) and the Redis cache repository; `auth` itself uses neither.

### Bootstrap (`bootstrap/`)

| File | Purpose |
|---|---|
| `env.go` | Loads `config.yaml` via Viper into the typed `Env` struct; exposes `bootstrap.GlobalEnv`. Sections (camelCase keys): `server` (port, readTimeout, writeTimeout), `database` (`pool` + four named PostgreSQL connections `admin`/`sell`/`erp`/`device`; only `admin` is wired, `device` has no sslmode/timezone), `redis`, `jwt` (secret, refreshSecret), `minio`, `mailer`, `bootstrap` (adminUsername/adminPassword) — `minio`, `mailer`, `bootstrap` are parsed only. A **temporary** trailing `keycloak` block (snake_case keys) remains for the existing auth module until it is rewritten on JWT. Timeouts are Go duration strings with a unit (e.g. `30s`) |
| `app.go` | Assembles `Application{Env, Fiber, Redis, Swagger}` — Swagger construction is commented out (DB wiring is done in `SetupRepositories`) |
| `fiber.go` | `NewFiber(ServerConfig)` creates the Fiber app (applies `server.readTimeout`/`writeTimeout` when non-zero) with helmet, logger, recover, CORS, and `dbQueryTimeoutMiddleware` (30s deadline on every request context) |
| `postgres.go` | `DBConnection.DSN()` builds the libpq DSN (sslmode/timezone omitted when empty); `NewPostgresConnection(conn, pool)` opens one connection and applies `database.pool` (zero values keep database/sql defaults); `Migrate(models...)` wraps `AutoMigrate` and is not called on startup |
| `redis.go` | `InitializeRedis(RedisConfig)` → `*redis.Client`; clears cache prefixes on start if `redis.clearOnStart: true` |
| `swagger.go` | `NewSwaggerConfig()` — Swagger metadata struct (host `localhost:<server.port>`); `@BasePath /api/v1` annotations live here |

### Module structure

Modules live under `modules/<name>/` with one subdirectory per layer. Package names match the **subfolder**, not the module name:

```
modules/<name>/
  controller/<name>.controller.go   → package controller    (HTTP handlers, DTOs only)
  entities/<name>.entity.go         → package entities      (GORM table structs, no json tags)
  models/<name>.model.go            → package models        (DTOs: request/response, no GORM)
  routes/<name>.route.go            → package routes
  services/<name>.service.go        → package services      (business logic + DTO↔entity mapping)
  repositories/<name>.repository.go → package repositories  (GORM queries, entities only)
```

`auth` currently has only `controller/`, `models/`, `routes/`, `services/` (it is stateless: Keycloak + JWT, no DB tables).

**Layer contract — strictly enforced:**
- `repositories` receives and returns **entities only** — never imports `models`
- `services` maps **request DTO → entity** before calling repo; maps **entity → response DTO** before returning
- `controller` works with **DTOs only** — never imports `entities`

**Interface naming:** `<Name>Ctrl`, `<Name>Service`, `<Name>Repository`
**Constructor naming:** `New<Name>Ctrl`, `New<Name>Service`, `New<Name>Repository`, `New<Name>Router`

### Active modules & routes

Health check: `GET /health` (root level, not under `/api/v1`)

All other routes are under `/api/v1`. `auth`'s `/signin` and `/refresh` are registered directly on the `/api/v1` group (no `/auth` prefix) — only `/auth/me` sits under `/auth`.

| Module | Routes |
|---|---|
| `auth` | `POST /signin` (Keycloak); `POST /refresh` (local JWT — see Authentication below); `GET /auth/me` (Keycloak) |

### Authentication

**JWT** — secrets come from `jwt.secret` (access) and `jwt.refreshSecret` (refresh); see `pkg/middleware/jwt_auth_middleware.go` for `AccessToken`/`AccessRefreshToken`/`GetOwnerAccessToken`/`GetInfoAccessToken`.

`POST /refresh` is backed by `JwtAuthCtrl`/`JwtAuthService` — this app's own encrypted-JWT scheme (`middleware.GenerateJWTToken`/`GetOwnerRefresh`), **not** Keycloak. `AuthCtrl.RefreshToken` (the Keycloak-proxying refresh) still exists on `auth.controller.go` but is not wired to any route.

**Keycloak OIDC (optional)** — enabled via `keycloak.enabled: true` in config (the `keycloak` block is temporary, until auth is rewritten on JWT); see `pkg/middleware/keycloak_middleware.go` for `KeycloakAuth`/`GetKeycloakClaims`/`RequireRealmRole`. Note `pkg/middleware` imports `modules/auth/models` (Keycloak claim types).

### Security pattern: identity always comes from the JWT

User-scoped endpoints should derive the user ID from `middleware.GetOwnerAccessToken(ctx)`, never from a client-supplied ID in the query or body (prevents IDOR). Apply this to any new endpoint by default.

### Response helpers

Always use `pkg/middleware` helpers (`response_middleware.go` for `NewSuccessResponse`/`NewErrorResponses`/etc., `msg_err.go` for error constructors like `ErrorNotFound`/`ErrorBadRequest`) — never `fiber.Map` directly. `pkg/response/` also exists but is unused — do not use it.

### Pagination

| Type | Struct | Use when |
|---|---|---|
| Offset | `middleware.PageQuery` | Fixed-page UIs |
| Keyset / load-more | `middleware.LoadMoreQuery` | Infinite scroll |

`LoadMoreQuery` flow: fetch `limit+1` rows ordered `id DESC` (optionally `WHERE id < cursor`); if `len > limit` → `HasMore=true`, trim to `limit`, set `NextCursor` = last trimmed row's ID (0 if no more). Client sends `cursor=NextCursor` for next page. Default choice for new list endpoints.

### Redis cache pattern

Cache key prefix is `materm-portal:` (`constants.AppKeyPrefix` in `pkg/constants/services/key.go`); build every cache key on it to avoid collisions on a shared Redis. TTL is `constants.ExpireTime` (1 day).

```go
// Cache hit check
if raw, err := r.cache.Get(ctx, cacheKey, constants.RootKey); err == nil {
    var cached []*entities.MyEntity
    if json.Unmarshal(raw, &cached) == nil && len(cached) > 0 {
        return cached[0], nil
    }
}

// Cache miss — query DB then store
_ = r.cache.Set(ctx, cacheKey, constants.RootKey, &result, constants.ExpireTime)
```

> `cache.Get` wraps the stored value in a JSON array `[value]` on read — unmarshal into a **slice** (`[]*T`), then take index 0 for single-entity lookups.

### Parallel service pattern

Parallel DB calls use buffered channels — every channel is buffered to its writer count to avoid goroutine leaks. Round 1 launches independent goroutines; Round 2 depends on Round 1 results.

### `pkg/` shared packages

| Path | Purpose |
|---|---|
| `pkg/cache/` | `RedisRepository` — `Get`, `Set`, `Delete`, `DeleteByPrefix`, `GetClient` |
| `pkg/middleware/` | Auth (JWT, Keycloak), response helpers, `PageQuery`, `LoadMoreQuery` |
| `pkg/constants/` | Route constants (`MainRoute`, `HealthCheckRoute`) |
| `pkg/constants/services/` | `AppKeyPrefix`, `RootKey`, `DBQueryTimeout`, `ExpireTime`, `Keys` struct |
| `pkg/utilities/convertor/` | Model conversion, JSON↔string helpers |
| `pkg/utilities/format/` | Datetime, phone, string helpers |
| `pkg/utilities/generator/` | Random strings, OTP, file-name generation |
| `pkg/utilities/helper/` | Generic `Retry[T]` / `RetryVoid` |
| `pkg/utilities/validator/` | `ValidateStruct` wrapper for go-playground/validator |
| `pkg/logs/` | Zap-based logger (`logs.Info`, `logs.Error`) |
| `pkg/circuitbreaker/` | Sony gobreaker wrapper |
| `pkg/httpclient/` | Configurable base-URL HTTP client |

### Tests

`tests/` currently holds only `tests/bootstrap` (DSN builder). `make test` runs `./tests/...` via gotestsum.

### golangci-lint

Config lives in `.golangci.yml` (v2 format — `linters.settings` not `linters-settings`, `linters.exclusions.rules` not `issues.exclude-rules`).

Active linters: `errcheck`, `govet` (all checks except `fieldalignment`), `staticcheck` (all checks except style), `nilerr`, `bodyclose`, `ineffassign`, `unused`, `contextcheck`.

Exclusions cover `_test.go` files, generated `docs/`, and the `copier.Copy` errcheck message.

### Adding a new module

1. Create `modules/<name>/{controller,entities,models,routes,services,repositories}/` with package names matching subfolders.
2. Register in `cmd/application/repositories.go`, `services.go`, `controller.go`, `router.go`.
3. Run `make swag` to regenerate docs if Swagger annotations were added.

---

## Git

> `config.yaml` is committed with keys only (no values). Always clear its values before staging it, and `git add` specific files rather than `git add .`.

### Versioning

Tags follow SemVer (`v<MAJOR>.<MINOR>.<PATCH>`). Current version: see the `version` file (first line).
