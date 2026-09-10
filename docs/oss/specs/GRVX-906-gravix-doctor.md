# SPEC GRVX-906: `gravix doctor` — diagnose the top 10 self-hosted setup failures

| Field | Value |
|---|---|
| **Spec ID** | GRVX-906 |
| **Phase** | 9 |
| **Goal** | G3.5 (`gravix doctor` diagnoses the top 10 setup failures — 10/10) |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 (would a team of ten notice this missing) = YES → core. A self-hoster whose stack fails to boot has no diagnostic path today beyond reading container logs; every team notices that gap on day one. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-901, GRVX-902 |
| **Blocks** | GRVX-910 |
| **Effort** | 5 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Today a self-hoster whose stack fails to boot has no diagnostic tool: `cmd/cli/cmd_status.go`'s
`gravix status` only probes `/live`, `/ready`, `/metrics` on an ingestion endpoint that is assumed
already reachable, and prints nothing about *why* it might not be. After this spec, `gravix doctor`
runs ten fixed, deterministic diagnostics covering the real failure points introduced by
`docker-compose.bootstrap.yml`, GRVX-901's zero-config boot, and GRVX-902's service discovery, and
prints, for every failing check, the exact shell command that resolves it.

## 2. Context the implementer needs

- `cmd/cli/main.go:23-69` — the top-level `switch os.Args[1]` dispatch table; `case "status":
  runStatus(os.Args[2:])` is the pattern to extend with `case "doctor": runDoctor(os.Args[2:])`.
- `cmd/cli/cmd_status.go:13-60` — `runStatus` is the closest existing pattern: builds a
  `flag.FlagSet`, an `http.Client{Timeout: 5 * time.Second}`, loops over named checks, prints one
  line per check with a `✓`/`✗` icon, tracks `allOK`, exits 1 on any failure. `gravix doctor` follows
  this shape but with ten checks instead of three, three icons instead of two, and a `fix:` line
  under every non-`ok` result.
- `cmd/cli/main.go:91-109` — `getEndpoint()` reads `GRAVIX_ENDPOINT` (default
  `http://localhost:8090`); `getAPIKey()` reads `GRAVIX_API_KEY`. `gravix doctor` reuses both.
- `docker-compose.bootstrap.yml:30-56` (`gateway`), `:58-81` (`ingestion`), `:83-134` (`cube`,
  `dashboard`) — the four services a self-hoster's `docker compose up` must bring healthy: ingestion
  on `:8090`→container `:8080`, gateway on `:8091`, cube on `:4000`, dashboard on `:8000`.
- GRVX-901 (`cmd/bootstrap_seed`) writes `<base-dir>/api_key.txt` (plaintext key) and
  `<base-dir>/dashboard_config.js`; `<base-dir>` defaults to `./data`. A missing `api_key.txt` after
  the stack has been up for more than a few seconds means `bootstrap-init` failed or never ran.
- GRVX-901 also creates `<base-dir>/gravix.db`, the tenant SQLite database opened via
  `tenantdb.Open` (`pkg/tenantdb/sqlite.go:26-37`).
- GRVX-902 (`pkg/discovery`) adds `GET /api/v1/services` to ingestion, scope `"admin:read"` — an
  unrestricted API key (the only kind `bootstrap_seed` issues) satisfies any scope
  (`pkg/tenantdb/tenantdb.go:59-65`). Doctor reuses this endpoint to validate the API key end to end,
  not just its shape.
- `storage/dashboard/nginx.conf` — GRVX-902 adds `http://localhost:8090` to the dashboard's
  `Content-Security-Policy` `connect-src` directive. If a self-hoster is running a modified or
  stale `nginx.conf`, the *browser* silently blocks the dashboard's fetches to ingestion while every
  server-side health check still reports healthy — this is invisible to `docker compose ps` and to
  `gravix status`, and is exactly the kind of failure `gravix doctor` exists to catch. Doctor
  verifies it by reading the **live** `Content-Security-Policy` response header from the running
  dashboard container, not by reading the repository's `nginx.conf` file — `gravix` is distributed
  via Homebrew and `go install` (`docs/oss/specs/GRVX-711...` README) and frequently runs with no
  repository checkout present at all.
- `go.mod:3` — `go 1.24.9`. A contributor building from source with an older Go fails at `go build`
  with a message that does not explain *which* version is required.
