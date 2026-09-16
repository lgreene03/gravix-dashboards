# Plugin System Design (v2)

Gravix supports extensibility through a stable, versioned ABI. Plugins run as **subprocesses
speaking JSON-RPC 2.0 over stdio**, so a plugin built against one Gravix release keeps working
against the next.

The contributor-facing guide, with a complete worked example, is
[Writing a plugin](../docs-site/docs/writing-a-plugin.md). This document records the design and what
changed from v1.

## What changed from v1

v1 (Horizon 1) described in-process, compile-time Go interfaces registered with `Register*` calls at
startup. v2 replaces the loading model entirely and narrows the extension points.

| v1 | Status in v2 | Why |
|---|---|---|
| **Loading**: build-time, in-process, `plugin.Open` or compiled-in registration | **Superseded** | Go's `plugin` package couples every plugin to the host's exact toolchain and dependency versions, is ELF-only, and cannot isolate a crash. Every Gravix release would break every plugin. |
| **Notification Channel** plugins (`notify.RegisterChannel`) | **Kept**, as `Notifier` | The one v1 extension point that was both safe and wanted. The four built-in senders now implement the same interface, in-process. |
| **Transform** plugins (`etl.Transform`) | **Removed** | A transform sits inside the derivation of metrics from facts. Correctness code takes no plugins: `pkg/recompute` and `pkg/sketch` are what make a number reproducible, and third-party code in that path would make "recomputable" unprovable. |
| **Storage Backend** plugins (`storage.ObjectStore`) | **Removed** | `ObjectStore` is core infrastructure on the write path for every fact. A plugin there sits between a request and its durable write, which is the one place a third party's latency must never appear. |
| **Auth Provider** plugins | **Removed** | Authentication is served natively by SSO/OIDC and API keys. A pluggable authenticator is a pluggable way to be wrong about who someone is. |
| — | **New**: `Exporter` | Writing batches to an external destination is the common integration request and is safely outside the correctness path. |
| — | **New**: `Adapter` | Converting a foreign payload into facts, at batch boundaries only, with the cardinality budget enforced on the Gravix side. |

Three extension points were removed. That is a deliberate narrowing, not an oversight: each sat
either inside the correctness path or on the ingestion hot path, and an ABI that cannot be extended
there is what lets the rest of the ABI be stable.

## The three kinds

| Kind | Interface | Invoked |
|---|---|---|
| `notifier` | `Notify(ctx, Alert) error` | when an alert fires |
| `exporter` | `Export(ctx, Batch) (ExportResult, error)` | at batch boundaries |
| `adapter` | `Convert(ctx, payload, contentType) ([]RequestFact, error)` | at batch boundaries |

`pkg/plugin` defines all three. `pkg/notify` provides the reference notifiers; `pkg/export` provides
the reference export formats.

## Transport

JSON-RPC 2.0, one request per line on stdin, one response per line on stdout. Three methods:

- `gravix.describe` → `Manifest` (the startup handshake)
- `gravix.notify` / `gravix.export` / `gravix.convert` → the kind's single operation

`ABIVersion` is `"1"`. It changes only when an existing method's request or response shape changes;
adding a method does not change it. A plugin declaring a different version is refused at the
handshake, before it is ever called.

## Isolation

| Property | Rule |
|---|---|
| Timeout | 30s per call by default, configurable, hard-killed after |
| Crash | Restarted with exponential backoff to 5 minutes; the host continues |
| Failure budget | 5 consecutive failures disable the plugin and log it; it never blocks the pipeline |
| Memory | 256 MB RSS by default, read from `/proc`; a no-op on platforms without it |
| Filesystem | Inherits the host's working directory |
| Network | **Unrestricted.** A notifier must reach Slack. |

The network rule is a genuine trust boundary and the contributor guide states it directly:
installing a third-party plugin is installing third-party code with network access, exactly like any
other dependency.

## What a plugin cannot do

- **Sit on the ingestion hot path.** Adapters run at batch boundaries. `services/ingestion` does not
  import `pkg/plugin`, and a test enforces it.
- **Bypass the cardinality budget.** An adapter's facts are checked on the Gravix side.
  `docs/04-non-goals.md` §5 is not waived by installing something.
- **Reach Gravix internals.** A plugin sees the ABI and nothing else. There is no shared memory, no
  shared process, and no import.
- **Leak a configured secret into a log.** Config fields typed `secret` are redacted everywhere the
  host reports on a plugin.

## Registry

One registry (`pkg/plugin.Registry`), not one per kind. Charter §7.2 has `ee/` register against core
extension points, and two registries would mean two answers to "what is installed". Registration
validates the manifest, refuses an ABI mismatch and a duplicate name, and checks that a plugin
implements the kind it claims.
