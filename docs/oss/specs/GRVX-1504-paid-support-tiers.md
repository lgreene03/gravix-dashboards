# SPEC GRVX-1504: Paid support tiers, separate from feature licensing

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1504 | **Phase** | 15 | **Goal** | G9.4 |
| **Placement** | `ee/` (BUSL-1.1) — the entitlement plumbing only; support itself is a service, not code |
| **Charter basis** | §7.3 Q1..Q5 all NO. Selling **response time and advice** does not withhold any capability: a self-hoster with no contract gets identical software, identical security fixes, and identical documentation. |
| **Implementer role** | `pro-engineer`, with `oss-steward` |
| **Depends on** | GRVX-1501, GRVX-1305 |
| **Blocks** | none |
| **Effort** | 3 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Sell support without it becoming a paywall. A support contract buys a response commitment and
someone's attention; it never buys a fix, a patch, a version, or a piece of documentation that
non-paying users do not get.

## 2. Context the implementer needs

- `GRVX-1501` establishes LTS and makes **security and correctness backports free** for supported versions. Support cannot resell them.
- `SECURITY.md` (GRVX-708) commits to acknowledging any disclosure within 24 hours, regardless of who reports it.
- `.claude/agents/oss-steward.md` targets zero issues unanswered beyond 72 hours and PR queue p90 under 5 days, for everyone.
- `GRVX-1305` provides billing.
- Charter §7.4 forbids dark patterns, which includes degrading the free support experience to make a paid tier attractive.
- `docs/sla.md` (2799 bytes) exists for the hosted offering. Read it; this is a different contract.

## 3. Non-goals for this spec