- Cube.js's own healthcheck (`docker-compose.bootstrap.yml:103-109`) probes `GET /readyz` on
  `:4000`.

## 3. Non-goals for this spec

- Do NOT implement auto-remediation. Every failing check prints a fix command; `gravix doctor` never
  executes one itself. A tool that silently mutates a user's stack on their behalf is a bigger
  support-burden risk than the setup failures it diagnoses.
- Do NOT read `storage/dashboard/nginx.conf` from a local repository checkout. Check 10
  (§6, `checkDashboardCSP`) probes the **live** `Content-Security-Policy` HTTP response header from
  the running dashboard container, because `gravix` binaries are frequently run with no repository
  present.
- Do NOT add a `-json` machine-readable output flag. Ten fixed, human-readable lines plus a summary
  is the full scope; a machine-readable mode is a future enhancement with its own spec.
- Do NOT change `gravix status`, `gravix send`, `gravix tail`, or `gravix replay`. This spec only
  adds `gravix doctor` and its dispatch case in `cmd/cli/main.go`.
- This spec does not cross non-goal §3 (No Agents) because `gravix doctor` runs once, on demand, at
  the operator's own command, and performs read-only checks — it is not a resident daemon and
  collects no host metrics.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `cmd/cli/cmd_doctor.go` | The ten diagnostics, `DoctorConfig`, `runDoctor`, `doctorRun` |
| `cmd/cli/cmd_doctor_test.go` | Tests for every check and the aggregate exit-code contract |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `cmd/cli/main.go` | Add `case "doctor": runDoctor(os.Args[2:])` to the dispatch switch; add a `gravix doctor` line to `printUsage()` |
| `cmd/cli/main_test.go` | Add a dispatch-table test asserting `"doctor"` routes to `runDoctor` (following whatever existing pattern this file uses for `"status"`/`"tail"`) |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cmd/cli/cmd_status.go`, `cmd_send.go`, `cmd_tail.go`, `cmd_replay.go` | Unrelated existing commands; this spec is purely additive |
| `services/ingestion/main.go` | Doctor is an external, read-only HTTP client of ingestion; it needs no server-side change |
| `storage/dashboard/nginx.conf` | Doctor observes this file's *effect* (the live CSP header), never its source text |

## 5. Interface contract

```go
// package main (cmd/cli)

// DoctorStatus is the outcome of one diagnostic check.
type DoctorStatus string

const (
	DoctorOK   DoctorStatus = "ok"
	DoctorWarn DoctorStatus = "warn"
	DoctorFail DoctorStatus = "fail"
)

// DoctorCheck is the result of one diagnostic.
type DoctorCheck struct {
	Name   string       // fixed, one of the ten names in §6
	Status DoctorStatus
	Detail string       // one line, always non-empty
	FixCmd string       // exact shell command; non-empty whenever Status != DoctorOK
}

// DoctorConfig carries every dependency a check needs, so every check is a
// pure function of cfg and is deterministically testable without a live
// Docker daemon, network, or filesystem beyond what the test supplies.
type DoctorConfig struct {
	Endpoint     string        // ingestion base URL, no trailing slash
	GatewayURL   string        // gateway base URL, no trailing slash
	CubeURL      string        // cube base URL, no trailing slash
	DashboardURL string        // dashboard base URL, no trailing slash
	BaseDir      string        // local data directory (bootstrap stack), e.g. "./data"
	APIKey       string        // GRAVIX_API_KEY; may be empty
	HTTPClient   *http.Client  // 3-second timeout

	LookPath   func(file string) (string, error)
	RunCommand func(name string, args ...string) ([]byte, error)
	Stat       func(name string) (os.FileInfo, error)
	Glob       func(pattern string) ([]string, error)
}

// NewDoctorConfig builds a DoctorConfig with every real dependency wired in:
// exec.LookPath, a func running exec.Command(name, args...).CombinedOutput(),
// os.Stat, filepath.Glob, and an *http.Client with a 3-second Timeout.
func NewDoctorConfig(endpoint, gatewayURL, cubeURL, dashboardURL, baseDir, apiKey string) DoctorConfig

// AllChecks is the fixed, ordered list of the ten diagnostics gravix doctor
// runs, in the order printed. Order matches §6's numbering.
var AllChecks = []func(DoctorConfig) DoctorCheck{
	checkDockerCLI,
	checkGoVersion,
	checkEnvConfigured,
	checkPortsAvailable,
	checkIngestionReachable,
	checkIngestionAuth,
	checkTenantDBSeeded,
	checkCubeReachable,
	checkWarehouseData,
	checkDashboardCSP,
}

