# SPEC GRVX-703: Create `boundary.yaml`, the machine-readable core/`ee` placement map

| Field | Value |
|---|---|
| **Spec ID** | GRVX-703 |
| **Phase** | 7 |
| **Goal** | G1.3 |
| **Placement** | boundary |
| **Charter basis** | §2 — the authoritative machine-readable boundary. If it and the charter prose disagree, this file wins and CI fails. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701, GRVX-702 |
| **Blocks** | GRVX-704, GRVX-710 |
| **Effort** | 2 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Create `docs/oss/boundary.yaml` — a machine-readable record of every gateable capability, its
placement (`core` or `ee`), the five Crippleware Test answers that decided it, and the code paths
that implement it. Add a Go validator that parses it and cross-checks it against the actual
`requirePlan` calls in the codebase.

## 2. Context the implementer needs

- `services/gateway/main.go:941-970` defines `planRank` (`free`:0, `starter`:1, `pro`:2) and
  `requirePlan(minPlan string)`, middleware returning HTTP 403 with
  `upgrade required: %s plan needed (current: %s)` when the tenant plan rank is below the minimum.
- `services/gateway/gateway_platform.go:128` gates the public metrics API with
  `if planRank[info.Plan] < planRank["pro"]`.
- Other plan-gated handlers are registered in `services/gateway/main.go`, `gateway_dashboards.go`, `gateway_alerts.go`, `gateway_billing.go`, `enterprise.go`.
- The Crippleware Test is charter §7.3, five questions, all must be NO for `ee` placement.
- `ee/` exists and contains only `ee/placeholder/` (GRVX-702).
- The repo has no YAML parsing dependency in `go.mod` today. `gopkg.in/yaml.v3` must be added.

## 3. Non-goals for this spec

- Do NOT move any feature between core and `ee/`. This spec **records** placement; GRVX-710 changes it.
- Do NOT remove or edit any `requirePlan` call.
- Do NOT add the `make` targets or the CI job. That is GRVX-704.
- Do NOT decide contested placements yourself. Every ruling in this file comes from charter §2.1,
  §2.2 or §2.3, and the spec states which. A capability not covered by the charter is a `SPEC DEFECT`.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `docs/oss/boundary.yaml` | The placement map |
