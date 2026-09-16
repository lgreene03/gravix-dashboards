# SPEC GRVX-1201: Plugin system v2 — a stable, versioned ABI for notifiers, exporters and adapters

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1201 | **Phase** | 12 | **Goal** | G6.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q1 = YES → core. An extensibility mechanism only paying users can build against produces no ecosystem. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-704 |
| **Blocks** | GRVX-1202, GRVX-1311 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Let a third party add a notifier, an exporter, or an ingestion adapter without forking Gravix, and
without their plugin breaking on the next release. A stable, versioned ABI is the difference between
an ecosystem and a pile of abandoned forks.

## 2. Context the implementer needs

- `docs/plugin-system.md` (5356 bytes) describes a design from Horizon 1. Read it in full and record which parts this spec keeps, changes, or supersedes.
- `pkg/notify/` has Slack, webhook, PagerDuty and OpsGenie senders, each with its own concrete type. These become the reference implementations of the notifier interface.
- `pkg/export/` (GRVX-1107) writes Parquet, CSV and JSONL. These become reference exporters.
- `services/ingestion/main.go` and `otlp.go` are the two ingestion paths; GRVX-1102 adds remote-write.
- Go plugins via `plugin.Open` are ELF-only, require identical toolchain and dependency versions, and cannot be unloaded. They are a poor fit for a tool that ships as a static binary and a container image.
- Charter §7.2: `ee/` registers against core extension points (GRVX-1302). This spec and GRVX-1302 must not build two competing registries.

## 3. Non-goals for this spec

- Do NOT use Go's `plugin` package. Its toolchain-version coupling would make every plugin break on every Gravix release, which is the opposite of a stable ABI.
- Do NOT allow a plugin to reach into Gravix internals. Plugins see the interface and nothing else.
- Do NOT let a plugin failure take down ingestion, rollup, or the dashboard.
- Do NOT build a second registry alongside GRVX-1302's. This spec defines the **plugin** kinds; GRVX-1302 defines the general extension-point mechanism, and this one uses it.
- Do NOT ship a plugin marketplace or registry service. That is GRVX-1202.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/plugin/plugin.go` | ABI version, kinds, lifecycle |
| `pkg/plugin/plugin_test.go` | Tests |
| `pkg/plugin/notifier.go` | Notifier interface |
| `pkg/plugin/exporter.go` | Exporter interface |
| `pkg/plugin/adapter.go` | Ingestion adapter interface |
| `pkg/plugin/registry.go` | Registration and lookup |
| `pkg/plugin/registry_test.go` | Tests |
| `pkg/plugin/host.go` | Subprocess host and isolation |
| `pkg/plugin/host_test.go` | Tests |
| `docs-site/docs/writing-a-plugin.md` | Guide with a complete worked example |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `pkg/notify/slack.go`, `webhook.go`, `pagerduty.go`, `opsgenie.go` | Implement `plugin.Notifier`. Keep existing behaviour and every existing test passing. |
| `docs/plugin-system.md` | Update to describe v2; record what changed |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/main.go` hot path | A plugin must not sit between a request and its durable write |
| `pkg/recompute/**`, `pkg/sketch/**` | Correctness code takes no plugins |
| `ee/**` | GRVX-1302 handles `ee/` registration |

## 5. Interface contract

### 5.1 The transport decision, stated so it is not relitigated

Plugins run as **subprocesses speaking JSON-RPC 2.0 over stdio**, not as loaded shared objects.

| | Go `plugin.Open` | Subprocess + stdio JSON-RPC |
|---|---|---|
| Breaks on Gravix toolchain change | yes | no |
| Requires matching dependency versions | yes | no |
| Works on every platform | no, ELF only | yes |
| Crash isolation | none — takes the host down | full |
| Language | Go only | any |
| Cost | in-process call | one process, JSON per call |

The cost is real and accepted: a notifier sends a handful of messages per minute, and an exporter
runs on a schedule. Neither is latency-critical. **No plugin sits on the ingestion hot path** —
adapters are invoked at batch boundaries, never per request.

### 5.2 `pkg/plugin/plugin.go`