// doctorRun executes every check in AllChecks, in order, against cfg and
// returns their results. It performs no I/O to stdout and never calls
// os.Exit — it is the unit under test for every AC in §7.
func doctorRun(cfg DoctorConfig) []DoctorCheck

// runDoctor is cmd/cli/main.go's entry point for `gravix doctor`. It parses
// flags, builds a DoctorConfig via NewDoctorConfig, calls doctorRun, prints
// results per §6.2, and calls os.Exit(1) if any DoctorCheck.Status ==
// DoctorFail, else returns (implicit exit 0).
func runDoctor(args []string)
```

### 5.1 Flags

| Flag | Type | Default | Help string |
|---|---|---|---|
| `-endpoint` | `string` | `""` (falls back to `GRAVIX_ENDPOINT`, then `http://localhost:8090`) | `Ingestion endpoint (env: GRAVIX_ENDPOINT)` |
| `-gateway-endpoint` | `string` | `http://localhost:8091` | `Gateway endpoint` |
| `-cube-endpoint` | `string` | `http://localhost:4000` | `Cube.js endpoint` |
| `-dashboard-endpoint` | `string` | `http://localhost:8000` | `Dashboard endpoint` |
| `-base-dir` | `string` | `./data` | `Local data directory used by the bootstrap stack` |

## 6. Behaviour

`doctorRun(cfg)` calls each function in `AllChecks` in order and collects the results. Each check:

1. **`checkDockerCLI`** — "Docker CLI available". `_, err := cfg.LookPath("docker")`. `err != nil` →
   `DoctorFail`, Detail `` "docker" was not found on PATH ``, FixCmd
   `` install Docker: https://docs.docker.com/get-docker/ ``. Else `DoctorOK`, Detail
   `` docker CLI found ``.
2. **`checkGoVersion`** — "Go toolchain (source builds only)". `out, err :=
   cfg.RunCommand("go", "version")`. `err != nil` → `DoctorFail`, Detail `` go was not found on PATH
   ``, FixCmd `` install Go 1.24 or newer: https://go.dev/dl/ ``. Else parse the first
   `\d+\.\d+` after `go` in `out` with regex `` go(\d+)\.(\d+) ``; if the parsed `(major, minor)` is
   less than `(1, 24)` → `DoctorFail`, Detail `` go <parsed> is older than go.mod's requirement of
   1.24 ``, same FixCmd as above. Else `DoctorOK`, Detail `` go <parsed> satisfies go.mod's 1.24
   requirement ``. Unparseable output is treated as `DoctorFail` with Detail
   `` could not parse "go version" output: <out> `` and the same FixCmd.
3. **`checkEnvConfigured`** — "Environment configured". `_, envErr := cfg.Stat(".env")`.
   `_, keyErr := cfg.Stat(filepath.Join(cfg.BaseDir, "api_key.txt"))`. If both `envErr != nil` and
   `keyErr != nil` → `DoctorFail`, Detail `` neither ".env" nor "<BaseDir>/api_key.txt" was found —
   neither the full stack nor the bootstrap stack has been configured ``, FixCmd
   `` cp .env.example .env   # full stack — or —   docker compose -f docker-compose.bootstrap.yml up
   -d --build   # bootstrap stack, generates api_key.txt automatically ``. Else `DoctorOK`, Detail
   `` found <".env" and/or "<BaseDir>/api_key.txt"> ``.
