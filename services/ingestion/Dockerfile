# Go 1.26.11, the first 1.26 release free of GO-2026-6859 (F-084), matching the
# toolchain line in go.mod. The tag names the version and the digest pins the
# bytes, so a stale cached golang:1.26-alpine can no longer build a release.
FROM golang:1.26.11-alpine@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ingestion-service ./services/ingestion

FROM alpine:3.21

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-ingestion" \
      org.opencontainers.image.description="Gravix ingestion service — HTTP API for fact collection" \
      org.opencontainers.image.source="https://github.com/lgreene03/gravix-dashboards" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Gravix"

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/ingestion-service .
RUN mkdir -p /app/data/raw && chown -R gravix:gravix /app/data
USER gravix

EXPOSE 8080

ENTRYPOINT ["./ingestion-service"]