| `pkg/boundary/boundary.go` | Parser and validator |
| `pkg/boundary/boundary_test.go` | Tests |
| `pkg/boundary/testdata/valid.yaml` | Minimal valid fixture |
| `pkg/boundary/testdata/invalid_crippleware.yaml` | Fixture: `ee` placement with a YES answer |
| `pkg/boundary/testdata/invalid_missing_answer.yaml` | Fixture: fewer than 5 answers |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `go.mod` | Add `gopkg.in/yaml.v3 v3.0.1` |
| `go.sum` | Updated by `go mod tidy` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/**` | This spec observes the gateway; it does not change it |
| `ee/**` | No `ee/` code exists to record beyond the placeholder |

## 5. Interface contract

### 5.1 `boundary.yaml` schema

```yaml
version: 1
capabilities:
  - id: string                 # required, kebab-case, unique
    name: string               # required, human-readable
    placement: core | ee       # required
    charter_ref: string        # required, e.g. "§2.1" or "§2.3"
    rationale: string          # required, one sentence
    crippleware_test:          # required when placement == ee; forbidden when placement == core
      q1_team_of_ten_notices: yes | no
      q2_affects_accuracy: yes | no
      q3_worse_security: yes | no
      q4_previously_open: yes | no
      q5_only_reason_is_money: yes | no
    paths: [string]            # required, >=1, repo-relative globs
    gate: string               # optional; the requirePlan minimum, e.g. "pro". Absent means ungated.
```

### 5.2 `pkg/boundary/boundary.go`

```go
// Package boundary parses and validates the open-core placement map in
// docs/oss/boundary.yaml, and cross-checks it against plan gates in the codebase.
package boundary

// Placement is where a capability's code may live.
type Placement string

const (
    PlacementCore Placement = "core"
    PlacementEE   Placement = "ee"
)

// CrippleWareTest records the five charter §7.3 answers.
type CrippleWareTest struct {
    Q1TeamOfTenNotices  bool `yaml:"q1_team_of_ten_notices"`
    Q2AffectsAccuracy   bool `yaml:"q2_affects_accuracy"`
    Q3WorseSecurity     bool `yaml:"q3_worse_security"`
    Q4PreviouslyOpen    bool `yaml:"q4_previously_open"`
    Q5OnlyReasonIsMoney bool `yaml:"q5_only_reason_is_money"`
}

// AnyYes reports whether any answer is yes, which forces core placement.
func (c CrippleWareTest) AnyYes() bool

// Capability is one gateable unit of functionality.
type Capability struct {
    ID              string           `yaml:"id"`
    Name            string           `yaml:"name"`
    Placement       Placement        `yaml:"placement"`
    CharterRef      string           `yaml:"charter_ref"`
    Rationale       string           `yaml:"rationale"`
    CrippleWareTest *CrippleWareTest `yaml:"crippleware_test"`
    Paths           []string         `yaml:"paths"`
    Gate            string           `yaml:"gate"`
}

// Map is the whole boundary file.
type Map struct {
    Version      int          `yaml:"version"`
    Capabilities []Capability `yaml:"capabilities"`
}

// Load reads and validates a boundary map from path.
func Load(path string) (*Map, error)

// Validate returns every rule violation found, or nil when the map is valid.
func (m *Map) Validate() []error

var (
    ErrUnsupportedVersion = errors.New("boundary: unsupported version")
    ErrDuplicateID        = errors.New("boundary: duplicate capability id")
    ErrMissingField       = errors.New("boundary: missing required field")
    ErrCrippleWareYes     = errors.New("boundary: ee placement with a yes answer forces core placement")
    ErrTestOnCore         = errors.New("boundary: crippleware_test present on a core capability")
    ErrTestMissingOnEE    = errors.New("boundary: crippleware_test required for ee placement")
)
```

### 5.3 Validation rules (each produces one error in `Validate()`)

1. `version` must equal `1`, else `ErrUnsupportedVersion`.
2. Every `id` must be unique, else `ErrDuplicateID` naming the id.
3. `id`, `name`, `placement`, `charter_ref`, `rationale` must be non-empty, and `paths` must have
   at least one entry, else `ErrMissingField` naming the id and field.
4. `placement` must be exactly `core` or `ee`, else `ErrMissingField` naming the id.
5. If `placement == ee` and `crippleware_test` is absent → `ErrTestMissingOnEE`.
6. If `placement == core` and `crippleware_test` is present → `ErrTestOnCore`.
7. If `placement == ee` and `CrippleWareTest.AnyYes()` → `ErrCrippleWareYes` naming the id and which
   questions answered yes.

### 5.4 Required initial content of `boundary.yaml`

Exactly these **23** capabilities (20 core, 3 `ee`). Placements are taken from charter
§2.1/§2.2/§2.3 and are **not** the implementer's judgement.

*Expanded from 18 by SD-002* (`../spec-defects.md`): the original list omitted `schema-validation`,
`compaction-retention`, `storage-abstraction`, `terraform-provider` and `deploy-tooling`, all core.
A map that omits real capabilities cannot guarantee a gate would appear in it.

**Core** (no `crippleware_test` block): `ingestion-http`, `ingestion-otlp`, `schema-validation`,
`rollup-engine`, `compaction-retention`, `storage-abstraction`, `query-duckdb-trino`, `dashboard`,
`alerting-threshold`, `alerting-anomaly`, `sdks`, `cli`, `rbac-single-org`, `audit-log-local`,
`rate-limiting`, `public-metrics-api`, `custom-dashboards`, `data-export`, `terraform-provider`,
`deploy-tooling`.

**ee** (each with all five answers `no`): `multi-tenancy`, `billing`, `tenant-branding`.

For the three `ee` entries use these `charter_ref` values: `multi-tenancy` → `§2.2`,
`billing` → `§2.2`, `tenant-branding` → `§2.3`.

For `public-metrics-api`, `custom-dashboards`, `data-export`, `rate-limiting`, `audit-log-local`:
set `charter_ref: "§2.3"`. Add a `gate:` field **only where a plan gate actually exists in the
code**, so GRVX-710 has an accurate record of what must change.

**Corrected by SD-001** (`../spec-defects.md`). An earlier draft of this spec asserted all five were
plan-gated. Exhaustive grep over non-test source shows otherwise:

| Capability | `gate:` field | Reality in code |
|---|---|---|
| `public-metrics-api` | `gate: pro` | `services/gateway/gateway_platform.go:132` returns 402 via a direct `planRank` comparison |
| `custom-dashboards` | *(omit)* | No plan gate. Role-gated only. |
| `data-export` | *(omit)* | No plan gate. Admin-only for mutations. |
| `audit-log-local` | *(omit)* | No plan gate. Admin-only by role. |
| `rate-limiting` | *(omit)* | Varies the limit by plan and returns 429 to everyone. A differentiated limit is not a gate. |

Additionally, `requirePlan` at `services/gateway/main.go:953` has **zero non-test callers** and is
dead code. Record it in the implementation report; GRVX-710 disposes of it.

The mismatch between `placement: core` and a non-empty `gate:` on `public-metrics-api` is the
intended signal that GRVX-710 has work to do.

## 6. Behaviour

1. `go get gopkg.in/yaml.v3@v3.0.1` and `go mod tidy`.
2. Implement `pkg/boundary/boundary.go` per §5.2 and §5.3.
3. Write the three testdata fixtures.
4. Write `docs/oss/boundary.yaml` with the 18 capabilities from §5.4. For every capability, `paths`
   lists the real repo-relative globs implementing it — determined by reading the gateway handler
   registrations. Record each glob you chose in the implementation report.
5. `Load` reads the file, unmarshals it, calls `Validate`, and returns the first error joined with
   `errors.Join` if any rule failed.
6. Write `boundary_test.go` covering every rule in §5.3 and the real `docs/oss/boundary.yaml`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| File missing | `Load` returns error wrapping `os.ErrNotExist` | `boundary: open <path>: no such file or directory` |
| Malformed YAML | `Load` returns the yaml error wrapped | `boundary: parse <path>: <yaml error>` |
| Any validation rule fails | `Load` returns all errors joined | one line per violation, each naming the capability id |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `docs/oss/boundary.yaml` loads and validates with zero errors | `TestRealBoundaryMapIsValid` |
| AC-2 | It contains exactly 23 capabilities (20 core, 3 ee) | `TestBoundaryMapHasExpectedCapabilityCount` |
| AC-3 | Every `ee` capability has all five answers and all are `no` | `TestEECapabilitiesPassCrippleware` |
| AC-4 | An `ee` capability with any `yes` answer is rejected | `TestCrippleWareYesRejected` |
| AC-5 | An `ee` capability missing the test block is rejected | `TestMissingCrippleWareTestRejected` |
| AC-6 | A `core` capability carrying a test block is rejected | `TestCrippleWareTestOnCoreRejected` |
| AC-7 | Duplicate ids are rejected | `TestDuplicateIDRejected` |
| AC-8 | `version: 2` is rejected | `TestUnsupportedVersionRejected` |
| AC-9 | Every path glob in the map matches at least one existing file | `TestAllBoundaryPathsExist` |
| AC-10 | `pkg/boundary` line coverage is ≥95% | `TestCoverage` (verified by the §8 command) |

## 8. Verification

```bash
# 1. Package tests and coverage
go test ./pkg/boundary/... -v -cover
# expect: PASS, coverage >= 95.0%

# 2. The real map is valid
go test ./pkg/boundary/... -run TestRealBoundaryMapIsValid -v
# expect: PASS

# 3. Every glob resolves
go test ./pkg/boundary/... -run TestAllBoundaryPathsExist -v
# expect: PASS

# 4. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 5. Open-core integrity
grep -rn "gravix-dashboards/ee" --include=*.go pkg/ services/ | grep -c . || true
# expect: 0
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] The path globs chosen for each capability are listed in the report
- [ ] `pkg/boundary` coverage ≥95%
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (charter §2 links to `boundary.yaml`)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A plan-gated capability not in §5.4's list of 18 | Return `SPEC DEFECT: §5.4 — <capability> gated at <path>:<line>, no charter ruling` |
| A charter §2.3 ruling that contradicts §5.4 | Return `SPEC DEFECT: §5.4 — conflicts with charter §2.3 row <name>` |
| No code path implementing a listed capability | Return `SPEC DEFECT: §5.4 — <id> has no implementation` |
