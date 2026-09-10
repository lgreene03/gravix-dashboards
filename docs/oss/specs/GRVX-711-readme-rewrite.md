# SPEC GRVX-711: Rewrite the README — what is free, what is paid, what we refuse to build

| Field | Value |
|---|---|
| **Spec ID** | GRVX-711 |
| **Phase** | 7 |
| **Goal** | G1.4, G3.1 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.4 — no dark patterns. A reader must be able to tell what is free without installing anything. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701 … GRVX-710 (all of Phase 7) |
| **Blocks** | none |
| **Effort** | 1 person-day |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Rewrite `README.md` so that a stranger, in the first screenful, learns: what Gravix is, what it
refuses to be, what is free, what costs money, and how to run it in one command. Every claim in it is
either already true or explicitly labelled as planned.

## 2. Context the implementer needs

- `README.md` exists (9576 bytes, 240 lines). Read it in full before rewriting; it contains accurate
  quick-start commands and endpoint tables worth preserving.
- Its current framing — "**What it is not:** A Datadog/Grafana Cloud replacement" — remains correct
  and must survive. Charter §4 keeps every non-goal.
- `docs/oss/01-competitive-thesis.md` §6 is the claim register. **No claim may appear in the README
  until its proof artefact exists.** All six proof artefacts are Phase 8 or later, so the README
  must not yet assert the cost or correctness claims as facts.
- Phase 7 created: `LICENSE`, `NOTICE`, `LICENSES.md`, `ee/LICENSE`, `ee/README.md`, `CONTRIBUTING.md`,
  `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`, `ADOPTERS.md`, `TRADEMARK.md`,
  `SECURITY.md`, `docs/verifying-releases.md`, `docs/oss/boundary.yaml`.
- GRVX-710 made five previously-paid capabilities free.
- Local endpoint table and quick-start commands in the current README are verified working.

## 3. Non-goals for this spec

- Do NOT assert any claim from the `01-competitive-thesis.md` register as a present fact. Their proof
  artefacts do not exist yet. Planned capabilities are labelled `planned`.
- Do NOT add a comparison table against Datadog or Grafana. That waits for Phase 10's benchmark.
- Do NOT add badges for metrics that do not exist.
- Do NOT remove the existing "What it is not" framing.
- Do NOT add upsell language, a pricing CTA, or a "Pro" banner. Charter §7.4.
- Do NOT change any command that currently works.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| none | This spec rewrites one existing file |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `README.md` | Full rewrite per §5, preserving every working command verbatim |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `docs/oss/01-competitive-thesis.md` | The claim register is the market-analyst's; the README consumes it |
| Every other Phase 7 document | The README links them; it does not restate them |
| `docs-site/**` | The docs site is a separate surface |

## 5. Interface contract

### 5.1 `README.md` — required structure, in order

1. **Title and one-line description.** Preserve the existing CI badge. Add a licence badge pointing
   at `LICENSE`.

2. **What Gravix is** — three sentences maximum. Preserve the existing accurate description.

3. **What Gravix is not** — a bulleted list drawn from `docs/04-non-goals.md`: no distributed
   tracing, no log aggregation, no agents, no sub-minute dashboards, no high-cardinality dimensions,
   no custom query language, no APM. Followed verbatim by:
   `These are permanent. They are in our constitution, not our backlog.`

4. **What is free** — a table with the areas from charter §2.1 and this heading verbatim:
   `## Everything below is free forever, under Apache-2.0`
   Include the note verbatim:
   `RBAC, audit logging, 2FA, TLS, unlimited retention, unlimited services and unlimited seats are free. Charging for the ability to not be breached is the pattern our charter exists to prevent.`

5. **What costs money** — a short table from charter §2.2, introduced verbatim by:
   `Gravix has a paid tier for organisations that run it *for other people*. Code for it lives in `ee/`, is source-available under BUSL-1.1, and converts to Apache-2.0 two years after each release.`
   Then verbatim:
   `The core builds, tests and runs with `ee/` deleted. CI proves this on every pull request.`
   Then: `None of the paid features exist yet. They are Phase 13. See docs/oss/20-roadmap-horizon-2.md.`
   *(That last sentence is removed by GRVX-1312 when Phase 13 ships. State it while it is true.)*

6. **Quick start** — the existing verified commands, unchanged: `cp .env.example .env`,
   `docker-compose up -d --build`, the dashboard URL, the sample `curl`. Preserve the bootstrap
   (`docker-compose.bootstrap.yml`) path too.

7. **Local endpoints** — the existing table, unchanged.

8. **Verify what you are running** — a short section linking `docs/verifying-releases.md`, with the
   `cosign verify-blob` one-liner.

9. **Documentation** — a link table: architecture, deployment, API reference, self-hosting, upgrade
   guide, the charter, the roadmap.

