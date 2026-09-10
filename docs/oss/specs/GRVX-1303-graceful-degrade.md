# SPEC GRVX-1303: Licence expiry degrades `ee/` to read-only and leaves the core untouched

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1303 | **Phase** | 13 | **Goal** | G7.5, G7.6 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.5 — on expiry, `ee/` features enter read-only degrade; existing configuration stays readable and exportable; **the core is entirely unaffected**. §7.3 Q1..Q5 all NO → ee, because expiry behaviour only exists where a licence exists. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1301, GRVX-1302 |
| **Blocks** | GRVX-1304 … GRVX-1311 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make licence expiry a non-event for everything that matters. When a Gravix Pro licence lapses,
ingestion, rollup, alerting and dashboards keep working exactly as they did; `ee/` configuration
stays readable and exportable; and `ee/` **writes** are refused with a message that says what
happened and what to do.

This is the spec that decides whether a customer whose card expires on a Friday has an incident.

## 2. Context the implementer needs

- `GRVX-1301` provides offline Ed25519 licence verification in the **core**, which always reports "no licence" in an OSS build and makes no network call, ever.
- `GRVX-1302` provides the core extension-point registry that `ee/` registers against.
- Charter §7.5: a tampered or absent licence "is not an error condition. It is the normal state of the OSS build, and must produce no warning noise."
- Charter §7.4 forbids nag screens and upgrade interstitials in the OSS dashboard.
- G7.5 requires licence expiry to have **no** impact on core function; G7.6 requires zero network calls for core operation.
- `pkg/export` (GRVX-1107) is core and free, so exporting `ee/` configuration during degrade uses an existing free mechanism.

## 3. Non-goals for this spec

