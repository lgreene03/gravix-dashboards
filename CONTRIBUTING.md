# Contributing to Gravix

Thank you for considering it. This document tells you what to build, how to build it, and what
review will check — so your first pull request has the best possible chance of being merged.

## Before you start

Gravix deliberately does not do certain things, and **requests in those categories are declined
regardless of how well they are implemented**. Please read
[`docs/04-non-goals.md`](docs/04-non-goals.md) before writing code. The short version:

- No distributed tracing
- No log aggregation or search
- No agents or host-level daemons, and no infrastructure metrics
- No sub-minute dashboards or streaming query engines
- No high-cardinality dimensions (`user_id`, `request_id`, `session_id`, `ip_address`)
- No custom query language
- No feature parity with Datadog or New Relic

These are in our constitution, not our backlog. If you need one of them, we will happily point you
at a tool that does it well. We would rather tell you that before you spend a weekend than after.

## Development setup

One command, from a fresh clone:

```bash
./scripts/dev_setup.sh
```

It checks your toolchain, fetches modules, and runs the fast suite once, so it is not finished until
a test has passed on your machine. **It installs nothing** — where something is missing it prints
the exact command for your platform and stops.

There is a dev container in `.devcontainer/` if you would rather have the toolchain pinned for you.
It is one supported path, never the only one: with Go installed you do not need Docker to work on
Gravix. Both paths are written up in
[`docs-site/docs/development-setup.md`](docs-site/docs/development-setup.md).

For a running stack:

```bash
cp .env.example .env          # change API_KEY and MINIO_ROOT_PASSWORD at minimum
docker-compose up -d --build  # full stack
```

For a leaner stack (~800 MB rather than ~7.8 GB):

```bash
cp .env.bootstrap.example .env
docker-compose -f docker-compose.bootstrap.yml up -d --build
```

If you would rather not run Docker at all, the golden-path smoke test exercises the pipeline
end to end without it:

```bash
./scripts/golden_path_test.sh
```

## Running tests

```bash
make test-fast                      # run this before you push. Budget: 5 minutes. No Docker.
make test-full                      # everything, including the slow-tagged suites
```

`make test-fast` is unit tests for every package, `schemas/` at 100%, the open-core boundary check,
and the golden path. It is the one to run while you work, and CI enforces the five-minute budget so
it stays that way. `./scripts/test_fast.sh --timing` prints the slowest packages if it has crept up.

**The split is by speed and dependency, never by importance.** No test was deleted, skipped, or
shortened to make the fast suite fast: `tests/e2e/`, `tests/correctness/` and `bench/` carry a
`//go:build slow` tag, and CI runs everything on every pull request.

Exit code **4** from the fast suite means every test passed and the suite was over budget. That is a
real regression and CI fails on it, but it is deliberately not the same code as a failing test.

The individual commands, when you want one of them on its own:

```bash
go test ./...                       # the fast set
go test -tags=slow ./...            # everything
make test-correctness               # the properties Gravix's claims rest on — see below
go test ./schemas/... -v -cover     # must stay at 100% — see below
make lint                           # go vet and staticcheck, the same two CI runs
make check-boundary                 # open-core boundary
node --test tests/cube/*.test.js dashboards/lib/*.test.js   # the Cube model and dashboard
```

### The correctness suite

`make test-correctness` runs `tests/correctness/`, which proves the properties the product's claims
rest on: that a recompute is byte-identical under adversarial conditions, that a late fact lands in
its own bucket and the prior value stays reproducible, that a retroactively added dimension gives
exactly what a from-scratch build would, that a merged sketch's percentile is within its bound, and
that no number the dashboard can show lacks a published contract.

It needs no Docker and runs in about twenty seconds. It is budgeted at five minutes, because a suite
nobody runs locally protects nothing.

**A failure is a defect report, not a chore.** Each one prints the exact `fixtures.Spec` that
reproduces the dataset — paste it into a test and you have the failing case. Do not widen a tolerance
or skip the test to get green: the property is the thing being shipped, and
`docs/oss/correctness-defects.md` is where the ones we have found are recorded, along with what
fixing each would mean.

`schemas/` is held at **100% line coverage**. It is the validation layer every fact passes through,
and a gap there is a gap in the guarantee that bad data never reaches storage. If your change
lowers it, add the test rather than lowering the gate.

## The open-core boundary

Gravix is open core. Everything outside `ee/` is Apache-2.0 and free forever. `ee/` is
**source-available** under BUSL-1.1 — readable, auditable, not open source.

Two rules make that real rather than rhetorical:

```bash
make build-oss    # the core must BUILD with ee/ physically deleted
make test-oss     # and its full test suite must PASS with ee/ deleted
```

No package outside `ee/` may import anything inside it. `make check-boundary` enforces that, along
with licence headers and the placement map in [`docs/oss/boundary.yaml`](docs/oss/boundary.yaml).

A capability that has been released under Apache-2.0 can never be moved into `ee/`. See the
[Open-Core Charter](docs/oss/00-open-core-charter.md) §7.3.

## Sign your commits (DCO)

