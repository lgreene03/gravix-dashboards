FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o request-metrics-rollup ./transforms/request_metrics_minute
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o service-events-rollup ./transforms/service_events_daily
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o purge ./cmd/purge

FROM alpine:3.21

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-rollup" \
      org.opencontainers.image.description="Gravix rollup and purge jobs — ETL from JSONL facts to Parquet metrics" \
      org.opencontainers.image.source="https://github.com/gravix/gravix" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Gravix"

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app

# Install timezone data for cron scheduling
RUN apk add --no-cache tzdata

COPY --from=builder /app/request-metrics-rollup .
COPY --from=builder /app/service-events-rollup .
COPY --from=builder /app/purge .

RUN mkdir -p /app/data && chown -R gravix:gravix /app/data
USER gravix

# Default entrypoint — override in docker-compose per container
ENTRYPOINT ["./request-metrics-rollup"]
