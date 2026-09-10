# SPEC GRVX-1302: The extension-point framework — the only door `ee/` is allowed through

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1302 |
| **Phase** | 13 |
| **Goal** | G7.7 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.1, §7.2 — the core must build, test and run with `ee/` physically deleted, and `ee/` code may never be smuggled into a place core depends on. This spec is the mechanism: it defines the one registry core calls into, proves the call site is silent when nothing is registered, and relocates the gateway's business logic into an importable package so a second, `ee/`-only binary can compose with it without core ever importing `ee/`. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701, GRVX-702, GRVX-703, GRVX-704, GRVX-1301 |
| **Blocks** | GRVX-1303, GRVX-1304, GRVX-1305, GRVX-1306, GRVX-1307, GRVX-1308, GRVX-1309, GRVX-1310, GRVX-1311, GRVX-1312 |
| **Effort** | 10 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec: (1) `pkg/extpoint` exists — a small, generic registry that lets one `http.Handler`
per named `ee/` feature be mounted onto the gateway's existing HTTP listener, with zero coupling in
either direction until something registers; (2) the gateway's business logic, currently
`package main` in `services/gateway/`, is relocated verbatim (no logic change) into the importable
`pkg/gatewaycore`, leaving `services/gateway/main.go` as a five-line OSS entrypoint; (3) a second
entrypoint, `ee/cmd/gateway/main.go`, exists that calls the identical `gatewaycore.Run()` and, when
built with real `ee/` packages later, blank-imports them so their `init()` can register; (4) every
Phase 13 `ee/` capability this Lead has ratified is recorded in `docs/oss/boundary.yaml` with its
Crippleware Test, so no later spec has to self-declare its own placement. With nothing registered —
the permanent state of every OSS install — the new call sites add zero routes, log zero lines, and
are indistinguishable from the code before this spec existed.

## 2. Context the implementer needs

- `services/gateway/main.go`, `enterprise.go`, `gateway_alerts.go`, `gateway_auth.go`,
  `gateway_billing.go`, `gateway_dashboards.go`, `gateway_platform.go`, `openapi.go` are all
  `package main`. `main_test.go` and `phase6_test.go` are the two test files, also `package main`.
  Total 10,468 lines across these 10 files (`wc -l services/gateway/*.go`). Nothing outside this
  directory imports it, because a `package main` cannot be imported — verified: no other file in
  the repository references `gravix-dashboards/services/gateway`.
- `services/gateway/main.go:195-498` is the entire `func main()`: flag parsing, env validation,
  construction of `*gateway` and its dependencies, `mux := http.NewServeMux()` at line 360, ~65
  `mux.HandleFunc`/`mux.Handle` registrations through line 459, `srv := &http.Server{...}` at line
  468, `srv.ListenAndServe()` at line 493.
- `services/gateway/main.go:578-595` — `type gateway struct { db tenantdb.DB; tokens
  *auth.TokenService; billing billing.Service; notifier *notify.Dispatcher; store
  storage.ObjectStore; rateLimiter *ratelimit.TenantLimiter; ipLimiter *ratelimit.IPLimiter;
  emailSender email.Sender; captchaVerifier captcha.Verifier; cubeAPIURL, cubeAPISecret, jwtSecret,
  baseURL string; totpKey []byte; activeExports map[string]bool; activeExportsMu sync.Mutex }`.
- `pkg/license` (`GRVX-1301`) exists: `license.FromEnv() (*license.License, error)`,
  `license.License.HasFeature(id string) bool`. It performs zero I/O beyond one env/file read — see
  `TestLicensePackageHasNoNetworkImports`.
