FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o load-generator ./cmd/load_generator

FROM alpine:3.18
WORKDIR /app
COPY --from=builder /app/load-generator .

# Environment variables can override flags
CMD ["./load-generator", "--target", "http://gravix-ingestion:8080/api/v1/facts", "--qps", "5", "--concurrency", "2"]
