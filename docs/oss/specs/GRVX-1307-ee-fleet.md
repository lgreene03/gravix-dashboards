# SPEC GRVX-1307: `ee/fleet/` — manage many Gravix installations from one console

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1307 | **Phase** | 13 | **Goal** | G7.7 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.3 Q1..Q5 all NO. A team with one Gravix install has nothing to fleet-manage; it affects no number, is not a security control, was never shipped open, and solves the genuine scale problem of many installs. → ee, per §2.2. |
| **Implementer role** | `pro-engineer` |
| **Depends on** | GRVX-1302, GRVX-1303 |
| **Blocks** | none |
| **Effort** | 8 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Let an operator running many Gravix installations see their health, push configuration, and
orchestrate upgrades from one place — while every managed install remains **fully functional and
independently operable** if the fleet console disappears.

## 2. Context the implementer needs

- `deploy/gravix/` is the Helm chart; `values-*.yaml` overlays exist per environment.
- `docs/oss/12-goal-tree.md` G8.1 requires Gravix Cloud to run the same OSS core; fleet is how many such installs are operated.
- `GRVX-1303` provides degrade; a fleet console losing its licence must not orphan managed installs.
- `GRVX-709` provides signed releases and SBOMs, which upgrade orchestration must verify.
- Charter §7.4 forbids requiring a network call for core operation — so a managed install must never depend on reaching the console to start, run, or recover.

## 3. Non-goals for this spec

