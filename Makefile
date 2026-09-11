GOBIN := $(shell go env GOPATH)/bin

.PHONY: build build-cli test test-js test-correctness test-race coverage up down clean lint lint-all purge trino-init helm-lint docs chaos build-oss test-oss check-boundary verify-reproducible sbom contracts contracts-check

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

# The Cube model, the dashboard's query routing and the lineage panel are
# JavaScript, so `go test` proves nothing about them. CI has always run these;
# until now there was no way to run them locally short of copying the command
# out of the workflow, and a gate you can only trip in CI is one you trip in CI.
test-js:
	node --test cube/model/schema/*.test.js dashboards/lib/*.test.js

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

# `lint` runs exactly what CI's lint job runs. It used to be go vet alone, which
# meant a change could pass `make lint` and still fail the check called `lint` —
# staticcheck finds things vet does not (unused package-level identifiers, for one).
# A local target weaker than the check it is named after is worse than no target.
lint:
	go vet ./...
	@command -v staticcheck > /dev/null 2>&1 || test -x "$(GOBIN)/staticcheck" || \
		(echo "Installing staticcheck..." && go install honnef.co/go/tools/cmd/staticcheck@latest)
	@# GOPATH/bin is not always on PATH, so call it by path when it is not.
	$$(command -v staticcheck || echo "$(GOBIN)/staticcheck") ./...

lint-all: lint

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

chaos:
	bash scripts/chaos/run_all.sh

# --- Open-core boundary (GRVX-704) ---------------------------------------
# These three enforce charter §7.1: the Apache-2.0 core must build, test and run
# with ee/ physically deleted. They run on every pull request.

build-oss: ## Build the Apache-2.0 core with ee/ absent
	./scripts/build_oss.sh build

test-oss: ## Build and test the Apache-2.0 core with ee/ absent
	./scripts/build_oss.sh test

# --- Correctness suite (GRVX-810) ----------------------------------------
# Proves the Phase 8 properties hold together rather than one spec at a time.
# No Docker, and it must stay under five minutes or nobody runs it.
test-correctness: ## Run the correctness suite (determinism, late data, mergeability, lineage)
	./scripts/correctness_test.sh

check-boundary: ## Enforce the open-core boundary (imports, gates, headers, map)
	go run ./cmd/checkboundary -root . -map docs/oss/boundary.yaml

# --- Metric contracts (GRVX-803) ------------------------------------------
# docs/02-derived-metrics.md is generated from contracts/. contracts-check is what
# stops the published definition of a metric drifting from the contract that is
# supposed to define it.

contracts: ## Regenerate docs/02-derived-metrics.md from contracts/
	go run ./pkg/metriccontract/cmd/gen -in contracts -out docs/02-derived-metrics.md

contracts-check: ## Fail if the generated doc is stale
	go run ./pkg/metriccontract/cmd/gen -in contracts -out /tmp/derived-metrics.check.md
	diff -u docs/02-derived-metrics.md /tmp/derived-metrics.check.md

# --- Supply chain (GRVX-709) ----------------------------------------------
# A signature says who built an artefact. Reproducibility says it matches the
# source. The SBOM says what is inside it. All three, or none of them help.

verify-reproducible: ## Build every binary twice and compare digests
	./scripts/verify_reproducible.sh

sbom: ## Generate a CycloneDX SBOM at sbom.json
	./scripts/generate_sbom.sh sbom.json
