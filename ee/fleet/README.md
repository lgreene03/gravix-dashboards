<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: BUSL-1.1 -->

# `ee/fleet` — what the console can and cannot do to an install

```
A managed install does not need the console.

Every Gravix installation the fleet console manages runs, ingests, aggregates,
alerts and serves dashboards with the console unreachable — permanently, not for
a grace period. The console reports health it is told about and offers changes an
install may accept. It cannot compel, and it is never in the path of anything
that matters.

If the console is down, nothing observable happens to your monitoring.
```

That is not modesty. A fleet console that can take down monitoring is a worse liability than the
problem it solves, and an observability vendor whose management plane causes outages does not get a
second chance.

## What it can do

| | |
|---|---|
| **Show** | Version, health and configuration hash for each install — everything the install chose to report |
| **Offer** | A configuration `Proposal`, with a stated reason, which an install's operator accepts, declines, or ignores |
| **Stage** | An upgrade, one install at a time by default, signature-verified, backed up first, health-checked after, rolled back automatically on failure |

## What it cannot do

| | |
|---|---|
| **Connect in** | The console never opens a connection to an install. Reporting is outbound only, from the install |
| **Apply a change** | There is no code path by which a proposal becomes a change. An operator applies it or does not |
| **Override a pin** | A setting an operator has pinned is shown as pinned. A proposal touching it is refused, and the rest of the proposal is still offered |
| **Upgrade without approval** | Approval is per install. There is no fleet-wide approval, deliberately |
| **See your host** | See below |

## The agent is not an agent

[`docs/04-non-goals.md`](../../docs/04-non-goals.md) §3 is unambiguous: no sidecar agents, no
host-level daemons, no collecting CPU, memory or disk from hosts. `agent.go` lives inside that
promise because of what it structurally cannot do, not because of how it is described:

- It sends `Report`, which has four fields: install id, version, healthy, config hash. There is
  nowhere in it to put a host metric. `TestAgentCollectsNoHostMetrics` asserts that shape.
- It opens no listener. `TestNoInboundConnections` asserts no `net.Listen`, `http.Server` or
  `ListenAndServe` appears in the package.
- It is off unless `AgentConfig.Enabled` is set *and* a console URL is configured. The zero value
  is silent.
- One local flag disables it, with no console involvement. An operator who wants this process to
  stop talking to anyone does not have to ask the people it talks to.

A self-report from one process about itself is not a host daemon. If it ever drifts toward
collecting something about the machine, the spec's §10 says to cut scope rather than argue about
the word.

## A declined proposal is not a problem

`Disposition.IsError()` returns `false` for every value, including `rejected`. That is deliberate
and it is the whole posture of this package: an install that declined a change is an install whose
operator made a decision. Rendering it as a red row, a "non-compliant" badge or a failure count is
how a management console teaches its users that their judgement is a defect.

## When the console's own licence lapses

Per [`ee/degrade`](../degrade/README.md): console *configuration* becomes read-only — no new
installs, no new proposals, no upgrades. Two things deliberately keep working:

- **Health recording.** An install's self-reports are still accepted. A console that froze its
  fleet view at the moment of expiry would be worse than one showing nothing.
- **Every managed installation.** Nothing an install does is gated on the console's licence, or on
  the console existing. `TestConsoleExpiryDoesNotAffectInstalls` is the assertion.

## Upgrades

Per install, in order: verify the release signature once before anything is touched, require *this*
install's approval, back up per [`docs/disaster-recovery.md`](../../docs/disaster-recovery.md),
apply, health check, and roll back automatically if the check fails. A batch in which anything
rolled back stops the run — a release that failed once is a release, not an accident.

Batch size defaults to **1**. An operator who wants a faster rollout says so; nobody gets one by
forgetting to configure it. Upgrading an entire fleet at once converts a bad release into a total
outage.

If `cosign` is not installed, `CosignVerifier` returns `ErrVerifierUnavailable` and the upgrade is
**refused**, not skipped. An orchestrator that treats "I could not check" as "it is fine" is worse
than one that does not check at all, because it reports success.
