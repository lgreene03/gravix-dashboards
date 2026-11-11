FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o load-generator ./cmd/load_generator

FROM alpine:3.24

# OCI image labels for provenance and registry metadata
LABEL org.opencontainers.image.title="gravix-load-generator" \
      org.opencontainers.image.description="Gravix synthetic load generator for testing and demos" \
      org.opencontainers.image.source="https://github.com/lgreene03/gravix-dashboards" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Gravix"

# Take the fixes Alpine has published since the base image was cut. The tag is a
# floating minor, so a rebuild already moves; this closes the gap between point
# releases, which is where zlib 1.4-r0 sat while image-scan was red (F-084).
RUN apk upgrade --no-cache

RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/load-generator .
USER gravix

# Environment variables can override flags
CMD ["./load-generator", "--target", "http://gravix-ingestion:8080/api/v1/facts", "--qps", "5", "--concurrency", "2"]
