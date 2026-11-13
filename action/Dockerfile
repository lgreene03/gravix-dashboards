# Go 1.26.11, the first 1.26 release free of GO-2026-6859 (F-084), matching the
# toolchain line in go.mod. The tag names the version and the digest pins the
# bytes, so a stale cached golang:1.26-alpine can no longer build a release.
FROM golang:1.26.11-alpine@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS builder

WORKDIR /build

# Copy Go SDK (local dependency).
COPY sdk/go/ sdk/go/

# Copy CLI source and root go.mod/go.sum.
COPY go.mod go.sum ./
COPY cmd/cli/ cmd/cli/

# Build a static binary.
RUN go build -ldflags="-s -w" -o /gravix ./cmd/cli/

# --- Runtime ---
FROM alpine:3.20

RUN apk add --no-cache bash
COPY --from=builder /gravix /usr/local/bin/gravix
COPY action/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
