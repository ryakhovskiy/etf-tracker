# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22 AS runtime
# HTTPS providers need CA roots; date calculations use Go's timezone database.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /usr/local/share/zoneinfo.zip
RUN addgroup -S -g 10001 holdings \
    && adduser -S -D -H -u 10001 -G holdings holdings \
    && mkdir -p /app/data \
    && chown holdings:holdings /app/data \
    && chmod 700 /app/data
WORKDIR /app
COPY --from=build /out/api ./api
ENV PORT=9000 \
    ZONEINFO=/usr/local/share/zoneinfo.zip \
    DATABASE_PATH=/app/data/holdings.db
USER 10001:10001
EXPOSE 9000
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:9000/health || exit 1
ENTRYPOINT ["/app/api"]
