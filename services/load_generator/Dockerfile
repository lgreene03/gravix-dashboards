FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o load-generator ./cmd/load_generator

FROM alpine:3.21
RUN addgroup -S gravix && adduser -S gravix -G gravix
WORKDIR /app
COPY --from=builder /app/load-generator .
USER gravix

# Environment variables can override flags
CMD ["./load-generator", "--target", "http://gravix-ingestion:8080/api/v1/facts", "--qps", "5", "--concurrency", "2"]