Gravix uses the [Developer Certificate of Origin](https://developercertificate.org/). Sign off with:

```bash
git commit -s
```

which appends:

```
Signed-off-by: Your Name <your.email@example.com>
```

That line certifies you wrote the change, or have the right to submit it. It is not a copyright
assignment.

**Gravix has no CLA and will never introduce one.** A CLA would let a future owner relicense your
contribution; the DCO cannot. That is a deliberate, permanent constraint on our own behaviour, and
it is the strongest signal we can give that the Apache-2.0 core will stay Apache-2.0. See the
[charter §3](docs/oss/00-open-core-charter.md) for why.

Forgot to sign off? Fix the last commit with:

```bash
git commit --amend -s --no-edit && git push --force-with-lease
```

For several commits:

```bash
git rebase --signoff origin/main && git push --force-with-lease
```

## What review will check

Five gates, in order. A failure stops the review rather than opening a negotiation.

1. **Boundary** — `make check-boundary`, `make build-oss`, `make test-oss` all clean.
2. **Security** — required for any change to authentication, crypto, licence verification,
   ingestion parsing, or SQL construction.
3. **Acceptance** — every acceptance criterion proven by a named test. Zero new skipped tests,
   and `make test-correctness` green. A softened assertion counts as a skipped test.
4. **Docs** — every new flag, endpoint, env var and CLI subcommand documented.
5. **Scope** — the diff touches only what the change needs.

A change to a file the spec did not name is a scope violation even when the change is an
improvement. *Especially* then: an unrelated improvement bundled into a PR makes that PR harder to
review and harder to revert. Send it separately and we will take it gladly.

## Where to send what

| What you have | Where it goes |
|---|---|
| A bug with a reproduction | Issue, using the bug template |
| A problem you want solved | Issue, using the feature template — describe the **problem**, not the solution |
| Something in the non-goals list | Please read `docs/04-non-goals.md` first; we will decline it |
| A security vulnerability | **Not a public issue.** See [`SECURITY.md`](SECURITY.md) |
| A question | GitHub Discussions |
| A design change | An RFC. The process and its directory arrive in `GRVX-1204`; until then, open an issue describing the design and we will treat it as one |

## Your first contribution

Start with the [`good first issue` label](https://github.com/lgreene03/gravix-dashboards/labels/good%20first%20issue).
Every issue carrying it names **the file to change, what done looks like, and the command that tells
you whether you got it right**. An issue missing any of those three is a trap rather than an
invitation, and we treat one that slips through as our bug.

When we apply that label we are promising:

- the file you need to change is named in the issue;
- what "done" looks like is described, not implied;
- there is a command that tells you whether you got it right;
- someone will answer a question on it within 48 hours;
- the change is genuinely wanted, and a correct PR will be merged.

`good first issue` does not mean "small". It means **specified** — small and underspecified is the
worst combination an issue can have, because it looks approachable and then is not.

**Claim one by commenting on it.** No assignment, no form, and we will not assign an issue to
anyone who has not asked. A claim holds for 21 days; if life gets in the way, saying so is a
complete answer and usually turns into us improving the issue.

[`docs/oss/good-first-issues.md`](docs/oss/good-first-issues.md) has the full standard, and
[`docs/oss/good-first-issue-inventory.md`](docs/oss/good-first-issue-inventory.md) is the source
list every entry is checked against in CI.

## Credit

Release notes name everyone whose work is in a release — author and co-author, alphabetically, with
no ranking and no tiers. A one-line fix and a subsystem count the same, because they are worth the
same amount of thank you. A first contribution is marked separately, because merging somebody's
first pull request is worth marking.

Your email address never appears. It is in your commit's `Signed-off-by` trailer because the DCO is
a legal record, and `pkg/relnotes` fails the build rather than publishing it.

If you would rather not be credited at all, add yourself to
[`docs/oss/no-credit.md`](docs/oss/no-credit.md) and you will appear as `an anonymous contributor`.
**No reason is required**, nobody will ask, and CI enforces it rather than somebody remembering.

## The contribution ladder

Contributor → Reviewer → Maintainer, with published, mechanical criteria — no discretionary
promotion, because discretion is how a ladder becomes a clique.

| Level | Can | Cannot |
|---|---|---|
| **Contributor** | Open issues and pull requests; comment; review informally | Merge; approve |
| **Reviewer** | Everything above, plus a binding approval in their subsystem | Merge; grant levels; cut a release |
| **Maintainer** | Everything above, plus merge, release, and grant Contributor→Reviewer | Amend the charter; overrule a veto inside a sprint |

You start as a Contributor by opening your first issue or pull request. Nothing to apply for and
nothing to sign — there is no CLA, deliberately.

Every promotion criterion is evidenced by something public: merged pull requests, review comments, a
written decision. No level requires employment, a commercial relationship, or an NDA.

The full criteria, the nomination process, and what happens on inactivity are in
[`docs/oss/contribution-ladder.md`](docs/oss/contribution-ladder.md).

## Governance

How decisions are made, who can block what, and what is not up for a vote:
[`GOVERNANCE.md`](GOVERNANCE.md). Behaviour expectations:
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## Commit and pull request conventions

- Imperative subject line, under 72 characters: "add burn-rate alerting", not "added" or "adds".
- The body explains **why**, since the diff already shows what.
- One logical change per pull request.
- Sign off every commit.
