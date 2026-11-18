# Go 1.26.11, the first 1.26 release free of GO-2026-6859 (F-084), matching the
# toolchain line in go.mod. The tag names the version and the digest pins the
# bytes, so a stale cached golang:1.26-alpine can no longer build a release.
FROM golang:1.26.11-alpine@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o request-metrics-rollup ./transforms/request_metrics_minute && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o service-events-rollup ./transforms/service_events_daily && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o service-events-detail-rollup ./transforms/service_events_detail && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o purge ./cmd/purge && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bootstrap_seed ./cmd/bootstrap_seed && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o iceberg-sync ./transforms/iceberg_sync && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o trino-catalog-sync ./transforms/trino_catalog_sync

FROM alpine:3.24

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-rollup" \
      org.opencontainers.image.description="Gravix rollup and purge jobs — ETL from JSONL facts to Parquet metrics" \
      org.opencontainers.image.source="https://github.com/lgreene03/gravix-dashboards" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Gravix"

# Take the fixes Alpine has published since the base image was cut. The tag is a
# floating minor, so a rebuild already moves; this closes the gap between point
# releases, which is where zlib 1.4-r0 sat while image-scan was red (F-085).
RUN apk upgrade --no-cache

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app

# Install timezone data for cron scheduling.
#
# Deliberately unpinned, against DL3018. Alpine's package repository serves only
# the CURRENT version of each package, so pinning an exact patch of tzdata does
# not reproduce an old build — it guarantees a future one fails, on the day
# upstream bumps it and the pinned version stops being served. That is exactly
# what happened: the pin read 2025c-r0, the repository moved to 2026c-r0, and
# every `docker compose up --build` of the bootstrap stack broke with
# "unable to select packages" (F-019).
#
# tzdata is also the package where currency beats reproducibility: a stale copy
# means a cron job fires at the wrong hour after a timezone rule changes. The
# base image is a floating minor tag and the stage runs apk upgrade above, so
# this layer is deliberately current rather than byte reproducible; the Go
# binaries, built from go.sum, are the reproducible part.
# hadolint ignore=DL3018
RUN apk add --no-cache tzdata

COPY --from=builder /app/request-metrics-rollup .
COPY --from=builder /app/service-events-rollup .
COPY --from=builder /app/service-events-detail-rollup .
COPY --from=builder /app/purge .
COPY --from=builder /app/bootstrap_seed .
COPY --from=builder /app/iceberg-sync .
COPY --from=builder /app/trino-catalog-sync .

RUN mkdir -p /app/data && chown -R gravix:gravix /app/data
USER gravix

# Default entrypoint — override in docker-compose per container
ENTRYPOINT ["./request-metrics-rollup"]