- `pkg/boundary` (`GRVX-703`) exists: `boundary.Load(path string) (*boundary.Map, error)`,
  `boundary.Capability{ID, Name, Placement, CharterRef, Rationale string; CrippleWareTest
  *boundary.CrippleWareTest; Paths []string; Gate string}`, `boundary.CrippleWareTest{
  Q1TeamOfTenNotices, Q2AffectsAccuracy, Q3WorseSecurity, Q4PreviouslyOpen, Q5OnlyReasonIsMoney
  bool}` (all `yaml` tags per `GRVX-703 §5.2`). `docs/oss/boundary.yaml` today (after `GRVX-703`,
  `GRVX-710`) records 18 capabilities: 15 `core`, 3 `ee` (`multi-tenancy`, `billing`,
  `tenant-branding`, `charter_ref` `§2.2`/`§2.2`/`§2.3`).
- `pkg/auth.TokenService.Validate(tokenStr string) (*auth.Claims, error)` and
  `auth.NewTokenService(secret string, duration time.Duration) *auth.TokenService` — JWT validation
  is a pure HMAC check against a shared secret; a second `TokenService` built from the same
  `JWT_SECRET` value validates the same tokens with no shared state required.
- `go.mod:1` — module `github.com/lgreene/gravix-dashboards`. `go.mod:3` — Go 1.24.9.
  `modernc.org/sqlite v1.46.1` (`go.mod:22`) is the only SQL driver in the module and is
  CGO-free — every `ee/` package that needs its own storage in this phase uses it.
- `ee/` exists (`GRVX-702`): `ee/LICENSE`, `ee/LICENSE_HEADER.txt`, `ee/README.md`,
  `ee/placeholder/doc.go`. `Makefile` has `build-oss`, `test-oss`, `check-boundary` targets
  (`GRVX-704`) that operate on a copy of the tree with `ee/` physically removed.
- `cmd/checkboundary`'s `checkImports` (`GRVX-704 §5.3`) walks every `*.go` file under `root`
  **excluding `ee/`**, parses imports with `go/parser` in `parser.ImportsOnly` mode — it does not
  evaluate build constraints. **A file physically located under `services/`, `pkg/`, or any other
  non-`ee/` path can never contain the literal import string `gravix-dashboards/ee/`, gated or not
  — `check-boundary` would flag it regardless of a `//go:build` tag.** This is why the file that
  imports `ee/` packages must live physically inside `ee/`.

## 3. Non-goals for this spec

- Do NOT change any HTTP handler's business logic. Every `func (gw *gateway) handleXxx` moves
  verbatim; only its package changes.
- Do NOT change `docker-compose.yml`, any `Dockerfile`, or `deploy/gravix/` (Helm). `go build -o
  bin/gateway ./services/gateway/` still produces the same binary name from the same path — the
  build command in every deployment artefact is unchanged.
- Do NOT implement any real `ee/` feature. `ee/cmd/gateway/main.go` blank-imports nothing yet; it
  proves the mechanism with zero registrants.
- Do NOT implement licence expiry / read-only degrade logic. That is `GRVX-1303`.
- Do NOT add a second interface type to `pkg/extpoint` beyond `Extension`. Every Phase 13
  capability that needs request-time behaviour mounts an `http.Handler`; every capability that
  needs background work runs it from its own `ee/cmd/*/main.go`, independent of this registry. A
  second interface here would be unused surface — the eight subsequent specs are designed against
  exactly this one.
- Do NOT rule on the placement of any Phase 13 capability yourself beyond transcribing this Lead's
  rulings from `docs/oss/20-roadmap-horizon-2.md` Phase 13 and this document's §6 step 8 into
  `boundary.yaml`. Placement decisions are recorded here, in the spec, not invented by the
  implementer.
