.PHONY: build test up down clean lint purge trino-init

build:
	go build -o bin/ingestion-service ./services/ingestion/
	go build -o bin/rollup-job ./transforms/request_metrics_minute/
	go build -o bin/event-rollup ./transforms/service_events_daily/
	go build -o bin/load-generator ./cmd/load_generator/
	go build -o bin/purge ./cmd/purge/

test:
	go test ./... -v -cover

up:
	docker-compose up -d --build

down:
	docker-compose down

clean:
	rm -rf bin/
	docker-compose down -v

lint:
	go vet ./...

purge:
	go run ./cmd/purge/ --retention-days 30

trino-init:
	bash storage/trino/run-queries.sh
