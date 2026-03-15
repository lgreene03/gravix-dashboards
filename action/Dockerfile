FROM golang:1.24-alpine AS builder

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
