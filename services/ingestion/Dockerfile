FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o ingestion-service ./services/ingestion

FROM alpine:3.21
RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/ingestion-service .
RUN mkdir -p /app/data/raw && chown -R gravix:gravix /app/data
USER gravix

# Expose port
EXPOSE 8080

ENTRYPOINT ["./ingestion-service"]
