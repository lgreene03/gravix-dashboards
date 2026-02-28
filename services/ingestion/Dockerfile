FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ingestion-service ./services/ingestion

FROM alpine:3.21

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-ingestion" \
      org.opencontainers.image.description="Gravix ingestion service — HTTP API for fact collection" \
      org.opencontainers.image.source="https://github.com/gravix/gravix" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Gravix"

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/ingestion-service .
RUN mkdir -p /app/data/raw && chown -R gravix:gravix /app/data
USER gravix

EXPOSE 8080

ENTRYPOINT ["./ingestion-service"]
