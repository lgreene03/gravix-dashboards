# SPEC GRVX-901: Single-command boot — `docker compose up` produces a populated dashboard with zero file edits

| Field | Value |
|---|---|
| **Spec ID** | GRVX-901 |
| **Phase** | 9 |
| **Goal** | G3.2 (0 required config steps before first data) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §2.1 "Operate" row — docker-compose and the `gravix` CLI are core, free forever. No `ee/` question arises: nothing here is gated. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-704 |
| **Blocks** | GRVX-906, GRVX-910 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today, `docker compose -f docker-compose.bootstrap.yml up -d --build` does not produce a working
stack: `ingestion` and `gateway` both set `TENANT_DB_PATH=/app/data/gravix.db`, but nothing creates
that database or seeds a tenant and API key, and there is no traffic generator, so the dashboard
shows a permanently empty chart. After this spec, `docker compose -f docker-compose.bootstrap.yml
up -d --build` alone — no `.env` edits, no manual seed command, no copy-pasted API key — produces a
running stack that is already ingesting synthetic traffic and whose dashboard is pre-configured
with a working API key.

## 2. Context the implementer needs

- `docker-compose.bootstrap.yml:41-44` — `gateway` sets `TENANT_DB_PATH=/app/data/gravix.db`.
- `docker-compose.bootstrap.yml:68-71` — `ingestion` sets `TENANT_DB_PATH=/app/data/gravix.db`.
  `services/ingestion/main.go:692-712` checks `TENANT_DB_PATH` **before** `API_KEY`, so ingestion
  will open (or create empty) `/app/data/gravix.db` in multi-tenant auth mode and reject every
  request until a tenant and API key exist in it.
- `.env.bootstrap.example:9` sets `API_KEY=change-me-to-a-real-secret`, which ingestion never reads
  because `TENANT_DB_PATH` takes precedence — this variable is currently dead configuration in
  bootstrap mode.
- `cmd/seed_tenants/main.go` seeds **three** demo SaaS tenants (Acme/Beta/Gamma) with three
  different API keys — the wrong shape for a single self-hosted deployment and never invoked by
  `docker-compose.bootstrap.yml` at all.
- `pkg/tenantdb/tenantdb.go:154-158` — `APIKeyRepo.Create(ctx, tenantID, name string, expiresAt
  *time.Time) (plainKey string, key *APIKey, err error)`. The returned key carries no scope
  restriction (`APIKeyInfo.Scopes` empty means unrestricted, `pkg/tenantdb/tenantdb.go:59-65`).
- `pkg/tenantdb/tenantdb.go:16-30` — `Tenant{ID, Name, Email, Plan, Status, ...}`.
- `pkg/tenantdb/tenantdb.go:26` — `db.Tenants().GetByEmail(ctx, email) (*Tenant, error)` (used by
  `cmd/seed_tenants/main.go:44` to check for an existing tenant).
- `pkg/tenantdb/sqlite.go:26-37` — `tenantdb.Open(dbPath string) (*SQLiteDB, error)` creates the
  SQLite file and runs migrations if it does not already exist.
- `services/rollup/Dockerfile` — a single multi-binary image already builds
  `request-metrics-rollup`, `service-events-rollup`, `service-events-detail-rollup`, and `purge`
  from `cmd/` and `transforms/`, run with different `entrypoint`/`command` overrides per
  `docker-compose.bootstrap.yml` service. This is the pattern to extend, not replace.
- `services/load_generator/Dockerfile:20` — default `CMD ["./load-generator", "--target",
  "http://gravix-ingestion:8080/api/v1/facts", "--qps", "5", "--concurrency", "2"]`. Wrong hostname
  for `docker-compose.bootstrap.yml` (whose ingestion service name is `ingestion`, not
  `gravix-ingestion` — `container_name` is `gravix-ingestion` but the resolvable Compose service
  name on the `backend` network is `ingestion`). `cmd/load_generator/main.go:112-114` falls back to
  the `API_KEY` env var when `--api-key` is not passed.
- `docker-compose.bootstrap.yml:30-56` (`gateway`) and `:58-81` (`ingestion`) currently have no
  `depends_on` at all.
