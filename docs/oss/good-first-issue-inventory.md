<!-- Audited by scripts/gfi_audit.sh and tests/governance/gfi_test.go. -->
# `good first issue` inventory

The source list behind the
[`good first issue` label](https://github.com/lgreene03/gravix-dashboards/labels/good%20first%20issue).

Each entry carries the five headings the label promises:
[`docs/oss/good-first-issues.md`](good-first-issues.md) says what those promises are. CI checks
every entry — the file must exist, and the verification command's first token must be a real
executable — so an entry cannot rot into a trap without the build going red.

Nothing here is invented to hit a number. Every entry is work this repository actually wants:
an untested function named by `go tool cover`, a register entry, or a documented gap.

**Status** is `unclaimed` or `claimed by @handle on YYYY-MM-DD`. A claim holds for 21 days.

---

## GFI-01 — Test the JWT claims helpers in pkg/auth

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/auth/jwt.go`

### The change

`WithClaims`, `ClaimsFromContext` and `HasRole` (lines 21, 26 and 48) have no test at all — the whole file reports 0% on those three.

Add `pkg/auth/jwt_test.go` covering: a context round-trip through `WithClaims` then `ClaimsFromContext`; `ClaimsFromContext` on a context that never carried claims (it must not panic, and must report that there were none); and `HasRole` for a role the claims have, a role they do not, and empty claims.

### How to verify

```bash
go test ./pkg/auth/ -run TestClaims -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

These three functions decide who is allowed to do what. An authorisation helper with no test is the one place a silent change is most expensive.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-02 — Test Bloom filter false-positive-rate reporting

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/bloom/bloom.go`

### The change

`FalsePositiveRate` at line 107 is untested. Add a case to `pkg/bloom/bloom_test.go` that builds a filter with a known size and element count and asserts the returned rate is within a sensible band — and that an empty filter reports 0 rather than NaN.

### How to verify

```bash
go test ./pkg/bloom/ -run TestFalsePositiveRate -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

The rate is what tells an operator whether the filter is still doing its job as data grows; a wrong number here is worse than no number.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-03 — Test BaseURLFromEnv's trailing-slash handling

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/email/email.go`

### The change

`BaseURLFromEnv` at line 200 is untested. It has three behaviours worth pinning: unset `BASE_URL` returns `http://localhost:8000`; a value with a trailing slash has it stripped; a value without one is returned unchanged.

Use `t.Setenv` so the test does not leak into others.

### How to verify

```bash
go test ./pkg/email/ -run TestBaseURLFromEnv -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Every link in every email is built on this. A stray double slash is the kind of thing nobody notices until a customer forwards a screenshot.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-04 — Test RenderFirstData

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/email/templates.go`

### The change

`RenderFirstData` at line 214 is untested. Add a case asserting the HTML contains the tenant name, that the plain-text alternative is non-empty and contains the dashboard URL, and that no template action (`{{`) survives into either output.

### How to verify

```bash
go test ./pkg/email/ -run TestRenderFirstData -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

This is the first email a new user gets after their data arrives. An unrendered template in it is the worst possible first impression.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-05 — Test RenderTryAlerting

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/email/templates.go`

### The change

`RenderTryAlerting` at line 225 is untested. Same shape as the `RenderFirstData` case: the HTML renders, the text alternative is non-empty and carries the `#alerts` link, and no template action survives.

### How to verify

```bash
go test ./pkg/email/ -run TestRenderTryAlerting -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Nudge emails are sent unattended. Nobody reads them before they go out, so the test is the only reader they get.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-06 — Test RenderUpgradeNudge

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/email/templates.go`

### The change

`RenderUpgradeNudge` at line 236 is untested. Assert the HTML renders, the text alternative carries the settings link, and no template action survives.

### How to verify

```bash
go test ./pkg/email/ -run TestRenderUpgradeNudge -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Charter §7.4 forbids dark patterns, and an upgrade nudge is exactly where one would appear. Having the rendered text under test makes the wording reviewable.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-07 — Test AnnualPriceIDFor

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/billing/stripe.go`

### The change

`AnnualPriceIDFor` at line 206 is untested. Build a `StripeService` with `NewStripeService("", "", plans)` and assert: a monthly price ID returns its configured annual ID; a plan with no annual price returns `""`; and an unknown price ID returns `""` rather than panicking.

### How to verify

```bash
go test ./pkg/billing/ -run TestAnnualPriceIDFor -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Billing code that is wrong charges somebody the wrong amount. Charter-wise, predictable billing is one of the four axes Gravix claims to be better on — an untested price lookup undercuts that claim.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-08 — Test IsAnnualPriceID, including the two-key indexing

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/billing/stripe.go`

### The change

`IsAnnualPriceID` at line 214 is untested, and the way it works is not obvious: `NewStripeService` indexes `s.plans` under **both** the monthly and the annual price ID (stripe.go lines 36-40), which is what makes `s.plans[priceID]` resolve for an annual ID at all.

Add a test that pins that: an annual ID returns true, the matching monthly ID returns false, and an unknown ID returns false. A comment saying why the double indexing matters would help the next reader.

### How to verify

```bash
go test ./pkg/billing/ -run TestIsAnnualPriceID -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

The behaviour depends on a detail of how the map is built fifty lines away. That is exactly the kind of thing a refactor breaks quietly.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-09 — Test DefaultPlansLegacy's tier mapping

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/billing/billing.go`

### The change

`DefaultPlansLegacy` at line 112 is untested. It maps the legacy three-tier names onto plan configs and is marked deprecated, which is precisely when a test is worth having.

Assert it returns three plans named `free`, `team` and `business`, with the event limits, seat limits and retention days the function currently sets.

### How to verify

```bash
go test ./pkg/billing/ -run TestDefaultPlansLegacy -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

A deprecated function still runs for existing customers. Pinning what it returns is how it can eventually be deleted with confidence rather than hope.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-10 — Test the billing mock's ParseWebhook and ListInvoices

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/billing/mock.go`

### The change

`ParseWebhook` at line 55 and `ListInvoices` at line 71 are both untested. They are the mock every other billing test relies on, so a mock that drifts from the real `StripeService` behaviour makes those tests pass for the wrong reason.

Add `pkg/billing/mock_test.go` asserting that `ParseWebhook` returns the event it was given, rejects malformed input the way the real one does, and that `ListInvoices` returns what was seeded.

### How to verify

```bash
go test ./pkg/billing/ -run TestMock -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

A test double that nobody tests is a second implementation nobody checks. When it drifts, every test built on it lies in the same direction.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-11 — Fill in the empty [Unreleased] section of the changelog

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`CHANGELOG.md`

### The change

`[Unreleased]` is empty, while Horizon 2 has landed phases 7 through 12. Finding F-041 records this.

Add entries under `Added`, `Changed` and `Fixed` for the work in `docs/oss/20-roadmap-horizon-2.md` that is actually merged — one line per user-visible change, not per commit. `git log --oneline` on the default branch is the source; the roadmap says what each GRVX id was for.

### How to verify

```bash
grep -c 'GRVX-' CHANGELOG.md
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Charter §6 requires a changelog entry for a charter amendment, and an adopter deciding whether to upgrade reads this file first. An empty one says nothing has happened, which is false.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-12 — Fix GRVX-1202's verification command, which cannot pass as written

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`docs/oss/specs/GRVX-1202-plugin-registry-scaffold.md`

### The change

§8 command 4 runs `grep -c "That is all we check." registry/README.md` and expects `1`. It returns `0`, and always will: §5.3's verbatim disclaimer wraps that sentence across two lines, and `grep -c` counts lines. Spec defect SD-032 has the details.

Change the second grep to match a fragment that survives the wrap — `grep -c "is all we check." registry/README.md` — leaving §5.3 untouched.

### How to verify

```bash
grep -c 'is all we check.' registry/README.md
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

A verification command that cannot pass trains implementers to skip verification commands.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-13 — Add the missing test to GRVX-1203's verification command

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`docs/oss/specs/GRVX-1203-contribution-ladder.md`

### The change

§8 command 5's `-run` pattern has nine alternatives for §7's ten tests. `TestNoDiscretionaryCriteria` — AC-3's test, and the one §3 and §10 both call the point of the document — is not matched by any of them, because `go test -run` matches unanchored and the shared substring is `Criteria`, not `TestCriteria`. Spec defect SD-033 has the details.

Add `TestNoDiscretionary` to the alternation.

### How to verify

```bash
go test ./tests/governance/ -run 'TestLadder|TestCriteria|TestNoDiscretionary|TestNoLevel|TestEmeritus|TestRemoval|TestContributingLinks|TestGovernancePointer|TestMaintainersHas|TestNoCommercial' -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Ten acceptance criteria, nine of them actually verified, is the kind of gap that is invisible until the unverified one breaks.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-14 — Recognise three more licences in the plugin registry

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/registry/registry.go`

### The change

`licensePhrases` at the bottom of the file knows Apache-2.0, MIT, the two BSD variants, MPL-2.0, GPL-3.0 and AGPL-3.0. ISC, 0BSD and Unlicense are common in small plugins and are not there, so listing check 2 falls back to looking for the bare SPDX string in the LICENSE file — which those licences do not contain.

Add a phrase for each, and extend `TestLicenceMatchingCoversTheCommonLicences` with a case per licence using real text from it.

### How to verify

```bash
go test ./pkg/registry/ -run TestLicenceMatching -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

Check 2 is a mechanical gate on someone else's plugin being listed. A gate that rejects honest submissions on a licence we did not think of is the kind of unfairness the registry README promises not to have.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-15 — Give every docs-site page a unique sidebar position

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`docs-site/docs/plugin-registry.md`

### The change

Three pages declare `sidebar_position: 7` — `plugin-registry.md`, `architecture-overview.md` and `vs-datadog.md` — and other numbers collide too. Docusaurus then orders them by filename, which is not the order anyone intended.

Renumber the pages in `docs-site/docs/` so each `sidebar_position` is unique, keeping the current intended grouping (getting started, then guides, then comparisons).

### How to verify

```bash
grep -h sidebar_position docs-site/docs/*.md | sort | uniq -d
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

The sidebar is how a first-time reader navigates. An order nobody chose is one that puts the comparison pages before the tutorial.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---

## GFI-16 — Test tenantdb's OpenFromEnv

**Status:** unclaimed  
**Opened:** 2026-09-16

### The file

`pkg/tenantdb/open.go`

### The change

`OpenFromEnv` at line 19 is untested. Add `pkg/tenantdb/open_test.go` covering: the default path when no environment variable is set; an explicit SQLite path via the environment, opened into a `t.TempDir()`; and an unreadable path returning an error rather than a nil handle.

Use `t.Setenv` so the test does not leak into others.

### How to verify

```bash
go test ./pkg/tenantdb/ -run TestOpenFromEnv -v
```

Expected: the command succeeds and reports the new coverage or the new count.

### Why this matters

This is the first thing every service calls at startup. A confusing failure here is a confusing failure for the whole product.

### If you get stuck

Comment on the issue, or open a
[discussion](https://github.com/lgreene03/gravix-dashboards/discussions). Someone will answer
within 48 hours. If this entry is wrong — the file moved, the command does not run, the change
is not actually wanted — say so; that is our bug, not yours.

---