4. **`checkPortsAvailable`** — "Required ports". For each of `8000, 8090, 8091, 4000`, attempt
   `net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)`; a
   successful dial means something is already listening. If one or more ports respond → `DoctorWarn`
   (not `DoctorFail` — the listener may be Gravix's own healthy container), Detail
   `` ports already in use: <comma-joined list> — this is expected once the stack is up; if
   `docker compose up` is currently failing to start, one of these may be a conflicting process ``,
   FixCmd `` lsof -i :<first port> ``. If none respond → `DoctorOK`, Detail `` ports 8000, 8090,
   8091, 4000 are free ``.
5. **`checkIngestionReachable`** — "Ingestion service reachable". `GET <cfg.Endpoint>/live` via
   `cfg.HTTPClient`. Non-`200` or transport error → `DoctorFail`, Detail
   `` GET <Endpoint>/live failed: <error or "status <code>"> ``, FixCmd
   `` docker compose -f docker-compose.bootstrap.yml up -d --build ``. Else `DoctorOK`, Detail
   `` ingestion is healthy at <Endpoint> ``.
6. **`checkIngestionAuth`** — "API key valid". `cfg.APIKey == ""` → `DoctorFail`, Detail
   `` GRAVIX_API_KEY is not set ``, FixCmd
   `` export GRAVIX_API_KEY=$(cat <BaseDir>/api_key.txt) ``. Else `GET <cfg.Endpoint>/api/v1/services`
   with header `X-API-Key: <cfg.APIKey>`. Transport error → `DoctorWarn`, Detail
   `` could not verify the API key: <error> — check "Ingestion service reachable" above ``, FixCmd
   `""`. `401` → `DoctorFail`, Detail `` GET <Endpoint>/api/v1/services returned 401 — the API key is
   invalid ``, FixCmd `` export GRAVIX_API_KEY=$(cat <BaseDir>/api_key.txt) ``. `200` → `DoctorOK`,
   Detail `` API key is valid ``. Any other status → `DoctorFail`, Detail
   `` GET <Endpoint>/api/v1/services returned <code> ``, FixCmd
   `` docker compose -f docker-compose.bootstrap.yml logs ingestion ``.
7. **`checkTenantDBSeeded`** — "Tenant database seeded". `_, err :=
   cfg.Stat(filepath.Join(cfg.BaseDir, "gravix.db"))`. `err != nil` → `DoctorWarn` (legacy
   single-API-key mode does not need this file), Detail
   `` <BaseDir>/gravix.db not found — fine in legacy single-API-key mode; if you expected the
   bootstrap stack, bootstrap-init may not have finished yet ``, FixCmd
   `` docker compose -f docker-compose.bootstrap.yml logs bootstrap-init ``. Else `DoctorOK`, Detail
   `` <BaseDir>/gravix.db exists ``.
8. **`checkCubeReachable`** — "Cube.js reachable". `GET <cfg.CubeURL>/readyz`. Non-`200` or
   transport error → `DoctorFail`, Detail `` GET <CubeURL>/readyz failed: <error or "status <code>">
   ``, FixCmd `` docker compose -f docker-compose.bootstrap.yml logs cube ``. Else `DoctorOK`, Detail
   `` cube.js is healthy at <CubeURL> ``.
9. **`checkWarehouseData`** — "Rollup has produced data". `matches, err :=
   cfg.Glob(filepath.Join(cfg.BaseDir, "warehouse", "request_metrics_minute", "*", "*.parquet"))`.
   `err != nil || len(matches) == 0` → `DoctorWarn`, Detail
   `` no Parquet files under <BaseDir>/warehouse/request_metrics_minute yet — the first rollup can
   take up to ~4 minutes after traffic starts (see the dashboard's first-run countdown) ``, FixCmd
   `` docker compose -f docker-compose.bootstrap.yml logs request-metrics-rollup ``. Else `DoctorOK`,
   Detail `` found <len(matches)> Parquet file(s) under <BaseDir>/warehouse/request_metrics_minute ``.
10. **`checkDashboardCSP`** — "Dashboard CSP allows ingestion". `GET <cfg.DashboardURL>/`. Transport
    error or non-`200` → `DoctorFail`, Detail `` GET <DashboardURL>/ failed: <error or "status
    <code>"> ``, FixCmd `` docker compose -f docker-compose.bootstrap.yml up -d --build ``. Else read
    the response's `Content-Security-Policy` header; if it does not contain `cfg.Endpoint` as a
    substring → `DoctorFail`, Detail `` the dashboard's Content-Security-Policy does not list
    <Endpoint> in connect-src — the browser will block requests to ingestion ``, FixCmd
    `` check storage/dashboard/nginx.conf's connect-src directive includes <Endpoint>, then: docker
    compose -f docker-compose.bootstrap.yml restart dashboard ``. Else `DoctorOK`, Detail
    `` dashboard's Content-Security-Policy allows <Endpoint> ``.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Any check returns `DoctorFail` | `runDoctor` exits 1 after printing all ten results | (see §6.2 summary format) |
| Every check returns `DoctorOK` or `DoctorWarn` | `runDoctor` exits 0 | `All checks passed.` |
| At least one `DoctorFail` present | final line before exit | `Some checks failed. Run the "fix:" command under each failing check, then re-run: gravix doctor` |

### 6.2 Output format

For each `DoctorCheck` in order, `runDoctor` prints one line:

```
<icon> <Name>: <Detail>
```

where `<icon>` is `✓` for `DoctorOK`, `⚠` for `DoctorWarn`, `✗` for `DoctorFail`. When
`Status != DoctorOK` and `FixCmd != ""`, print an indented second line:

```
    fix: <FixCmd>
```

After all ten checks, print a blank line then the summary line per §6.1.

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `AllChecks` contains exactly 10 entries, in the exact order listed in §6 | `TestAllChecksHasTenEntriesInOrder` |
| AC-2 | `checkDockerCLI` returns `DoctorFail` with the exact FixCmd when `LookPath` errors, `DoctorOK` when it succeeds | `TestCheckDockerCLI` |
| AC-3 | `checkGoVersion` returns `DoctorFail` for a parsed version below 1.24 and `DoctorOK` for 1.24 and 1.25 | `TestCheckGoVersion` |
| AC-4 | `checkEnvConfigured` returns `DoctorFail` only when both `.env` and `<BaseDir>/api_key.txt` are absent | `TestCheckEnvConfigured` |
| AC-5 | `checkPortsAvailable` returns `DoctorWarn` when a port dial succeeds and `DoctorOK` when all four refuse | `TestCheckPortsAvailable` |
| AC-6 | `checkIngestionReachable` returns `DoctorFail` on a non-200 `/live` and `DoctorOK` on 200 | `TestCheckIngestionReachable` |
| AC-7 | `checkIngestionAuth` returns `DoctorFail` with the exact "GRAVIX_API_KEY is not set" detail when `cfg.APIKey == ""`, and `DoctorFail` with the 401 detail when the server returns 401 | `TestCheckIngestionAuth` |
| AC-8 | `checkTenantDBSeeded` returns `DoctorWarn` when `gravix.db` is absent and `DoctorOK` when present | `TestCheckTenantDBSeeded` |
| AC-9 | `checkCubeReachable` returns `DoctorFail` on a non-200 `/readyz` and `DoctorOK` on 200 | `TestCheckCubeReachable` |
| AC-10 | `checkWarehouseData` returns `DoctorWarn` when `Glob` finds zero Parquet files and `DoctorOK` when it finds at least one | `TestCheckWarehouseData` |
| AC-11 | `checkDashboardCSP` returns `DoctorFail` when the live `Content-Security-Policy` header omits `cfg.Endpoint`, and `DoctorOK` when it is present | `TestCheckDashboardCSP` |
| AC-12 | `doctorRun` on a config where every check is engineered to fail returns a slice with 10 `DoctorFail` results, and `runDoctor`'s exit path (tested by extracting the exit-decision into a pure helper `anyFailed(checks []DoctorCheck) bool`) returns `true` | `TestDoctorRunAggregateExitDecision` |

## 8. Verification

```bash
# 1. Every check function, in isolation
go test ./cmd/cli/... -run TestCheck -v
# expect: PASS for AC-2 through AC-11

# 2. Ordering and aggregate exit-code contract
go test ./cmd/cli/... -run "TestAllChecksHasTenEntriesInOrder|TestDoctorRunAggregateExitDecision" -v
# expect: PASS

# 3. Dispatch wiring
go test ./cmd/cli/... -run TestMain -v
# expect: PASS, including the new "doctor" dispatch case

# 4. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 5. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (README or quick-start mentions `gravix doctor`) or `NO DOCS
      DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] Every `DoctorFail`-producing check in §6 has a non-empty `FixCmd` (verified by AC-12's fixture)

## 10. Escalation

| If you find… | Do this |
|---|---|
| A real setup failure not covered by the ten checks in §6 (e.g. a Docker-Desktop-on-macOS-specific failure) | Return `SPEC DEFECT: §6 — an eleventh known failure mode exists; §5's AllChecks is a fixed ten and needs a version bump to add one` |
| `checkGoVersion`'s regex fails to parse a real `go version` output format on a supported platform | Return `SPEC DEFECT: §6 step 2 — go version output format assumption is wrong on <platform>` |
| `net.DialTimeout` in `checkPortsAvailable` is unreliable in the CI sandbox (no loopback, or ports pre-bound by the CI runner itself) | Return `SPEC DEFECT: §6 step 4 — port-check approach is not portable to CI` |