- Do NOT stop, throttle, degrade or delay ingestion, rollup, alerting, dashboards or export on expiry. Any of those would make an expired licence a production incident, which is exactly what §7.5 exists to prevent.
- Do NOT delete `ee/` configuration on expiry. It stays readable so a customer can leave with it.
- Do NOT phone home to check a licence, on expiry or at any other time.
- Do NOT show an upgrade interstitial or nag banner. A single, factual, dismissible notice in the `ee/`-specific admin view only.
- Do NOT edit any core file. This is an `ee/` spec.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/degrade/degrade.go` | Degrade state machine |
| `ee/degrade/degrade_test.go` | Tests |
| `ee/degrade/guard.go` | Write guard applied to every `ee/` mutation |
| `ee/degrade/guard_test.go` | Tests |
| `ee/degrade/README.md` | What degrade does and does not touch |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | Every change is under `ee/`. Registration happens through GRVX-1302's extension points. |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Every path outside `ee/` | `pro-engineer` cannot edit core. If a hook is missing, return `EXTENSION POINT REQUIRED`. |
| `pkg/license/**` (GRVX-1301) | Verification is core and settled |

## 5. Interface contract

### 5.1 `ee/degrade/degrade.go`

```go
// Package degrade implements Gravix Enterprise's behaviour when a licence is
// absent, expired, or invalid. Its central guarantee is negative: nothing in
// this package can affect ingestion, rollup, alerting, dashboards, or export.
package degrade

// State is the licence-driven capability level.
type State string

const (
    // StateLicensed: a valid, unexpired licence. Full ee/ function.
    StateLicensed State = "licensed"
    // StateGrace: expired within the grace window. Full ee/ function, notice shown.
    StateGrace State = "grace"
    // StateReadOnly: expired past grace. ee/ reads and exports work; ee/ writes refused.
    StateReadOnly State = "read_only"
    // StateAbsent: no licence at all. The normal OSS state. Silent; no notice, no log.
    StateAbsent State = "absent"
)

// GracePeriod is how long after expiry ee/ keeps working fully. Fourteen days
// covers a lapsed card, a purchasing cycle, and a holiday, which are the actual
// reasons licences expire.
const GracePeriod = 14 * 24 * time.Hour

// Evaluate returns the current state from a verification result.
func Evaluate(v license.Result, now time.Time) State

// Notice returns the message to display for a state, or "" when nothing should
// be shown. StateAbsent always returns "" — the OSS build must be silent.
func Notice(s State, expiresAt time.Time) string
```

### 5.2 What each state permits

| Capability | Licensed | Grace | ReadOnly | Absent |
|---|---|---|---|---|
| Ingestion, rollup, alerting, dashboards, export (**core**) | ✅ | ✅ | ✅ | ✅ |
| Read `ee/` configuration | ✅ | ✅ | ✅ | n/a |
| Export `ee/` configuration | ✅ | ✅ | ✅ | n/a |
| Create or modify `ee/` configuration | ✅ | ✅ | ❌ | n/a |
| `ee/` background jobs already scheduled | ✅ | ✅ | ✅ | n/a |
| Schedule new `ee/` background jobs | ✅ | ✅ | ❌ | n/a |
| Notice displayed | none | factual, dismissible | factual, dismissible | **none** |

The core row is entirely unaffected across every state. That row is the spec.

Existing scheduled `ee/` jobs continue in `StateReadOnly` deliberately: stopping a customer's
warehouse sync mid-month because a card expired would destroy data continuity to make a billing
point.

### 5.3 The write guard

```go
// Guard wraps an ee/ mutation. Every write path in ee/ passes through it.
func Guard(ctx context.Context, s State, op string, fn func() error) error

// ErrReadOnly is returned when a write is attempted in StateReadOnly.
var ErrReadOnly = errors.New("gravix enterprise: licence expired; configuration is readable and exportable, but cannot be changed")
```

The error text names all three facts a user needs: what happened, what still works, and what does
not. HTTP surfaces return **`402 Payment Required`** with:

```json
{
  "error": "licence_expired",
  "message": "Your Gravix Enterprise licence expired on 2026-11-04. Existing configuration is readable and exportable. Renew to make changes.",
  "expired_at": "2026-11-04T00:00:00Z",
  "core_unaffected": true,
  "export_endpoint": "/api/gateway/exports"
}
```

`core_unaffected: true` is in the payload so an operator triaging an alert at 3am can see
immediately that this is not a production incident.

### 5.4 Silence in the OSS build

In `StateAbsent`: `Notice` returns `""`; nothing is logged at any level; no metric is emitted; no
HTTP header is added; no UI element appears. An OSS user must not be able to tell that `ee/` code
paths exist at all. Charter §7.4, and the ordinary state of the overwhelming majority of installs.

### 5.5 The negative test

`TestCoreUnaffectedInEveryState` must, for each of the four states: ingest facts, run a rollup,
evaluate an alert rule, render a dashboard query, and run an export — asserting identical results
across all four. It is the only test that actually proves §7.5, and it is the reason this spec is
listed as blocking every other `ee/` spec.

## 6. Behaviour

1. Implement `Evaluate` with the grace window.
2. Implement `Guard` and apply it to every `ee/` mutation path.
3. Implement `Notice`, returning `""` for `StateAbsent`.
4. Implement the 402 response shape.
5. Let already-scheduled `ee/` jobs continue in `StateReadOnly`; refuse only new scheduling.
6. Write `TestCoreUnaffectedInEveryState`.
7. Verify `make build-oss && make test-oss` pass with `ee/` deleted.
8. Verify `git diff --name-only` shows no path outside `ee/`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `ee/` write in `StateReadOnly` | `ErrReadOnly`, HTTP 402 | the §5.3 payload |
| New `ee/` job scheduled in `StateReadOnly` | refused, 402 | same |
| Any core operation in any state | proceeds unchanged | — |
| `StateAbsent` | silent | no output of any kind |
| Licence clock skew beyond 24h | treat as valid, log once in `ee/` only | `licence expiry is <d> in the past relative to system clock; check NTP` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Core behaviour is identical in all four states | `TestCoreUnaffectedInEveryState` |
| AC-2 | `ee/` writes are refused in `StateReadOnly` | `TestReadOnlyRefusesWrites` |
| AC-3 | `ee/` configuration stays readable in `StateReadOnly` | `TestReadOnlyPermitsReads` |
| AC-4 | `ee/` configuration stays exportable in `StateReadOnly` | `TestReadOnlyPermitsExport` |
| AC-5 | Already-scheduled `ee/` jobs continue in `StateReadOnly` | `TestScheduledJobsContinue` |
| AC-6 | The grace window is 14 days and full function persists through it | `TestGracePeriodFullFunction` |
| AC-7 | `StateAbsent` produces no notice, log, metric or header | `TestAbsentStateIsSilent` |
| AC-8 | The 402 payload carries `core_unaffected: true` | `TestExpiredResponseSaysCoreUnaffected` |
| AC-9 | No network call occurs in any state | `TestNoNetworkInAnyState` |
| AC-10 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-11 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |
| AC-12 | No upgrade interstitial or nag element is introduced | `TestNoNagUI` |

## 8. Verification

```bash
# 1. The guarantee this spec exists for
go test ./ee/degrade/... -run TestCoreUnaffectedInEveryState -v
# expect: PASS

# 2. Read-only means readable and exportable
go test ./ee/degrade/... -run 'TestReadOnlyPermitsReads|TestReadOnlyPermitsExport|TestScheduledJobsContinue' -v
# expect: PASS

# 3. The OSS build is silent
go test ./ee/degrade/... -run TestAbsentStateIsSilent -v
# expect: PASS

# 4. No network, ever
go test ./ee/degrade/... -run TestNoNetworkInAnyState -v
# expect: PASS

# 5. Charter §7.1 — the core does not need ee/
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss
# expect: 0; both succeed

# 6. No nag UI
grep -rniE "upgrade now|go pro|unlock|start free trial" ee/ dashboards/ | grep -c . || true
# expect: 0

# 7. Boundary
make check-boundary
# expect: boundary: 0 violations
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Zero files outside `ee/` modified
- [ ] `make build-oss && make test-oss` green with `ee/` deleted
- [ ] Expiry demonstrated against a live instance with ingestion running throughout
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Degrade requires a core change | Return `EXTENSION POINT REQUIRED` with the proposed interface. Do not edit core. |
| Any core capability affected by expiry | STOP. `CHARTER VIOLATION: §7.5 — <capability>`. This is the one thing this spec must not allow. |
| Pressure to stop scheduled jobs on expiry | Refuse, citing §5.2. Destroying data continuity to make a billing point is how a vendor loses a customer permanently. |