```go
// Package plugin defines Gravix's third-party extension ABI. Plugins run as
// subprocesses speaking JSON-RPC 2.0 over stdio, so a plugin built against one
// Gravix release keeps working against the next.
package plugin

// ABIVersion is the wire contract version. It changes only when an existing
// method's request or response shape changes; adding a method does not change it.
const ABIVersion = "1"

// Kind is what a plugin extends.
type Kind string

const (
    KindNotifier Kind = "notifier"
    KindExporter Kind = "exporter"
    KindAdapter  Kind = "adapter"
)

// Manifest is what a plugin declares at startup, in response to "gravix.describe".
type Manifest struct {
    Name        string   `json:"name"`         // kebab-case, unique
    Version     string   `json:"version"`      // semver
    ABIVersion  string   `json:"abi_version"`  // must equal ABIVersion
    Kind        Kind     `json:"kind"`
    Description string   `json:"description"`
    ConfigSchema map[string]ConfigField `json:"config_schema"`
    Homepage    string   `json:"homepage"`
    License     string   `json:"license"`
}

// ConfigField describes one configuration value the plugin accepts.
type ConfigField struct {
    Type        string `json:"type"`        // "string"|"int"|"bool"|"duration"|"secret"
    Required    bool   `json:"required"`
    Default     any    `json:"default"`
    Description string `json:"description"`
}

var (
    ErrABIMismatch     = errors.New("plugin: ABI version mismatch")
    ErrDuplicateName   = errors.New("plugin: a plugin with that name is already registered")
    ErrManifestInvalid = errors.New("plugin: manifest is invalid")
    ErrTimeout         = errors.New("plugin: call timed out")
    ErrCrashed         = errors.New("plugin: subprocess exited")
)
```

A `secret` config field is never logged, never included in an error message, and never written to a
`gravix doctor` bundle.

### 5.3 The three interfaces

```go
// Notifier delivers an alert to a destination.
type Notifier interface {
    // Notify delivers one alert. It must be idempotent on AlertID: Gravix may
    // retry, and a duplicate page is worse than a late one.
    Notify(ctx context.Context, alert Alert) error
}

// Exporter writes a batch of rows to an external destination.
type Exporter interface {
    // Export writes rows. It is called at batch boundaries, never per row.
    Export(ctx context.Context, batch Batch) (ExportResult, error)
}

// Adapter converts a foreign payload into Gravix facts.
type Adapter interface {
    // Convert turns a raw payload into validated facts. It must reject anything
    // that would violate the cardinality budget rather than passing it through:
    // a plugin is not an exemption from docs/04-non-goals.md §5.
    Convert(ctx context.Context, payload []byte, contentType string) ([]RequestFact, error)
}
```

### 5.4 Isolation and failure

| Property | Rule |
|---|---|
| Timeout | 30s default per call, configurable per plugin, hard-killed after |
| Crash | Restarted with exponential backoff to 5 min; the host continues |
| Failure budget | 5 consecutive failures disables the plugin and raises an alert; it never blocks the pipeline |
| Memory | Subprocess RSS limit, default 256 MB; exceeding it kills and restarts |
| Filesystem | Inherits the host's working directory; Gravix passes no credentials it was not configured to pass |
| Network | Unrestricted — a notifier must reach Slack. This is stated plainly in the plugin guide so operators know what installing a plugin means. |

The network rule is a genuine trust boundary and the guide must say so directly: installing a
third-party plugin is installing third-party code with network access, exactly like any other
dependency.

## 6. Behaviour

1. Read `docs/plugin-system.md` in full; record kept, changed and superseded parts.
2. Implement the ABI, the manifest handshake (`gravix.describe`), and the registry.
3. Implement the subprocess host with the §5.4 isolation rules.
4. Convert the four existing notifiers to implement `plugin.Notifier` **in-process** — they are first-party and need no subprocess. The interface is shared; the transport is not mandatory for built-ins.
5. Write the guide with a complete worked notifier example a reader can copy and run.
6. Verify no plugin can be invoked on the ingestion hot path.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| ABI mismatch | refuse to load | `plugin "<name>" declares ABI <a>, this Gravix supports <b>` |
| Duplicate name | refuse the second | `plugin "<name>" is already registered` |
| Call timeout | kill, restart, count a failure | `plugin "<name>" timed out after <d>` |
| 5 consecutive failures | disable, alert | `plugin "<name>" disabled after 5 consecutive failures` |
| Memory limit exceeded | kill, restart | `plugin "<name>" exceeded <n> MB and was restarted` |
| Adapter returns an over-cardinality fact | reject the fact, count it | `plugin "<name>" produced a fact exceeding the cardinality budget` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | A subprocess plugin loads, describes itself, and is called | `TestPluginLifecycle` |
| AC-2 | An ABI mismatch is refused, not attempted | `TestABIMismatchRefused` |
| AC-3 | A crashing plugin does not take down the host | `TestCrashIsolation` |
| AC-4 | A hanging plugin is killed at the timeout | `TestTimeoutKillsPlugin` |
| AC-5 | Five consecutive failures disable the plugin | `TestFailureBudgetDisables` |
| AC-6 | A disabled plugin never blocks alerting or export | `TestDisabledPluginDoesNotBlockPipeline` |
| AC-7 | An adapter cannot bypass the cardinality budget | `TestAdapterCannotExceedCardinality` |
| AC-8 | No plugin is invoked on the ingestion hot path | `TestNoPluginOnIngestHotPath` |
| AC-9 | `secret` config values never appear in logs or errors | `TestSecretConfigNeverLogged` |
| AC-10 | The four existing notifiers pass their original tests unchanged | `TestExistingNotifiersUnchanged` |
| AC-11 | The guide's worked example builds and runs | `TestPluginGuideExampleWorks` |
| AC-12 | Go's `plugin` package is not imported anywhere | `TestNoGoPluginPackage` |

