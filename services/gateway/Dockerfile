FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gateway ./services/gateway

FROM alpine:3.21

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-gateway" \
      org.opencontainers.image.description="Gravix gateway service — multi-tenant auth and management API" \
      org.opencontainers.image.source="https://github.com/gravix/gravix" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Gravix"

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/gateway .
RUN mkdir -p /app/data && chown -R gravix:gravix /app/data
USER gravix

EXPOSE 8091

ENTRYPOINT ["./gateway"]
