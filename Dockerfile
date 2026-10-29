# syntax=docker/dockerfile:1

# ---- build stage ------------------------------------------------------------------------------------------------
# The Go image must be at least the `go` line of go.mod (1.25.0); 1.26.10 is pinned because it carries the
# standard-library fixes that `govulncheck` asked for (go 1.26.2 had 20+ findings in net/http, crypto/tls, ...).
# Bump the patch version here when govulncheck reports a newer fix. The toolchain is fixed by the image, so
# GOTOOLCHAIN=local keeps Go from downloading another one at build time.
ARG GO_VERSION=1.26.10
FROM golang:${GO_VERSION}-alpine AS builder
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/server

# ---- final stage ------------------------------------------------------------------------------------------------
FROM alpine:3.21
LABEL Name=materm-web-portal-backend
RUN apk --no-cache add ca-certificates tzdata \
 && addgroup -S -g 10001 app && adduser -S -u 10001 -G app -H -h /app -s /sbin/nologin app \
 && mkdir -p /app && chown app:app /app

WORKDIR /app
COPY --from=builder --chown=app:app /out/app /app/app
# NO configuration is baked in. The binary insists on a ./config.yaml, so the image carries the BLANK template
# (keys only, deploy/config.blank.yaml); every value comes from the environment (DATABASE_ADMIN_HOST,
# REDIS_HOST, PORTAL_FRONTENDKEYS, CACHE_INVALIDATEKEY, SERVER_ENV, ...) or from a config.yaml that the
# orchestrator mounts over /app/config.yaml (Secret / ConfigMap).
COPY --chown=app:app deploy/config.blank.yaml /app/config.yaml

ENV TZ=Asia/Vientiane \
    LOG_FILE=off \
    SERVER_PORT=3000
USER 10001:10001

# 3000 = the public API. The Prometheus endpoint (metrics.port) is NOT exposed here on purpose: when enabled, set
# METRICS_BIND=0.0.0.0 and publish that port to the monitoring network only, never through the ingress.
EXPOSE 3000

# /health is the liveness probe (touches no dependency); use /ready for readiness in the orchestrator.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${SERVER_PORT:-3000}/health" || exit 1

ENTRYPOINT ["/app/app"]