## 8. Verification

```bash
# 1. Lifecycle and isolation
go test ./pkg/plugin/... -v -cover
# expect: PASS, coverage >= 90%

# 2. A bad plugin cannot hurt the host
go test ./pkg/plugin/... -run 'TestCrashIsolation|TestTimeoutKillsPlugin|TestDisabledPluginDoesNotBlockPipeline' -v
# expect: PASS

# 3. Plugins are not an exemption from the non-goals
go test ./pkg/plugin/... -run 'TestAdapterCannotExceedCardinality|TestNoPluginOnIngestHotPath' -v
# expect: PASS

# 4. No Go plugin package
grep -rn '"plugin"' --include=*.go pkg/ services/ | grep -v "gravix-dashboards/pkg/plugin" | grep -c . || true
# expect: 0

# 5. Existing notifiers untouched
go test ./pkg/notify/... -v
# expect: PASS

# 6. The guide works
go test ./pkg/plugin/... -run TestPluginGuideExampleWorks -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `docs/plugin-system.md` parts recorded as kept, changed or superseded
- [ ] All four existing notifier test suites pass unchanged
- [ ] The plugin guide states the network trust boundary plainly
- [ ] `docs-engineer` delta merged
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A plugin kind needing the ingestion hot path | Return `SPEC DEFECT: §3`. Route to `senior-engineering-lead`. A plugin between a request and its fsync would put third-party code inside the durability guarantee. |
| GRVX-1302's registry overlapping this one | Return `SPEC DEFECT: §3 — two registries`. One mechanism, two kinds of consumer. |
| Pressure to use Go's `plugin` package for speed | Refuse, citing §5.1. Every plugin breaking on every release costs more than JSON encoding ever will. |

---

## 11. Implementation report

All twelve acceptance criteria pass. `pkg/plugin` is at **91.5%** statement coverage, above the 90%
§8 requires.

```
ok  github.com/lgreene/gravix-dashboards/pkg/plugin   coverage: 91.5% of statements
ok  github.com/lgreene/gravix-dashboards/pkg/notify
staticcheck ./pkg/plugin/ ./pkg/notify/   (no findings)
boundary: 0 violations
build-oss exit=0
test-oss exit=0
```

`go test ./...` passes with no failures. Twenty-nine tests in `pkg/plugin`.

### 11.1 §9 — `docs/plugin-system.md`: kept, changed, superseded

v1 described in-process, compile-time Go interfaces registered at startup, across four extension
points. The rewritten document records the delta in full; the summary:

| v1 | v2 | Why |
|---|---|---|
| Build-time in-process loading | **Superseded** by subprocess JSON-RPC | Go's `plugin` package couples every plugin to the host's toolchain and dependency versions and cannot isolate a crash |
| Notification Channel plugins | **Kept** as `Notifier` | The one v1 point that was both safe and wanted |
| Transform plugins (`etl.Transform`) | **Removed** | Sits inside the derivation of metrics from facts; third-party code there makes "recomputable" unprovable |
| Storage Backend plugins (`ObjectStore`) | **Removed** | On the write path for every fact — the one place a third party's latency must never appear |
| Auth Provider plugins | **Removed** | A pluggable authenticator is a pluggable way to be wrong about who someone is |
| — | **New**: `Exporter`, `Adapter` | The common integration requests, both safely outside the correctness path |

Three extension points were removed. That is a deliberate narrowing: each sat either inside the
correctness path or on the ingestion hot path, and refusing to extend there is what lets the rest of
the ABI be stable. The document says so rather than letting them disappear quietly.

### 11.2 The test binary is the plugin

Crash isolation, timeouts and restart backoff cannot be tested honestly against a mocked transport —
a mock cannot hang or die. `TestMain` re-executes the test binary with `GRAVIX_PLUGIN_MODE` set, so
every test drives a **real subprocess over real pipes** speaking real JSON-RPC. The modes cover a
well-behaved plugin, an ABI mismatch, a malformed manifest, a crash on start, a crash mid-call, a
hang, a plugin that always returns an error, and one that answers with garbage.

One thing that had to change to make the hang real: `select {}` in the fake plugin triggers Go's
deadlock detector, which aborts the process — so the host saw EOF and the timeout never fired. A
long sleep models a hung plugin; the runtime does not intervene.

### 11.3 AC-11 — the guide's example is compiled and run, not eyeballed

`TestPluginGuideExampleWorks` extracts the first Go block from
`docs-site/docs/writing-a-plugin.md`, writes it into a temporary module, **compiles it**, launches
the binary as a plugin, and drives the handshake and a notify call. A guide whose example does not
work is worse than no guide, because a reader assumes their own code is at fault.

It clears `GOFLAGS` for the build: a parent `-mod=vendor` would fail in a throwaway module with no
vendor directory, which would have looked like the example being broken.

### 11.4 AC-8 is structural, not an inspection

`TestNoPluginOnIngestHotPath` runs `go list -deps` over `services/ingestion` and fails if
`pkg/plugin` appears anywhere in its transitive dependencies. Grepping for call sites would prove
only that today's code does not call a plugin; the dependency check proves it *cannot*, because the
package is not reachable from that binary at all.

### 11.5 §8 step 4's grep over-matches; AC-12 is the accurate check

The verification command

```bash
grep -rn '"plugin"' --include=*.go pkg/ services/ | grep -v "gravix-dashboards/pkg/plugin" | grep -c .
```

returns **3**, not 0. All three are false positives: one is the literal inside `TestNoGoPluginPackage`
itself, and two are `"plugin"` used as a **slog attribute key** in `host.go`. The precise check —

```bash
grep -rnE '^\s+(_\s+)?"plugin"$' --include=*.go . | grep -v _test.go
```

— returns nothing, and `TestNoGoPluginPackage` (AC-12) walks every Go file checking import lines
rather than any occurrence of the word. The spec's grep is a reasonable smoke test that cannot
distinguish an import from a log key; it is not a defect in the code.

### 11.6 SD-031 — §4.2 names two files that do not exist

§4.2 lists `pkg/notify/slack.go` and `pkg/notify/webhook.go`. Neither exists. `pkg/notify` contains
`notify.go`, `opsgenie.go` and `pagerduty.go`; the Slack and webhook senders are methods on
`Dispatcher` inside `notify.go`, which §4.2 does **not** list.

So the four adapters live where their implementations do: `DispatcherNotifier` (serving both Slack
and webhook) in `notify.go`, and the PagerDuty and OpsGenie adapters beside their senders. Modifying
`notify.go` is not forbidden — §4.3's do-not-touch list is the ingestion hot path, `pkg/recompute`,
`pkg/sketch` and `ee/`, and this spec's §9, like GRVX-1109's, carries no "no file outside §4" clause.
The same reasoning is recorded there. Registered as **SD-031**; it is the fourth instance of the
pattern F-042 describes.

### 11.7 Two guards worth naming

**An adapter cannot be constructed without a cardinality check.** `Host.Impl` returns an error if an
adapter is built with a nil `admitFact`, so the budget cannot be skipped by forgetting to pass one.
`TestAdapterRequiresACardinalityCheck` pins it, and `TestAdapterCannotExceedCardinality` drives a
plugin that returns one templated path and one raw UUID, requiring the second to be dropped. A
plugin's promise to respect `docs/04-non-goals.md` §5 is not evidence that it did.

**A secret never reaches a log or an error.** `TestSecretConfigNeverLogged` drives a plugin past its
failure budget — the path that logs the most — and asserts the configured token appears in neither
the log nor any error text, while the non-secret field and the `[redacted]` marker both do. Dropping
the config entirely would pass a weaker test and leave an operator unable to see what the plugin was
configured with.

### 11.8 Scope

`services/ingestion`, `pkg/recompute`, `pkg/sketch` and `ee/**` are untouched (§4.3). Go's `plugin`
package is imported nowhere. No plugin marketplace or registry service was built (§3 — that is
GRVX-1202), and there is one registry, not two: GRVX-1302 will build on `pkg/plugin.Registry` rather
than beside it.

### 11.9 A test that scribbled in the repository

The guide's example writes `alerts.log` to its working directory, and a plugin inherits the host's
(§5.4). The first run of `TestPluginGuideExampleWorks` therefore left `pkg/plugin/alerts.log` in the
checkout — the same class of problem as F-018, caught here by `git status` before it was committed.
The test now runs from its temp directory via `t.Chdir`, with the guide path resolved to an absolute
one first. The behaviour under test is unchanged; only where it lands is.