- Do NOT withhold a fix, patch, or backport from a non-paying user. Ever.
- Do NOT degrade community support to differentiate. The 72-hour community target stands, and a paid tier must not be created by letting the free one rot.
- Do NOT gate documentation, a knowledge base, or a troubleshooting guide behind a contract.
- Do NOT promise a fix timeline. Support sells **response**, not **resolution** — a promise to fix an arbitrary bug in a fixed window is one nobody can keep honestly.
- Do NOT edit any core file.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/support/entitlement.go` | Contract entitlements and response-clock computation |
| `ee/support/entitlement_test.go` | Tests |
| `ee/support/register.go` | Extension-point registration |
| `docs-site/docs/support.md` | What support buys, and what everyone gets free |

### 4.2 Files to modify

| Path | Change |
|---|---|
| none — **zero core files** | |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `SECURITY.md` | Disclosure commitments apply to everyone |
| `scripts/backport.sh`, `pkg/version/**` | LTS backports are free (GRVX-1501) |
| Every path outside `ee/` | `pro-engineer` cannot edit core |

## 5. Interface contract

### 5.1 What support sells, and what it does not

| | Community (free) | Standard | Premium |
|---|---|---|---|
| The software | identical | identical | identical |
| Security fixes | all, free | all, free | all, free |
| Correctness fixes | all, free | all, free | all, free |
| LTS backports | all, free | all, free | all, free |
| Documentation | all | all | all |
| Public issue response | best effort, 72h target | 72h target | 72h target |
| **Private channel** | — | ✅ | ✅ |
| **First response** | best effort | 1 business day | 4 business hours |
| **Named contact** | — | — | ✅ |
| **Architecture review** | — | 1/year | 4/year |
| **Upgrade assistance** | docs | docs + async help | scheduled, attended |

Every row above the bold ones is identical across all three columns. That is the design: the
product is the same, and what varies is how quickly a human looks at your specific problem.

### 5.2 The commitment, verbatim in `docs-site/docs/support.md`

```
A support contract does not buy you a better Gravix.

It buys you a faster answer. The software is identical, the security fixes are
identical, the LTS backports are identical, and the documentation is identical.
If we fix a bug you reported under contract, that fix ships to everyone in the
next release — we do not hold patches for paying customers, and we never will.

What you are actually paying for is that when you open a ticket at 09:00, someone
whose job it is to care about your deployment reads it that morning rather than
when they get to it.

If that is not worth it to you, use the free tier. It is the same product, and
we mean that.
```

### 5.3 Response clock, honestly computed

```go
// Package support implements Gravix Enterprise support entitlements. It sells
// response time, never capability: every fix, patch and backport reaches
// non-paying users identically and at the same time.
package support

// Tier is a support level.
type Tier string

const (
    TierCommunity Tier = "community"
    TierStandard  Tier = "standard"
    TierPremium   Tier = "premium"
)

// FirstResponseTarget returns the committed time to a first human response.
// It is a response commitment, not a resolution commitment: no tier promises
// that a bug will be fixed within any window, because no honest vendor can.
func FirstResponseTarget(t Tier) time.Duration

// BusinessHours defines when the clock runs. Outside them it pauses, which is
// stated on the support page rather than discovered when a ticket ages overnight.
type BusinessHours struct {
    Timezone string   // the customer's, agreed at contract
    Days     []time.Weekday
    Start    string   // "09:00"
    End      string   // "17:00"
    Holidays []string // agreed at contract, published in the customer's portal
}

// Deadline computes when a first response is due from a ticket's arrival.
func Deadline(t Tier, hours BusinessHours, arrived time.Time) time.Time
```

Business hours pausing the clock is normal and fair; hiding it is not. The support page states it
plainly, with a worked example of a Friday-evening ticket.

### 5.4 Fixes are never withheld

Enforced structurally, not by policy:

- A fix arising from a support ticket is a normal pull request against `main`, reviewed and merged like any other, and released on the normal train.
- There is no private branch, no customer-specific build, and no "hotfix available to contract holders".
- If a fix warrants an out-of-band release, that release is public and unrestricted.
- `ee/support/` contains **no code path that produces a binary, patch, or artefact**. It computes entitlements and routes tickets. That absence is testable and is tested.

### 5.5 Degrade behaviour

In `StateReadOnly` (GRVX-1303): existing tickets stay readable, new tickets fall back to the
community channel, and the customer is told so plainly. An expired support contract must never mean
a customer cannot report a security issue — `SECURITY.md`'s channel is open to everyone,
permanently, and the support page says so.

## 6. Behaviour

1. Implement tiers, response targets and the business-hours clock.
2. Implement `Deadline` with a worked Friday-evening example in the tests and in the docs.
3. Verify `ee/support/` produces no artefact of any kind.
4. Write the support page with the §5.2 commitment verbatim and the identical-rows table.
5. Apply `degrade.Guard` per §5.5, leaving the security channel open in every state.
6. Verify no fix path is gated by tier.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Ticket outside business hours | clock pauses; deadline computed from the next business hour | `support: clock resumes <time> <timezone>` |
| Contract expired | new tickets route to community; existing stay readable | `support: contract expired; the community channel and the security channel remain open` |
| A fix would be withheld from a non-contract user | refuse the design | `support: fixes are never tier-gated` |
| Security report from a non-contract user | full `SECURITY.md` SLA applies | — |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | Every capability row is identical across all three tiers | `TestCapabilitiesIdenticalAcrossTiers` |
| AC-2 | `ee/support/` produces no binary, patch, or artefact | `TestSupportProducesNoArtefact` |
| AC-3 | No fix path is gated by tier | `TestNoTierGatedFixes` |
| AC-4 | LTS backports are available regardless of contract | `TestBackportsIgnoreContract` |
| AC-5 | The commitment appears verbatim | `TestSupportCommitmentVerbatim` |
| AC-6 | Response targets are response, never resolution | `TestNoResolutionPromise` |
| AC-7 | The business-hours clock pauses correctly, with the Friday example | `TestBusinessHoursClock` |
| AC-8 | An expired contract leaves the security channel open | `TestSecurityChannelAlwaysOpen` |
| AC-9 | The community 72-hour target is unchanged | `TestCommunityTargetUnchanged` |
| AC-10 | No documentation is gated | `TestNoGatedDocumentation` |
| AC-11 | `git diff` touches no path outside `ee/` | `TestNoCoreFilesModified` |
| AC-12 | Core builds and tests green with `ee/` deleted | `TestCoreBuildsWithoutEE` |

## 8. Verification

```bash
# 1. Support sells response, not capability
go test ./ee/support/... -run 'TestCapabilitiesIdenticalAcrossTiers|TestSupportProducesNoArtefact|TestNoTierGatedFixes|TestBackportsIgnoreContract' -v
# expect: PASS

# 2. The commitment
grep -c "A support contract does not buy you a better Gravix." docs-site/docs/support.md
grep -c "It is the same product, and" docs-site/docs/support.md
# expect: 1 each

# 3. No resolution promises
go test ./ee/support/... -run TestNoResolutionPromise -v
grep -ciE "guaranteed fix|resolution within|will be fixed within" docs-site/docs/support.md || true
# expect: PASS; 0

# 4. Security is never contingent on payment
go test ./ee/support/... -run TestSecurityChannelAlwaysOpen -v
# expect: PASS

# 5. Free support is not degraded
go test ./ee/support/... -run 'TestCommunityTargetUnchanged|TestNoGatedDocumentation' -v
# expect: PASS

# 6. Charter §7.1
git diff --name-only | grep -v '^ee/' | grep -c . || true
make build-oss && make test-oss && make check-boundary
# expect: 0; all succeed
```

## 9. Definition of done

- [ ] All twelve acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `ee/support/` demonstrated to produce no artefact
- [ ] The commitment present verbatim, including the closing line
- [ ] Zero resolution-time promises anywhere
- [ ] `docs-engineer` delta merged, labelling this source-available
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| Pressure to hold a patch for contract holders | Refuse absolutely, citing §5.4. Route to `license-boundary-auditor`. This is the clearest possible charter violation. |
| Pressure to let community response slip to sell Standard | Refuse, citing §7.4. Route to `oss-steward`, who owns the 72-hour target. |
| A request for a resolution-time guarantee | Decline and explain. Nobody can honestly promise to fix an unknown bug in a fixed window, and promising it sells a lie. |
