.PHONY: build build-cli test test-race coverage up down clean lint lint-all purge trino-init helm-lint docs

build:
	go build -o bin/ingestion-service ./services/ingestion/
	go build -o bin/gateway ./services/gateway/
	go build -o bin/request-metrics-rollup ./transforms/request_metrics_minute/
	go build -o bin/service-events-rollup ./transforms/service_events_daily/
	go build -o bin/service-events-detail-rollup ./transforms/service_events_detail/
	go build -o bin/load-generator ./cmd/load_generator/
	go build -o bin/purge ./cmd/purge/
	go build -o bin/gravix ./cmd/cli/

build-cli:
	go build -o bin/gravix ./cmd/cli/

test:
	go test ./... -v -cover

test-race:
	go test ./... -v -race -count=1

coverage:
	go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out
	@echo "---"
	@echo "Schema coverage:"
	@go test ./schemas/... -cover

up:
	docker-compose up -d --build

down:
	docker-compose down

clean:
	rm -rf bin/ coverage.out
	docker-compose down -v

lint:
	go vet ./...

lint-all: lint
	@which staticcheck > /dev/null 2>&1 || (echo "Installing staticcheck..." && go install honnef.co/go/tools/cmd/staticcheck@latest)
	staticcheck ./...

helm-lint:
	helm lint deploy/gravix \
		--set global.apiKey=test-key \
		--set global.storage.accessKey=test-access \
		--set global.storage.secretKey=test-secret

purge:
	go run ./cmd/purge/ --retention-days 30

trino-init:
	bash storage/trino/run-queries.sh

docs:
	@echo "API docs: open docs/api-site/index.html in a browser"
	@echo "OpenAPI spec: docs/openapi.yaml"
