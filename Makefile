GOBIN := $(shell go env GOPATH)/bin

.PHONY: build build-cli setup test test-fast test-full test-js test-correctness test-race coverage up down clean lint lint-all purge trino-init helm-lint docs chaos build-oss test-oss check-boundary verify-reproducible sbom contracts contracts-check rfc-index rfc-check roadmap-board roadmap-check relnotes bench build-ee spec-status-check incident-audit supported-versions supported-versions-check charter-evidence

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

# --- Test suites (GRVX-1206) ----------------------------------------------
# The split is by speed and dependency, never by importance. The slow suites
# carry the `slow` build tag; nothing is skipped, shortened or deleted, and
# every test runs somewhere on every pull request.
#
#   test-fast   what you run before pushing. Budget 5 minutes. No Docker.
#   test-full   everything, including tests/e2e, tests/correctness and bench.
#   test        unchanged: the full run.

setup: ## Check the toolchain, fetch modules, run the fast suite once
	./scripts/dev_setup.sh

test-fast: ## The contributor suite: unit tests, schemas coverage, boundary, golden path
	./scripts/test_fast.sh

test-full: ## Everything, including the slow-tagged suites and the OSS build gates
	./scripts/test_fast.sh
	go test -tags=slow ./... -count=1
	$(MAKE) build-oss
	$(MAKE) test-oss

test:
	go test -tags=slow ./... -v -cover

test-race:
	go test -tags=slow ./... -v -race -count=1

# The Cube model, the dashboard's query routing and the lineage panel are
# JavaScript, so `go test` proves nothing about them. CI has always run these;
# until now there was no way to run them locally short of copying the command
# out of the workflow, and a gate you can only trip in CI is one you trip in CI.
test-js:
	node --test tests/cube/*.test.js dashboards/lib/*.test.js

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

# The Enterprise gateway: the same pkg/gatewaycore as `build`, plus whatever ee/
# feature packages ee/cmd/gateway/main.go blank-imports. Not part of `build`, and
# it fails outright when ee/ is absent — which is the point (charter §7.1).
build-ee: ## Build the Enterprise gateway (requires ee/ present)
	go build -o bin/gateway-ee ./ee/cmd/gateway/

# --- Benchmark (GRVX-1001) -------------------------------------------------
# Every published cost and performance number comes from here, and a stranger
# can run it: no cloud account, no network, no Docker. SCALE=standard is what
# the published figures use; small exists for a laptop or CI.
bench: ## Run the benchmark harness (SCALE=small|standard|large, default small)
	./bench/run.sh --scale $(or $(SCALE),small)

# --- Metric contracts (GRVX-803) ------------------------------------------
# docs/02-derived-metrics.md is generated from contracts/. contracts-check is what
# stops the published definition of a metric drifting from the contract that is
# supposed to define it.

contracts: ## Regenerate docs/02-derived-metrics.md from contracts/
	go run ./pkg/metriccontract/cmd/gen -in contracts -out docs/02-derived-metrics.md

contracts-check: ## Fail if the generated doc is stale
	go run ./pkg/metriccontract/cmd/gen -in contracts -out /tmp/derived-metrics.check.md
	diff -u docs/02-derived-metrics.md /tmp/derived-metrics.check.md

# --- RFCs and the decision log (GRVX-1204) --------------------------------
# docs/oss/rfcs/index.md is generated from the RFCs. rfc-check is what stops the
# decision log drifting from the decisions it claims to record, and what enforces
# the comment window and approval count charter §6 requires.

rfc-index: ## Regenerate docs/oss/rfcs/index.md from docs/oss/rfcs/
	go run ./pkg/rfc/cmd/gen -in docs/oss/rfcs -out docs/oss/rfcs/index.md

rfc-check: ## Validate every RFC and fail if the decision log is stale
	go run ./pkg/rfc/cmd/gen -in docs/oss/rfcs -out /tmp/rfc-index.check.md
	diff -u docs/oss/rfcs/index.md /tmp/rfc-index.check.md

# --- Public roadmap board (GRVX-1207) -------------------------------------
# docs-site/docs/roadmap.md is generated from the goal tree, the spec index and
# the non-goals. roadmap-check is what stops the published roadmap disagreeing
# with the documents that actually decide it.

roadmap-board: ## Regenerate docs-site/docs/roadmap.md
	python3 scripts/gen_roadmap_board.py

roadmap-check: ## Fail if the published roadmap is stale
	python3 scripts/gen_roadmap_board.py --check

# A spec marked done whose files do not exist is a published claim that work
# happened. The register is maintained by hand, so it is checked by machine.
spec-status-check: ## Verify every spec marked done has the files its §4.1 names
	python3 scripts/audit_spec_status.py

# --- The incident loop (GRVX-1408) ----------------------------------------
# Every incident on Gravix Cloud ends as a merged change in the Apache-2.0 core
# or as a public reason why not. There is no third outcome, and this is what
# notices when one has quietly become a backlog item.
#
# It reports; it dispositions nothing. A machine deciding that an incident
# produced no learning would defeat the purpose of the loop.
incident-audit: ## Report incidents that have not closed the loop back to the core
	./scripts/incident_audit.sh

# --- Support windows (GRVX-1501) ------------------------------------------
# SECURITY.md's supported-versions table is generated from docs/oss/releases.json.
# A security policy that claims a support window the maintainers do not honour
# is worse than no policy, so it is not maintained by hand.
supported-versions: ## Regenerate SECURITY.md's supported-versions table
	go run ./pkg/version/cmd/gen

supported-versions-check: ## Fail if SECURITY.md's supported-versions table is stale
	go run ./pkg/version/cmd/gen -check

# --- The annual charter review (GRVX-1508) --------------------------------
# Every number in the review is computed here, not typed. A field nothing
# measured comes back as -1 and is listed as unmeasurable — never as 0, which
# would read as a measurement.
#
# Exit 2 means a capability moved from core into ee/ (a §7.3 Q4 violation) and
# exit 3 means the canary tripped. Both are findings that must reach a person
# before the review is published.
charter-evidence: ## Collect the measured evidence for the annual charter review
	./scripts/charter_evidence.sh

# --- Release notes (GRVX-1209) --------------------------------------------
# Crediting people is the part that must not depend on somebody remembering, so
# it is the part that is generated. The narrative is the opposite: write
# .release-summary.md yourself; the generator refuses to invent it.
#
#   make relnotes VERSION=v1.1.0 PREV=v1.0.0

relnotes: ## Assemble release notes for VERSION since PREV
	@test -n "$(VERSION)" || { echo "usage: make relnotes VERSION=v1.1.0 [PREV=v1.0.0]" >&2; exit 2; }
	go run ./pkg/relnotes/cmd/gen \
	  -prev "$(or $(PREV),$(shell git tag --sort=-creatordate | head -1))" \
	  -version "$(VERSION)" \
	  -out "$(or $(OUT),/tmp/relnotes.md)"

# --- Supply chain (GRVX-709) ----------------------------------------------
# A signature says who built an artefact. Reproducibility says it matches the
# source. The SBOM says what is inside it. All three, or none of them help.

verify-reproducible: ## Build every binary twice and compare digests
	./scripts/verify_reproducible.sh

sbom: ## Generate a CycloneDX SBOM at sbom.json
	./scripts/generate_sbom.sh sbom.json
