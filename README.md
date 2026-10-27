

# Materm Web Portal Backend

Go backend API for the Materm Web Portal website — built with Fiber v2. It is the **read API** of the public website (doc/08 section 2): it reads published content from PostgreSQL views with a read-only role and is called **only server-to-server** by the portal-frontend with the `X-Portal-Key` header. It never migrates or writes the schema. Today it provides the foundation (error/response envelopes, request id, access log, portal key guard, `/health`, `/ready`); the content modules come with milestone M2.

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
| `cache.enabled`, `cache.ttl` | `true`, `24h` | response cache |
| `rateLimit.readPerMinute` | `300` | read ceiling per client |
| `forms.dailyLimit`, `forms.minFillSeconds`, `forms.maxClockSkew` | `20`, `3`, `10m` | contact form limits |
| `preview.cacheTTL` | `10s` | preview snapshot cache |
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
├── modules/          # Feature modules (`system` today: /health, /ready)
├── pkg/              # apperror, response, pagination, requestid, logs, ratelimit, rediskeys, portalkey,
│                     # routepolicy, cache, testutil, utilities, ...
├── tests/            # Black-box tests (bootstrap DSN)
├── config.yaml       # Configuration (keys only in git)
├── config.enc.yaml   # Encrypted configuration (stale, see above)
└── .sops.yaml        # SOPS encryption rules
```



## API

- **Base path**: `/api/v1` (every route needs `X-Portal-Key`; a path that matches nothing is 401 without it, 404 with it)
- **Probes** (no key, root level): `GET /health` (liveness), `GET /ready` (readiness: database ok and Redis ok; in dev a missing Redis is `degraded`), aliases `/healthz`, `/readyz`
- **Errors**: `{success:false, message, code, errors?, request_id}`, identical to the admin backend

| Method | Path | Policy |
|---|---|---|
| `GET` | `/health`, `/healthz` | public |
| `GET` | `/ready`, `/readyz` | public |



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