- `docker-compose.bootstrap.yml:114-133` (`dashboard`) mounts `./dashboards:/usr/share/nginx/html:ro`
  and depends on `cube: condition: service_healthy`.
- `dashboards/index.html:1166-1169` loads `lib/utils.js`, `lib/cube-client.js`, `lib/chart-helpers.js`,
  then `app.js`, in that order, with no `config.js`-style include before them.
- `dashboards/app.js:1-8` — `GRAVIX_CONFIG` is built as `Object.assign({<defaults>},
  window.GRAVIX_CONFIG || {})`, so any script that sets `window.GRAVIX_CONFIG` before `app.js` loads
  overrides the defaults, and a missing script tag (404) leaves `window.GRAVIX_CONFIG` `undefined`
  with no thrown error — the existing defaults apply unchanged.

## 3. Non-goals for this spec

- Do NOT change `docker-compose.yml` (the full stack). This spec is scoped to
  `docker-compose.bootstrap.yml` only.
- Do NOT add a feature flag to disable synthetic traffic. The roadmap deliverable is explicit:
  ship *with* synthetic traffic by default. A user who wants it stopped runs
  `docker compose -f docker-compose.bootstrap.yml stop synthetic-traffic`; that is a documented
  runtime action, not a config edit, and needs no code.
- Do NOT touch `cmd/seed_tenants/`. It remains the multi-tenant demo seeder for `docker-compose.yml`.
- Do NOT implement service auto-discovery, SLO dashboards, or alert proposals — GRVX-902/903/904.
- Do NOT change how the dashboard behaves when `window.GRAVIX_CONFIG` is absent (full stack,
  non-bootstrap deployments) — the `Object.assign` fallback in `dashboards/app.js:3-8` already
  covers that case and must keep working unmodified.
- This spec does not cross non-goal §3 (No Agents) because the synthetic-traffic container is an
  optional, explicitly-visible Compose service the operator can see and stop — not a hidden agent.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `cmd/bootstrap_seed/main.go` | Idempotently provisions one local tenant, one unrestricted API key, and the dashboard's runtime config |
