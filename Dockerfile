# build stage
FROM golang:1.25.0-alpine AS builder
RUN apk add --no-cache git

WORKDIR /go/src/app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
COPY config.yaml .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /go/bin/app ./cmd/server

# final stage
FROM alpine:3.21
LABEL Name=materm-web-portal-backend
RUN apk --no-cache add ca-certificates tzdata nano
RUN mkdir /logs
COPY --from=builder /go/bin/app /app
COPY --from=builder /go/src/app/config.yaml .

ENV TZ=Asia/Vientiane

ENTRYPOINT ["/app"]
EXPOSE 3000