- This spec does not cross non-goal `docs/04-non-goals.md` §4 (No Real-Time Dashboards) or §6 (No
  Custom Query Language) — it is a process-composition and registry change, not a query or latency
  change.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/extpoint/extpoint.go` | `Extension` interface, `Register`, `Registered`, `Mounted` |
| `pkg/extpoint/extpoint_test.go` | Tests, including the empty-registry graceful-absence test |
| `pkg/gatewaycore/main.go` | Relocated from `services/gateway/main.go`; `package gatewaycore`; `func main()` renamed to `func Run()`; adds the mount loop and `/api/gateway/ee/status` |
| `pkg/gatewaycore/enterprise.go` | Relocated from `services/gateway/enterprise.go`; `package gatewaycore` only |
| `pkg/gatewaycore/gateway_alerts.go` | Relocated from `services/gateway/gateway_alerts.go`; `package gatewaycore` only |
| `pkg/gatewaycore/gateway_auth.go` | Relocated from `services/gateway/gateway_auth.go`; `package gatewaycore` only |
| `pkg/gatewaycore/gateway_billing.go` | Relocated from `services/gateway/gateway_billing.go`; `package gatewaycore` only |
| `pkg/gatewaycore/gateway_dashboards.go` | Relocated from `services/gateway/gateway_dashboards.go`; `package gatewaycore` only |
| `pkg/gatewaycore/gateway_platform.go` | Relocated from `services/gateway/gateway_platform.go`; `package gatewaycore` only |
| `pkg/gatewaycore/openapi.go` | Relocated from `services/gateway/openapi.go`; `package gatewaycore` only |
| `pkg/gatewaycore/main_test.go` | Relocated from `services/gateway/main_test.go`; `package gatewaycore` only |
| `pkg/gatewaycore/phase6_test.go` | Relocated from `services/gateway/phase6_test.go`; `package gatewaycore` only |
| `pkg/gatewaycore/ee_mount_test.go` | New tests for the mount loop and `/api/gateway/ee/status`, using a fake `extpoint.Extension` registered only inside this test binary |
| `ee/cmd/gateway/main.go` | The Enterprise entrypoint skeleton: calls `gatewaycore.Run()`, imports zero `ee/` sub-packages today |
| `ee/cmd/gateway/main_test.go` | Asserts the file compiles and its import block contains no `ee/` sub-package yet (this test is edited to assert exactly one new import per spec in `GRVX-1304`–`GRVX-1311`, see each spec's own §4.2) |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `services/gateway/main.go` | Deleted and replaced with the five-line OSS entrypoint in §5.4 |
| `services/gateway/enterprise.go`, `gateway_alerts.go`, `gateway_auth.go`, `gateway_billing.go`, `gateway_dashboards.go`, `gateway_platform.go`, `openapi.go`, `main_test.go`, `phase6_test.go` | Deleted; content relocated verbatim to `pkg/gatewaycore/` (see §4.1) |
| `docs/oss/boundary.yaml` | Add the 8 Phase 13 `ee` capability ids from §6 step 8, each with a full `crippleware_test` block (all five answers `no`) and `charter_ref` |
| `Makefile` | Add one target, `build-ee`, per §5.5. No existing target changes. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `pkg/tenantdb/**`, `pkg/billing/**`, `pkg/sso/**`, `pkg/notify/**`, `pkg/auth/**` | Their behaviour does not change; only their caller's package changes |
| `docker-compose.yml`, `Dockerfile*`, `deploy/gravix/**` | The OSS build command and output path are unchanged; see §3 |
| `ee/LICENSE`, `ee/LICENSE_HEADER.txt`, `ee/README.md`, `ee/placeholder/**` | Owned by `GRVX-702`; unrelated to this spec |

## 5. Interface contract

### 5.1 `pkg/extpoint/extpoint.go`

```go
// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package extpoint is the one registry ee/ code is allowed to reach core through.
//
// Core never imports ee/. Instead, ee/ packages register an Extension here from their
// own init() function, and core's gatewaycore.Run mounts whatever is registered onto
// its existing HTTP listener. In the OSS binary, ee/ is never imported by anything in
// the build graph, so Registered() always returns an empty slice, the mount loop in
// gatewaycore never executes its body, and /api/gateway/ee/status always reports
// {"extensions":[]}. This is not a special case in the code — it is what an empty
// registry does by construction. No path in this package logs, warns, or errors when
// the registry is empty; emptiness is the permanent, correct state of every OSS install
// (charter §7.4, §7.5).
package extpoint

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Extension is implemented by an ee/ package that wants to mount its own HTTP surface
// onto the shared gateway process. Each ee/ feature package (ee/tenancy, ee/billing,
// ee/identity, ...) registers exactly one Extension from its own init().
type Extension interface {
	// Name is a short, stable, lowercase identifier, e.g. "tenancy", "identity". Used
	// only for the /api/gateway/ee/status listing and registry bookkeeping — it has no
	// effect on routing.
	Name() string

	// PathPrefix is the exact mount point, e.g. "/ee/tenancy/". Must start and end with
	// "/". The registered Handler receives requests with this prefix already stripped
	// (net/http.StripPrefix semantics): a request to "/ee/tenancy/policies" reaches the
	// handler as "/policies".
	PathPrefix() string

	// Handler is the http.Handler mounted at PathPrefix. It is called once, at
	// registration time, and the returned Handler is reused for the life of the
	// process — Handler must not depend on per-request state to construct itself.
	Handler() http.Handler
}

var (
	mu         sync.RWMutex
	extensions = map[string]Extension{} // keyed by Name()
	byPrefix   = map[string]Extension{} // keyed by PathPrefix()
)

// Register adds ext to the registry. It panics if ext, ext.Name(), or ext.PathPrefix()
// is invalid, if PathPrefix does not both start and end with "/", or if the Name or
// PathPrefix collides with an already-registered Extension. Register is called only
// from package init() functions in ee/ packages, so a panic here is a build-time
// programming error, never a runtime condition an operator can trigger — it must never
// be reached in the OSS binary, where this function is never called.
func Register(ext Extension) {
	if ext == nil {
		panic("extpoint: Register called with a nil Extension")
	}
	name := ext.Name()
	prefix := ext.PathPrefix()
	if name == "" {
		panic("extpoint: Extension.Name() must be non-empty")
	}
	if len(prefix) < 2 || prefix[0] != '/' || prefix[len(prefix)-1] != '/' {
		panic(fmt.Sprintf("extpoint: Extension %q PathPrefix() = %q must start and end with \"/\"", name, prefix))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := extensions[name]; exists {
		panic(fmt.Sprintf("extpoint: Extension name %q registered twice", name))
	}
	if _, exists := byPrefix[prefix]; exists {
		panic(fmt.Sprintf("extpoint: PathPrefix %q registered twice", prefix))
	}
	extensions[name] = ext
	byPrefix[prefix] = ext
}

// Registered returns every registered Extension, sorted by Name for deterministic
// iteration. In the OSS binary this is always an empty, non-nil slice.
func Registered() []Extension {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Extension, 0, len(extensions))
	for _, ext := range extensions {
		out = append(out, ext)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Mounted returns the Extension registered at the exact PathPrefix, and whether one was
// found. Used by gatewaycore's mount loop.
func Mounted(prefix string) (Extension, bool) {
	mu.RLock()
	defer mu.RUnlock()
	ext, ok := byPrefix[prefix]
	return ext, ok
}
```

### 5.2 Mount loop and status endpoint — inserted into `pkg/gatewaycore/main.go`

Inserted immediately after the last existing `mux.HandleFunc`/`mux.Handle` call (the line
registering `"/metrics"`, formerly `services/gateway/main.go:459`) and before the `srv :=
&http.Server{...}` construction (formerly `main.go:468`). No line between the existing
registrations and this insertion changes.

```go
// Mount every registered ee/ extension. In the OSS binary, extpoint.Registered()
// is always empty and this loop body never executes — zero routes added, zero log
// lines, by construction (see pkg/extpoint doc comment).
for _, ext := range extpoint.Registered() {
	prefix := ext.PathPrefix()
	mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), ext.Handler()))
}
mux.HandleFunc("/api/gateway/ee/status", handleEEStatus)
```

```go
// eeStatusEntry is one row of the /api/gateway/ee/status response.
type eeStatusEntry struct {
	Name       string `json:"name"`
	PathPrefix string `json:"path_prefix"`
}

// handleEEStatus handles GET /api/gateway/ee/status — unauthenticated, read-only,
// lists every mounted ee/ extension by name and prefix. Returns {"extensions":[]} on
// every OSS install, always 200, never 404/500/503. This is a diagnostic endpoint
// (used by `gravix doctor` and, later, ee/fleet health checks) — it is never rendered
// in the OSS dashboard UI, so it is not an upsell surface (charter §7.4).
func handleEEStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	exts := extpoint.Registered()
	entries := make([]eeStatusEntry, 0, len(exts))
	for _, ext := range exts {
		entries = append(entries, eeStatusEntry{Name: ext.Name(), PathPrefix: ext.PathPrefix()})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"extensions": entries})
}
```

`main.go`'s `import` block gains exactly two new entries: `"strings"` (already imported —
verify, do not duplicate) and `"github.com/lgreene/gravix-dashboards/pkg/extpoint"`.

### 5.3 `func main()` → `func Run()`

The full body of `services/gateway/main.go:195-498` (formerly `func main()`) is relocated to
`pkg/gatewaycore/main.go` with exactly this signature change and nothing else:

```go
// Run starts the Gravix gateway. It blocks until the process receives a shutdown
// signal or the HTTP server returns a non-ErrServerClosed error. Run calls
// os.Exit(1) directly on unrecoverable configuration errors (missing TENANT_DB_PATH,
// missing or too-short JWT_SECRET, malformed STRIPE_SECRET_KEY, etc.) — this is
// unchanged from the prior func main() behaviour, only its name changed.
func Run() {
	// ... identical body ...
}
```

Every other top-level declaration in these files (`type gateway struct`, every `func (gw
*gateway) handleXxx`, every package-level `var`, every `init()` registering Prometheus
collectors) moves unchanged except for the `package main` → `package gatewaycore` line at the top
of each file.

### 5.4 New `services/gateway/main.go` — the entire file

```go
// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gateway is the Gravix OSS gateway entrypoint. It imports only
// pkg/gatewaycore — never ee/ — so this binary is, by construction, the OSS
// gateway with zero paid capability, whether or not ee/ exists in the working tree.
package main

import "github.com/lgreene/gravix-dashboards/pkg/gatewaycore"

func main() {
	gatewaycore.Run()
}
```

### 5.5 `ee/cmd/gateway/main.go` — the Enterprise entrypoint skeleton

```go
// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command gateway is the Gravix Enterprise entrypoint. It calls the identical
// gatewaycore.Run used by the OSS binary. The only difference between this binary
// and services/gateway's is the import block below: each ee/ feature package this
// binary should include is blank-imported here so its init() registers with
// pkg/extpoint before Run mounts extensions. Today this list is empty — no ee/
// feature exists yet. GRVX-1304 through GRVX-1311 each add exactly one import line.
package main

import (
	// ee/ feature imports go here, one per shipped capability. Empty today.

	"github.com/lgreene/gravix-dashboards/pkg/gatewaycore"
)

func main() {
	gatewaycore.Run()
}
```

### 5.6 `Makefile` addition

```make
.PHONY: build-ee

build-ee: ## Build the Enterprise gateway (requires ee/ present)
	go build -o bin/gateway-ee ./ee/cmd/gateway/
```

## 6. Behaviour

1. `mkdir -p pkg/gatewaycore ee/cmd/gateway`.
2. For each of `main.go`, `enterprise.go`, `gateway_alerts.go`, `gateway_auth.go`,
   `gateway_billing.go`, `gateway_dashboards.go`, `gateway_platform.go`, `openapi.go`,
   `main_test.go`, `phase6_test.go`: `git mv services/gateway/<file> pkg/gatewaycore/<file>`, then
   change only the `package main` line to `package gatewaycore`. Do not reformat, reorder imports
   beyond what `gofmt` requires, or touch any other line.
3. In `pkg/gatewaycore/main.go`, rename `func main()` to `func Run()` (§5.3) and insert the mount
   loop and `handleEEStatus` (§5.2) at the exact point specified. Add the `pkg/extpoint` import.
4. Create `services/gateway/main.go` with exactly the §5.4 content.
5. Create `ee/cmd/gateway/main.go` and `ee/cmd/gateway/main_test.go` with the §5.5 content and a
   test asserting the import block contains zero occurrences of `gravix-dashboards/ee/` today.
6. Create `pkg/extpoint/extpoint.go` (§5.1) and `pkg/extpoint/extpoint_test.go`.
7. Create `pkg/gatewaycore/ee_mount_test.go`: define a package-private fake `type fakeExt
   struct{}` implementing `Extension` with `Name() "fake"`, `PathPrefix() "/ee/fake/"`,
   `Handler()` returning a handler that writes `200 fake-ok`; register it in a `TestMain` or a
   dedicated test's own setup (not a package `init()`, so it never leaks into a production build);
   assert a request to `/ee/fake/ping` reaches it and a request to `/api/gateway/ee/status` before
   registration returns `{"extensions":[]}`.
8. Add these 8 capabilities to `docs/oss/boundary.yaml`, each with `placement: ee`, a
   `crippleware_test` block with all five answers `no`, and the `charter_ref`/`rationale` given
   below. `paths` is the `ee/<dir>/` glob each spec's own §4.1 will populate.

   | id | name | charter_ref | rationale |
   |---|---|---|---|
   | `tenancy-fleet-console` | Cross-tenant fleet provisioning and lifecycle automation | §2.2 | Operating many tenants' lifecycles from one console is running Gravix for other people, not a capability a single team needs. |
   | `billing-manual-invoicing` | Non-Stripe manual/PO invoicing and consolidated cross-tenant statements | §2.2 | Enterprise procurement workflows outside Stripe are a scale problem, not a correctness or security one. |
   | `identity-scim` | SCIM v2 user provisioning and directory sync | §2.3 | SCIM only matters with an enterprise IdP and headcount churn — a 10-person team using SSO does not miss it. |
   | `fleet-console` | Managing N separate Gravix installations from one console | §2.2 | Only an operator running many installations needs this; a single install has nothing to manage. |
   | `compliance-siem-evidence` | SIEM log streaming, proactive retention-hold archival, SOC2 evidence packs | §2.3 | The local audit log is already free and complete; streaming it to a SIEM and packaging auditor evidence only matters once a compliance team demands it. |
   | `intelligence-forecasting` | Seasonal forecasting and capacity projection | §2.3 | Additive analysis over the same free warehouse data; its absence makes no displayed number less accurate. |
   | `whitelabel-custom-domains` | Custom domains and signed embeddable dashboards | §2.3 | Reselling Gravix under another brand is a go-to-market choice, not a baseline need. |
   | `warehouse-continuous-sync` | Continuous sync to an external data warehouse | §2.3 | Operational convenience for a data team; the underlying Parquet is already freely exportable. |

9. Run `go build ./...`, `go test ./pkg/gatewaycore/... ./pkg/extpoint/...`, then `make build-ee`
   and confirm `bin/gateway-ee` starts and its `/api/gateway/ee/status` response is byte-identical
   to `bin/gateway`'s (both `{"extensions":[]}`) when no `ee/` feature package is imported.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `extpoint.Register` called with `ext == nil` | panic | `extpoint: Register called with a nil Extension` |
| `Extension.Name()` empty | panic | `extpoint: Extension.Name() must be non-empty` |
| `PathPrefix()` missing leading/trailing `/` | panic | `extpoint: Extension %q PathPrefix() = %q must start and end with "/"` |
| Duplicate `Name()` | panic | `extpoint: Extension name %q registered twice` |
| Duplicate `PathPrefix()` | panic | `extpoint: PathPrefix %q registered twice` |
| `GET /api/gateway/ee/status` with a non-GET method | `405` | `GET required` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `extpoint.Registered()` returns an empty, non-nil slice before any `Register` call | `TestRegisteredEmptyByDefault` |
| AC-2 | `extpoint.Register` panics on a `PathPrefix` without a trailing `/` | `TestRegisterRejectsBadPrefix` |
| AC-3 | `extpoint.Register` panics on a duplicate `Name` | `TestRegisterRejectsDuplicateName` |
| AC-4 | `extpoint.Register` panics on a duplicate `PathPrefix` | `TestRegisterRejectsDuplicatePrefix` |
| AC-5 | A request to a mounted extension's prefix reaches its `Handler` with the prefix stripped | `TestMountLoopStripsPrefix` |
| AC-6 | `GET /api/gateway/ee/status` returns `{"extensions":[]}` and `200` when the registry is empty | `TestEEStatusEmptyByDefault` |
| AC-7 | `GET /api/gateway/ee/status` lists a registered fake extension's name and prefix | `TestEEStatusListsRegistered` |
| AC-8 | `go build -o bin/gateway ./services/gateway/` succeeds and the binary's behaviour (route table, response bodies for `/live`, `/ready`) is unchanged from before this spec | `TestOSSGatewayBehaviourUnchanged` |
| AC-9 | `go build -o bin/gateway-ee ./ee/cmd/gateway/` succeeds with zero `ee/` sub-package imports | `TestEEGatewayBuildsWithNoExtensions` |
| AC-10 | Every capability id listed in §6 step 8 is present in `docs/oss/boundary.yaml` with `placement: ee` and all five `crippleware_test` answers `no` | `TestPhase13CapabilitiesRecorded` |
| AC-11 | `go test ./pkg/gatewaycore/...` passes with the exact same test count as `go test ./services/gateway/...` reported before this spec (proves the relocation changed zero test outcomes) | `TestRelocationPreservesTestSuite` |
| AC-12 | No file outside `ee/` contains the literal string `gravix-dashboards/ee/` | `TestNoCoreFileReferencesEE` |

## 8. Verification

```bash
# 1. Registry behaviour
go test ./pkg/extpoint/... -v -cover
# expect: PASS, coverage >= 95.0%

# 2. Mount loop and status endpoint
go test ./pkg/gatewaycore/... -run 'TestMountLoop|TestEEStatus' -v
# expect: PASS

# 3. Both binaries build
go build -o bin/gateway ./services/gateway/ && go build -o bin/gateway-ee ./ee/cmd/gateway/
# expect: both succeed

# 4. Full gatewaycore suite unchanged in size
go test ./pkg/gatewaycore/... -v 2>&1 | tail -5
# expect: same PASS count as the pre-move services/gateway suite, recorded in the report

# 5. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `pkg/extpoint` coverage ≥95%
- [ ] `git diff --stat` shows every `services/gateway/*.go` file as deleted and every
      `pkg/gatewaycore/*.go` file as added, with a near-zero line-level diff per file pair (package
      declaration and, in `main.go`, the `Run` rename plus §5.2 insertion only)
- [ ] `docker-compose.yml`, every `Dockerfile`, and `deploy/gravix/**` show zero diff
- [ ] `docs/oss/boundary.yaml` contains all 8 new `ee` entries from §6 step 8
- [ ] `docs-engineer` delta merged (architecture doc gains a one-paragraph description of
      `pkg/extpoint` and the two-binary build), or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| A file under `services/gateway/` not listed in §4.1/§4.2 | Return `SPEC DEFECT: §4 — <path> unaccounted for`. |
| A test in `main_test.go`/`phase6_test.go` that fails only because of the package rename (not a real bug) | Fix the test's package-qualified references, do not change its assertions; note it in the report. |
