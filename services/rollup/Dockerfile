FROM golang:1.24-alpine AS builder

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
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o iceberg-sync ./transforms/iceberg_sync

FROM alpine:3.21

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-rollup" \
      org.opencontainers.image.description="Gravix rollup and purge jobs — ETL from JSONL facts to Parquet metrics" \
      org.opencontainers.image.source="https://github.com/lgreene03/gravix-dashboards" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Gravix"

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
# base image is pinned by digest above, which is where build reproducibility
# actually comes from.
# hadolint ignore=DL3018
RUN apk add --no-cache tzdata

COPY --from=builder /app/request-metrics-rollup .
COPY --from=builder /app/service-events-rollup .
COPY --from=builder /app/service-events-detail-rollup .
COPY --from=builder /app/purge .
COPY --from=builder /app/bootstrap_seed .

RUN mkdir -p /app/data && chown -R gravix:gravix /app/data
USER gravix

# Default entrypoint — override in docker-compose per container
ENTRYPOINT ["./request-metrics-rollup"]
