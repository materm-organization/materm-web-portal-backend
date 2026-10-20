

# Materm Web Portal Backend

Go backend API for the Materm Web Portal — built with Fiber v2. Currently contains the `auth` module only.

---

Go
Fiber
GORM
Redis
JWT
Keycloak
Swagger
License
Coverage

## Tech Stack

- **Language**: Go 1.25.0
- **Framework**: Fiber v2
- **Database**: PostgreSQL with GORM (named connections: admin, sell, erp, device; only admin is connected today)
- **Cache**: Redis
- **Documentation**: Swagger/OpenAPI
- **Authentication**: Keycloak (OIDC) + JWT



## Quick Start



### 1. Prerequisites

- Go 1.25.0+
- PostgreSQL
- Redis
- Keycloak instance
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

`config.yaml` reference (keys only; never commit real values):

```yaml
server:
  port:             # HTTP port
  readTimeout:      # duration with unit, e.g. 30s (blank = Fiber default)
  writeTimeout:     # duration with unit, e.g. 30s (blank = Fiber default)

database:
  pool:             # applied to the admin connection; 0/blank = database/sql default
    maxOpenConns:
    maxIdleConns:
    connMaxLifetimeMinutes:
    connMaxIdleTimeMinutes:
  admin:            # the only connection opened at startup (PostgreSQL)
    host:
    port:
    user:
    password:
    dbname:
    sslmode:
    timezone:
  sell:             # sell, erp, device: parsed only, not connected yet
    host: ...       # same keys as admin
  erp:
    host: ...       # same keys as admin
  device:
    host: ...       # same keys as admin, but no sslmode/timezone

redis:
  host:
  port:
  username:
  password:
  db:
  clearOnStart:

jwt:
  secret:           # access token secret
  refreshSecret:    # refresh token secret

minio:              # parsed only, no feature uses it yet
  endpoint:
  accessKey:
  secretKey:
  bucketName:
  baseUrl:

mailer:             # parsed only, no feature uses it yet
  host:
  username:
  password:
  port:
  from:

bootstrap:          # parsed only, no feature uses it yet
  adminUsername:
  adminPassword:

# TEMPORARY: kept until auth is rewritten on JWT (existing auth module reads it)
keycloak:
  base_url:
  realm:
  client_id:
  client_secret:
  enabled:
```

Any key can also be overridden by an environment variable (`.` replaced by `_`, e.g. `REDIS_HOST`).

> `config.enc.yaml` still holds an older schema and must be regenerated from the new `config.yaml` (`make encrypt`) once values are filled in.



### 4. Run Application

```bash
make swag   # generate Swagger docs (required before first run)
make run
```



## Project Structure

```
.
├── bootstrap/        # App initialization (fiber, db, redis, swagger)
├── cmd/
│   ├── application/  # Dependency wiring (repositories, services, controllers, router)
│   └── server/       # Entry point
├── modules/          # Feature modules (only `auth` today)
│   └── auth/
│       ├── controller/
│       ├── models/
│       ├── routes/
│       └── services/
├── pkg/              # Shared utilities
│   ├── middleware/
│   ├── response/
│   └── utilities/
├── config.yaml       # Configuration (decrypted, gitignored)
├── config.enc.yaml   # Encrypted configuration (committed to repo)
└── .sops.yaml        # SOPS encryption rules
```



## API Documentation

- **Swagger UI**: `http://localhost:8080/swagger/index.html`
- **Base Path**: `/api/v1`
- **Auth**: Bearer JWT Token (via Keycloak)

### Routes

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check (root level, not under `/api/v1`) |
| `POST` | `/api/v1/signin` | Sign in via Keycloak |
| `POST` | `/api/v1/refresh` | Refresh token (local JWT scheme) |
| `GET` | `/api/v1/auth/me` | Current user info (Keycloak-protected) |



## Makefile Commands

```bash
make install   # Download Go modules and install Swagger + golangci-lint CLIs
make run       # Run the application
make build     # Build binary to bin/app
make swag      # Generate Swagger documentation
make test      # Run all tests
make lint      # Run golangci-lint (v2)
make decrypt   # Decrypt config.enc.yaml → config.yaml
make encrypt   # Encrypt config.yaml → config.enc.yaml
```



## Testing

```bash
make test
go test -v -run TestFunctionName ./path/to/package/...
```



## Common Issues

**Swagger not found:**

```bash
go install github.com/swaggo/swag/cmd/swag@latest
source ~/.zshrc
```

**Port in use:**

```bash
lsof -ti:8080 | xargs kill -9
```



## Contributors

This project exists thanks to all the people who contribute.


|                                                   |                                                 |                                             |
| ------------------------------------------------- | ----------------------------------------------- | ------------------------------------------- |
| **[MaxSoudPouk](https://github.com/MaxSoudPouk)** | **[Ssoulisack](https://github.com/Ssoulisack)** | **[Souny99](https://github.com/Souny99)** |




## License

Apache 2.0