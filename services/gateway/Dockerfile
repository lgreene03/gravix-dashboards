FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gateway ./services/gateway

FROM alpine:3.21

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-gateway" \
      org.opencontainers.image.description="Gravix gateway — tenant management, auth, and billing API" \
      org.opencontainers.image.source="https://github.com/gravix/gravix" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Gravix"

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/gateway .
# Metric contracts are source, not data: GET /api/v1/lineage reads them to report
# a metric's formula, exactness and error bound. Without them the endpoint has
# nothing to say, so they ship inside the image alongside the binary.
COPY --from=builder /app/contracts ./contracts
RUN mkdir -p /app/data && chown -R gravix:gravix /app/data
USER gravix

EXPOSE 8080

ENTRYPOINT ["./gateway"]