| `cmd/bootstrap_seed/main_test.go` | Tests for `cmd/bootstrap_seed/main.go` |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `docker-compose.bootstrap.yml` | Add `bootstrap-init` and `synthetic-traffic` services; add `depends_on: bootstrap-init: condition: service_completed_successfully` to `gateway`, `ingestion`, `dashboard`; add a second bind mount on `dashboard` for the generated config file |
| `services/rollup/Dockerfile` | Add a fourth `go build` line for `./cmd/bootstrap_seed` and a matching `COPY --from=builder` |
| `.env.bootstrap.example` | Remove the now-dead `API_KEY` line; add a comment explaining that the API key is generated automatically into `data/api_key.txt` |
| `dashboards/index.html` | Add `<script src="dashboard_config.js"></script>` immediately before the `<script src="app.js"></script>` line |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docker-compose.yml` | Full-stack compose file; out of scope |
| `cmd/seed_tenants/**` | Multi-tenant demo seeder; unrelated to single-org bootstrap |
| `dashboards/app.js` | No behavior change needed — the existing `Object.assign` default-merge already consumes `window.GRAVIX_CONFIG` fields added by this spec |
| `services/ingestion/main.go`, `services/gateway/main.go` | Neither needs a code change; both already support `TENANT_DB_PATH` |
| `pkg/tenantdb/**` | This spec is a consumer of the existing repo interfaces, not a schema change |

## 5. Interface contract

```go
// Command bootstrap_seed idempotently provisions the single local tenant and
// API key needed for zero-config self-hosted boot of docker-compose.bootstrap.yml.
//
// Usage:
//
//	go run ./cmd/bootstrap_seed/ \
//	  -db ./data/gravix.db \
//	  -api-key-file ./data/api_key.txt \
//	  -dashboard-config ./data/dashboard_config.js
package main

// Flags:
//   -db                 string   default "./data/gravix.db"            "Path to the tenant SQLite database"
//   -api-key-file       string   default "./data/api_key.txt"          "Path to write the plaintext API key (mode 0600)"
//   -dashboard-config   string   default "./data/dashboard_config.js"  "Path to write the dashboard's window.GRAVIX_CONFIG file"
//   -tenant-name        string   default "local"                       "Name of the single bootstrap tenant"
//   -tenant-email       string   default "local@gravix.invalid"        "Email of the single bootstrap tenant"
//   -ingestion-url      string   default "http://localhost:8090"       "Value written into dashboard_config.js as ingestionApiUrl"
//   -gateway-url        string   default "http://localhost:8091"       "Value written into dashboard_config.js as gatewayUrl"

func main()

// provision performs the idempotent seed. It is the unit under test; main()
// parses flags and calls it.
func provision(ctx context.Context, cfg provisionConfig) error

type provisionConfig struct {
	DBPath           string
	APIKeyFile       string
	DashboardConfig  string
	TenantName       string
	TenantEmail      string
	IngestionURL     string
	GatewayURL       string
}

var ErrAlreadyProvisioned = errors.New("bootstrap_seed: api key file already exists, nothing to do")
```

### 5.1 `dashboard_config.js` exact content

```js
window.GRAVIX_CONFIG = {
  ingestionApiUrl: "<IngestionURL>",
  gatewayUrl: "<GatewayURL>",
  apiKey: "<plainKey>"
};
```

One line per field, values JSON-string-escaped via `encoding/json`, file mode `0644`.

## 6. Behaviour

`provision` executes these steps in order:

1. `os.Stat(cfg.APIKeyFile)`. If the file exists, print `bootstrap already provisioned (api key
   file exists at <path>)` to stdout, return `ErrAlreadyProvisioned`. `main()` treats
   `ErrAlreadyProvisioned` as success: prints the message and exits 0.
2. `tenantdb.Open(cfg.DBPath)` — creates the database and runs migrations if it does not exist.
3. `existing, _ := db.Tenants().GetByEmail(ctx, cfg.TenantEmail)`.
4. If `existing == nil`: create `&tenantdb.Tenant{Name: cfg.TenantName, Email: cfg.TenantEmail,
   Plan: "free", Status: "active"}` via `db.Tenants().Create(ctx, tenant)`. Set `tenant := existing`
   otherwise (the file was deleted but the tenant survived — do not create a duplicate tenant).
5. `plainKey, _, err := db.APIKeys().Create(ctx, tenant.ID, "bootstrap", nil)`.
6. `os.WriteFile(cfg.APIKeyFile, []byte(plainKey), 0600)`.
7. Build the `dashboard_config.js` content per §5.1 and `os.WriteFile(cfg.DashboardConfig, ...,
   0644)`.
8. Print `provisioned tenant "<name>" (id=<id>)` and `api key written to <path>` to stdout.
9. Return `nil`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `-db` and `-api-key-file` point at directories that do not exist and cannot be created | `main()` exits 1 | `bootstrap_seed: <underlying os error>` |
| `tenantdb.Open` fails | `main()` exits 1 | `bootstrap_seed: open tenant database: <error>` |
| `db.APIKeys().Create` fails | `main()` exits 1 | `bootstrap_seed: create api key: <error>` |
| API key file already exists | exit 0, no files touched | `bootstrap already provisioned (api key file exists at <path>)` |

## 6.2 `docker-compose.bootstrap.yml` changes

Add, after the `gateway` service block:

```yaml
  bootstrap-init:
    build:
      context: .
      dockerfile: services/rollup/Dockerfile
    container_name: gravix-bootstrap-init
    entrypoint: ["./bootstrap_seed"]
    command:
      - "-db=/app/data/gravix.db"
      - "-api-key-file=/app/data/api_key.txt"
      - "-dashboard-config=/app/data/dashboard_config.js"
    volumes:
      - ./data:/app/data
    networks:
      - backend
```

Add `depends_on: { bootstrap-init: { condition: service_completed_successfully } }` to `gateway`,
`ingestion`, and `dashboard`. Add a second volume line to `dashboard`:
`- ./data/dashboard_config.js:/usr/share/nginx/html/dashboard_config.js:ro`.

Add, after the `purge` service block:

```yaml
  synthetic-traffic:
    build:
      context: .
      dockerfile: services/load_generator/Dockerfile
    container_name: gravix-synthetic-traffic
    restart: always
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        API_KEY="$$(cat /app/data/api_key.txt)" exec ./load-generator \
          --target http://ingestion:8080/api/v1/facts \
          --qps 5 --concurrency 2
    volumes:
      - ./data:/app/data:ro
    depends_on:
      ingestion:
        condition: service_healthy
    mem_limit: 64m
    cpus: 0.25
    networks:
      - backend
```

`services/rollup/Dockerfile` gains a fifth build line —
`CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bootstrap_seed ./cmd/bootstrap_seed` — and a
matching `COPY --from=builder /app/bootstrap_seed .` in the runtime stage.

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `provision` on an empty directory creates exactly one tenant with email `local@gravix.invalid` | `TestProvisionCreatesSingleTenant` |
| AC-2 | `provision` writes a non-empty API key file with mode `0600` | `TestProvisionWritesAPIKeyFileWithCorrectMode` |
| AC-3 | `provision` writes `dashboard_config.js` containing the literal string `window.GRAVIX_CONFIG` and the written API key | `TestProvisionWritesDashboardConfig` |
| AC-4 | Calling `provision` a second time returns `ErrAlreadyProvisioned` and does not create a second tenant | `TestProvisionIsIdempotent` |
| AC-5 | Deleting only the API key file and re-running `provision` reuses the existing tenant (tenant count stays 1) | `TestProvisionReusesExistingTenantWhenKeyFileMissing` |
| AC-6 | The API key written by `provision` validates successfully via `db.APIKeys().ValidateKey` | `TestProvisionedAPIKeyValidates` |
| AC-7 | `docker compose -f docker-compose.bootstrap.yml config` parses without error and defines a `bootstrap-init` and a `synthetic-traffic` service | `TestBootstrapComposeConfigValid` (shell, see §8) |

## 8. Verification

```bash
# 1. Unit tests for the seed binary
go test ./cmd/bootstrap_seed/... -v -run TestProvision
# expect: PASS, all TestProvision* pass

# 2. Idempotency end to end
rm -rf /tmp/gravix-bootstrap-verify && mkdir -p /tmp/gravix-bootstrap-verify
go run ./cmd/bootstrap_seed/ \
  -db /tmp/gravix-bootstrap-verify/gravix.db \
  -api-key-file /tmp/gravix-bootstrap-verify/api_key.txt \
  -dashboard-config /tmp/gravix-bootstrap-verify/dashboard_config.js
go run ./cmd/bootstrap_seed/ \
  -db /tmp/gravix-bootstrap-verify/gravix.db \
  -api-key-file /tmp/gravix-bootstrap-verify/api_key.txt \
  -dashboard-config /tmp/gravix-bootstrap-verify/dashboard_config.js
# expect: second run prints "bootstrap already provisioned (api key file exists at ...)" and exits 0

# 3. docker-compose.bootstrap.yml still parses
docker compose -f docker-compose.bootstrap.yml config >/dev/null
echo "exit=$?"
# expect: exit=0

# 4. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 5. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

### 8.1 Verification record — 2026-09-11

**Result: implemented and verified, with one open spec defect (SD-013) that this spec cannot resolve.**
Everything below was run as written.

**1. Unit tests for the seed binary**

```
$ go test ./cmd/bootstrap_seed/... -v -run TestProvision -count=1
--- PASS: TestProvisionCreatesSingleTenant (0.04s)
--- PASS: TestProvisionWritesAPIKeyFileWithCorrectMode (0.03s)
--- PASS: TestProvisionWritesDashboardConfig (0.03s)
--- PASS: TestProvisionIsIdempotent (0.03s)
--- PASS: TestProvisionReusesExistingTenantWhenKeyFileMissing (0.04s)
--- PASS: TestProvisionedAPIKeyValidates (0.04s)
--- PASS: TestProvisionFailsWhenPathsAreUnusable (0.00s)
ok  	github.com/lgreene/gravix-dashboards/cmd/bootstrap_seed	0.212s
```

**2. Idempotency end to end**

```
$ go run ./cmd/bootstrap_seed/ -db …/gravix.db -api-key-file …/api_key.txt -dashboard-config …/dashboard_config.js
…migrations complete applied=8 current_version=8
provisioned tenant "local" (id=3b2aae83-375d-46e7-8131-b8b91ae0d152)
api key written to …/api_key.txt

$ # second run, same arguments
bootstrap already provisioned (api key file exists at …/api_key.txt)
exit=0
```

The generated file matches §5.1 exactly, and the key file is `0600`:

```js
window.GRAVIX_CONFIG = {
  ingestionApiUrl: "http://localhost:8090",
  gatewayUrl: "http://localhost:8091",
  apiKey: "grvx_…"
};
```

**3. `docker-compose.bootstrap.yml` still parses** — it did not before. See F-010.

```
$ docker compose -f docker-compose.bootstrap.yml config >/dev/null; echo "exit=$?"
exit=0
$ docker compose -f docker-compose.bootstrap.yml config --services | sort
bootstrap-init
cube
dashboard
gateway
ingestion
purge
request-metrics-rollup
service-events-rollup
synthetic-traffic
```

**4. Nothing else broke**

```
$ go build ./... && go test ./schemas/...
ok  	github.com/lgreene/gravix-dashboards/schemas	0.006s
```

**5. Open-core integrity**

```
$ make check-boundary
boundary: 0 violations
$ make build-oss && make test-oss
(both succeed with ee/ absent)
```

**Beyond §8**, the two gates that exist only in CI:

```
$ go test ./... -race -count=1     # no failures
$ make lint                         # go vet + staticcheck, clean
```

### 8.2 What could not be verified here, and why

The Definition of Done asks for `docker compose up -d --build` to bring four services to healthy
within 60 seconds. **This environment has the Docker CLI but no daemon** (`docker run` fails with
`dial unix /var/run/docker.sock: no such file or directory`), so no container was ever started. That
item is unticked below rather than claimed.

What was verified instead: the file parses, every service and dependency edge is asserted by
`TestBootstrapComposeConfigValid`, and the binary the new service runs is asserted to be built into
its image by `TestBootstrapSeedIsBuiltIntoItsImage` — a container that exits 127 on a missing binary
is the failure that static checks can still catch.

Two things remain unproven until someone runs it with a daemon:

- **UID equality across two images.** `api_key.txt` is written `0600` by `gravix` in the rollup image
  and read by `gravix` in the load-generator image. Both create that user with `adduser -S` on the
  same `alpine:3.21` base and no user-creating package in between, so both should be UID 100 — but
  that is reasoning, not a measurement. If it is ever false, `synthetic-traffic` now says so in one
  line and names the log to read, rather than restarting forever on a bare `cat` failure.
- **Bind-mount ordering.** `dashboard` mounts `./data/dashboard_config.js` as a single file. Docker
  creates a *directory* at a bind-mount source that does not exist, so this depends on
  `bootstrap-init` having completed first — which `depends_on: service_completed_successfully`
  guarantees for `docker compose up`. Mounting `./data` as a directory instead would avoid the
  ordering question and publish `api_key.txt` over HTTP, so the single-file mount is the right trade;
  `TestBootstrapComposeConfigValid` fails if anyone changes it to the directory.

### 8.3 Guards proven by mutation

| Guard | Mutation | Reported |
|---|---|---|
| AC-2 `TestProvisionWritesAPIKeyFileWithCorrectMode` | key file written `0644` | yes — named the mode |
| AC-4 `TestProvisionIsIdempotent` | already-provisioned check disabled | yes — a second key would be minted on every boot |
| F-010 `TestComposeEnvironmentEntriesAreStrings` | re-unquoted the colon-terminated value | yes — and `docker compose config` independently exited 1 on the same mutation |

### 8.4 Defects found

- **SD-013 (open, blocks §1's second promise).** `dashboard_config.js` cannot pre-configure the
  dashboard's API key, because nothing reads `GRAVIX_CONFIG.apiKey` — the dashboard takes its key
  from `localStorage`. Of §5.1's three fields, only `gatewayUrl` has a consumer. Implemented as
  specified regardless, since AC-3 requires the key in the file; the resolution is a product and
  security decision, not an implementation choice.
- **F-010 (fixed).** `docker-compose.bootstrap.yml` had never parsed: `…DATABASE_PATH=:memory:` ends
  in a colon, so unquoted YAML read the entry as a mapping key and Compose rejected the file. The
  documented low-cost deployment path was unusable and failed too early to leave a trace.
- **F-011 (open, out of scope).** `docker-compose.yml` defines `gateway` twice and Compose rejects it
  too. Not fixed: §3 forbids touching that file, and choosing between the two definitions changes
  how the full stack is configured. Between the two findings, every documented Docker path in the
  repository was broken.

### 8.5 Deviations from the spec

| Deviation | Why |
|---|---|
| `provision` takes an `io.Writer` for stdout, not the §5 signature | §6.1 fixes four exact messages. Without an injectable writer they are unassertable, and the already-provisioned message is the one an operator reads most. |
| `docker-compose.bootstrap.yml` also gained a quoting fix and a readability guard in `synthetic-traffic` | AC-7 is unsatisfiable without the first (F-010). The second turns an unreadable key file from an endless restart loop into one line naming the log to read. |
| `.github/workflows/ci.yml` modified, though not in §4.2 | F-010 survived because nothing ever ran `docker compose config` in CI. A finding whose cause is "no gate existed" is only half fixed by fixing the instance. |
| `README.md` modified | The DoD asks for the docs delta removing the manual seed step. |
| `.gitignore` gained five missing command names | `go build ./cmd/bootstrap_seed` writes `./bootstrap_seed` into the repository root, and it reached the index during this work — F-001 (committed binaries) recurring, caught before the commit. The file's own header states the invariant, "every command in this repo needs covering by name", and five commands were uncovered: `bootstrap_seed` and `gen_facts` (new), `demo_seed`, `status_page` and `trial_expiry` (pre-existing). Adding all five costs five lines; a test asserting the invariant has no home in this spec and is worth one of its own. |
| `docs/oss/boundary.yaml` **not** modified | `cmd/bootstrap_seed` is unambiguously core (charter §2.1 "Operate"), so a capability entry would be reasonable — but `make check-boundary` passes without one and §9 says no file outside §4. Left for whoever next edits that map. |

## 9. Definition of done

- [x] All seven acceptance criteria pass with their named tests — §8.1, plus four beyond the seven
- [x] Every Verification command run, real output pasted into the report — §8.1
- [ ] **Not verified: no Docker daemon in this environment.** `docker compose up` was never run, so
      the four-services-healthy claim is not made. What was checked instead, and the two assumptions
      that remain untested, are in §8.2. This is the one item a reviewer must confirm by hand.
- [x] `make check-boundary` clean
- [x] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] **No** — five files outside §4.1/§4.2 were touched, each listed with its reason in §8.5
- [x] `docs-engineer` delta merged — README's bootstrap quick-start no longer copies `.env`, and now
      documents the generated key and how to stop the synthetic traffic
- [x] Zero new skipped or quarantined tests
- [x] `data/api_key.txt` and `data/dashboard_config.js` are covered by the existing `data/` rule at
      `.gitignore:46` — checked before writing anything, per §10

## 10. Escalation

| If you find… | Do this |
|---|---|
| `.gitignore` does not already exclude `data/` | Return `SPEC DEFECT: §4 — data/ is not gitignored, generated secrets would be committed` |
| `docker compose` (v2 plugin) is unavailable in the CI image and only `docker-compose` (v1) exists | Return `SPEC DEFECT: §8 — CI image lacks the docker compose v2 CLI` |
| `services/rollup/Dockerfile`'s builder stage cannot resolve `cmd/bootstrap_seed` because of an unrelated build error already present in the tree | Return `SPEC DEFECT: §4.2 — services/rollup/Dockerfile build broken independent of this spec` |