10. **Contributing** — links `CONTRIBUTING.md`, `GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, and states
    verbatim: `Gravix uses the DCO. There is no CLA, and there will never be one — see docs/oss/00-open-core-charter.md §3 for why.`

11. **Licence** — a table mirroring `LICENSES.md`: core Apache-2.0, `ee/**` BUSL-1.1
    (source-available, not open source), docs CC-BY-4.0. Link `LICENSES.md` and `TRADEMARK.md`.

12. **Security** — links `SECURITY.md` with: `Please do not open a public issue for a vulnerability.`

### 5.2 Prohibited content

- Any unproven comparative claim (a number about Datadog or Grafana, or "20× cheaper").
- The word "enterprise-grade", "blazing fast", "next-generation", or "revolutionary".
- Any feature described in the present tense that does not exist. Planned items are marked `planned`.
- Any pricing figure. Phase 13 sets pricing; asserting one now would be invented.

## 6. Behaviour

1. Read the current `README.md` in full. List every command and endpoint in it in the report.
2. Verify each of those commands still works, or is unchanged from a state already verified. Any
   command that does not work is reported as a defect and preserved verbatim, not silently fixed —
   fixing it is a separate change.
3. Write the new `README.md` with all twelve §5.1 sections in order, including all six verbatim passages.
4. Confirm every internal link resolves to a file that exists.
5. Confirm no prohibited content from §5.2 appears.
6. Confirm no claim from `01-competitive-thesis.md` §6 is asserted as a present fact.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| An internal link targets a missing file | stop | `SPEC DEFECT: §6 step 4 — broken link to <path>` |
| A quick-start command no longer works | preserve it, report it | `defect: README command fails: <command>` |
| A thesis claim would be asserted without its proof artefact | stop | `SPEC DEFECT: §5.2 — unproven claim: <quote>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `README.md` contains all twelve §5.1 section headings in order | `TestReadmeStructure` |
| AC-2 | The "permanent … not our backlog" sentence appears verbatim | `TestReadmeStatesNonGoalsArePermanent` |
| AC-3 | The free-tier heading and the "not be breached" note appear verbatim | `TestReadmeStatesWhatIsFree` |
| AC-4 | The `ee/`-deleted CI sentence appears verbatim | `TestReadmeStatesOSSBuildInvariant` |
| AC-5 | `ee/` is described as source-available, never as open source | `TestReadmeLabelsEECorrectly` |
| AC-6 | The no-CLA sentence appears verbatim | `TestReadmeStatesNoCLA` |
| AC-7 | Every internal link resolves to an existing file | `TestReadmeLinksResolve` |
| AC-8 | No prohibited word or phrase from §5.2 appears | `TestReadmeHasNoMarketingFluff` |
| AC-9 | No claim from the thesis register is asserted as a present fact | `TestReadmeAssertsNoUnprovenClaim` |
| AC-10 | Every quick-start command from the previous README is preserved verbatim | `TestReadmeCommandsPreserved` |
| AC-11 | No pricing figure appears | `TestReadmeHasNoPricing` |

## 8. Verification

```bash
# 1. Structure
for s in "What Gravix is" "What Gravix is not" "free forever" "costs money" "Quick start" \
         "Local endpoints" "Verify what you are running" "Documentation" "Contributing" "Licence" "Security"; do
  grep -q "$s" README.md && echo "ok: $s" || echo "MISSING: $s"
done
# expect: eleven "ok:" lines

# 2. Verbatim passages
grep -c "They are in our constitution, not our backlog." README.md
# expect: 1
grep -c "The core builds, tests and runs with \`ee/\` deleted." README.md
# expect: 1
grep -c "There is no CLA, and there will never be one" README.md
# expect: 1

# 3. ee/ labelled honestly
grep -c "source-available" README.md
# expect: >= 1

# 4. No fluff, no invented pricing
grep -ciE "enterprise-grade|blazing fast|next-generation|revolutionary|20x cheaper" README.md || true
# expect: 0
grep -cE '\$[0-9]+/(mo|month)' README.md || true
# expect: 0

# 5. Links resolve
grep -oE '\]\(([^)]+\.md)\)' README.md | sed 's/](\(.*\))/\1/' | while read -r f; do
  test -f "$f" || echo "BROKEN: $f"
done
# expect: no output

# 6. Tests
go test ./tests/... -run TestReadme -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Every command and endpoint from the previous README listed in the report as preserved or reported broken
- [ ] Every internal link resolves
- [ ] Zero prohibited words, zero pricing figures, zero unproven claims
- [ ] `ee/` labelled source-available everywhere it appears
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A broken internal link | Return `SPEC DEFECT: §6 step 4 — <path>` |
| A quick-start command that fails | Preserve it verbatim, report `defect: README command fails: <command>`. Do not fix it here. |
| Pressure to include a comparative number | Refuse. Return `SPEC DEFECT: §5.2` and cite the thesis register: no claim ships before its proof artefact. |