- Do NOT make a managed install depend on the console. Each install must start, ingest, roll up, alert and serve dashboards with the console unreachable, indefinitely.
- Do NOT push configuration that a local operator cannot override. The install's operator is the final authority on their own system.
- Do NOT collect data from managed installs beyond health and version. Charter §7.4 governs telemetry regardless of who is running the install.
- Do NOT edit any core file.
- Do NOT auto-upgrade without explicit per-install approval.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/fleet/fleet.go` | Fleet registry and health |
| `ee/fleet/fleet_test.go` | Tests |
| `ee/fleet/config.go` | Configuration push and local override |
| `ee/fleet/config_test.go` | Tests |
| `ee/fleet/upgrade.go` | Upgrade orchestration |
| `ee/fleet/upgrade_test.go` | Tests |
| `ee/fleet/agent.go` | The reporting component that runs beside a managed install |
| `ee/fleet/register.go` | Extension-point registration |
| `ee/fleet/README.md` | What the console can and cannot do to an install |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| Every path outside `ee/` | `pro-engineer` cannot edit core |
| `deploy/gravix/**` | The chart stays usable without any fleet console |

## 5. Interface contract

### 5.1 The independence invariant

Stated in `ee/fleet/README.md`, verbatim:

```
A managed install does not need the console.

Every Gravix installation the fleet console manages runs, ingests, aggregates,
alerts and serves dashboards with the console unreachable — permanently, not for
a grace period. The console reports health it is told about and offers changes an
install may accept. It cannot compel, and it is never in the path of anything
that matters.

If the console is down, nothing observable happens to your monitoring.
```

This is not modesty. A fleet console that can take down monitoring is a worse liability than the
problem it solves, and an observability vendor whose management plane causes outages does not get a
second chance.

### 5.2 The agent, and why it is not an agent

`docs/04-non-goals.md` §3 forbids agents: "We WILL NOT build, distribute, or support sidecar agents
or host-level daemons… We WILL NOT collect system metrics (CPU, Memory, Disk) from hosts."

`ee/fleet/agent.go` is compatible with that non-goal only under these constraints, which are
mandatory and testable:

| Constraint | Rule |
|---|---|
| Scope | Reports **only** the Gravix process's own version, health-check result, and config hash |
| Host metrics | **Collects none.** No CPU, memory, disk, network, process list, or anything about the host |
| Direction | Outbound only; the console never connects in |
| Optionality | Absent by default; the install works identically without it |
| Kill switch | A local operator disables it with one config flag and no console involvement |

It is a **self-report from one process about itself**, not a host daemon. If a reviewer judges it has
drifted toward being an agent, the correct response is to cut scope, not to argue definitions — and
§10 says so.

### 5.3 Configuration push, with local override

```go
// Package fleet manages many Gravix installations. It reports and proposes; it
// never compels. A managed install is fully operable with the console absent.
package fleet

// Proposal is a configuration change offered to an install.
type Proposal struct {
    ID        string    `json:"id"`
    InstallID string    `json:"install_id"`
    Changes   map[string]any `json:"changes"`
    Reason    string    `json:"reason"`
    ProposedAt time.Time `json:"proposed_at"`
}

// Disposition is what an install did with a proposal.
type Disposition string

const (
    DispositionPending  Disposition = "pending"
    DispositionAccepted Disposition = "accepted"
    DispositionRejected Disposition = "rejected"   // by a local operator
    DispositionExpired  Disposition = "expired"    // never acted on
)

// LocalOverride records that an install's operator has pinned a setting.
// A pinned setting is never changed by a proposal, and the console shows it as
// pinned rather than as drift.
type LocalOverride struct {
    Key    string
    Reason string
    SetBy  string
    SetAt  time.Time
}
```

An install that rejects a proposal is not "non-compliant" and must not be rendered as an error state.
It is an install whose operator made a decision, and the console's job is to show that decision, not
to nag about it.

### 5.4 Upgrade orchestration

Per install, in order: verify the release signature and SBOM (GRVX-709); require explicit approval;
back up per `docs/disaster-recovery.md`; upgrade; run the install's health check; on failure, roll
back automatically and report. **No fleet-wide simultaneous upgrade** — a staged rollout with a
configurable batch size, defaulting to one, because upgrading an entire fleet at once converts a bad
release into a total outage.

## 6. Behaviour

1. Implement the registry, health reporting, and the agent under the §5.2 constraints.
2. Implement proposals with local override and non-nagging disposition rendering.
3. Implement staged upgrade with signature verification, backup, health check and auto-rollback.
4. Apply `degrade.Guard` to console mutations; managed installs are unaffected by console licence state.
5. Verify a managed install runs indefinitely with the console unreachable.
6. Verify the agent collects nothing about the host.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Console unreachable | install continues; agent queues locally and retries | `fleet: console unreachable; local operation unaffected` |
| Proposal touches a pinned key | refused, shown as pinned | `fleet: "<key>" is pinned locally by <who>: <reason>` |
| Release signature invalid | refuse the upgrade | `fleet: release signature verification failed; upgrade refused` |
| Health check fails after upgrade | auto-rollback, report | `fleet: health check failed after upgrade; rolled back to <version>` |
| Console licence expired | console read-only; installs unaffected | the GRVX-1303 payload |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A managed install runs indefinitely with the console unreachable | `TestInstallIndependentOfConsole` |
| AC-2 | The agent collects no host metric of any kind | `TestAgentCollectsNoHostMetrics` |
| AC-3 | The agent is absent by default and disableable locally | `TestAgentOptionalAndDisableable` |
| AC-4 | The console never connects inbound to an install | `TestNoInboundConnections` |
| AC-5 | A pinned setting is never changed by a proposal | `TestLocalOverrideRespected` |
| AC-6 | A rejected proposal is not rendered as an error | `TestRejectedProposalIsNotAnError` |
| AC-7 | An unsigned release is refused | `TestUnsignedReleaseRefused` |
| AC-8 | A failed health check triggers automatic rollback | `TestFailedUpgradeRollsBack` |
| AC-9 | Upgrades are staged, defaulting to batch size 1 | `TestUpgradeIsStaged` |
| AC-10 | Console licence expiry does not affect managed installs | `TestConsoleExpiryDoesNotAffectInstalls` |
| AC-11 | The independence statement appears verbatim | `TestIndependenceStatementVerbatim` |
| AC-12 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-13 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Independence — the invariant
go test ./ee/fleet/... -run 'TestInstallIndependentOfConsole|TestConsoleExpiryDoesNotAffectInstalls' -v
# expect: PASS

# 2. Non-goal §3 — this is not an agent
go test ./ee/fleet/... -run 'TestAgentCollectsNoHostMetrics|TestAgentOptionalAndDisableable|TestNoInboundConnections' -v
grep -rniE "cpu_percent|mem_total|disk_usage|/proc/|host_metrics" ee/fleet/ | grep -c . || true
# expect: PASS; 0

# 3. The operator is the authority
go test ./ee/fleet/... -run 'TestLocalOverrideRespected|TestRejectedProposalIsNotAnError' -v
# expect: PASS

# 4. Upgrades are safe
go test ./ee/fleet/... -run 'TestUnsignedReleaseRefused|TestFailedUpgradeRollsBack|TestUpgradeIsStaged' -v
# expect: PASS

# 5. The statement
grep -c "If the console is down, nothing observable happens to your monitoring." ee/fleet/README.md
# expect: 1

# 6. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All thirteen acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] A managed install demonstrated running with the console stopped
- [ ] The agent demonstrated collecting nothing about the host
- [ ] Zero files outside `ee/` modified
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| The agent drifting toward host-level collection | Cut scope. Do not argue the definition. Route to `cpo` citing non-goal §3; if it needs host metrics, it is not this feature. |
| A design where an install depends on the console | Return `SPEC DEFECT: §5.1`. A management plane that can cause an outage is worse than the problem it solves. |
| Pressure to auto-upgrade a fleet | Refuse, citing §5.4. One bad release becoming a total outage is the failure this staging exists to prevent. |
