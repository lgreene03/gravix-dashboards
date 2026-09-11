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
go test ./...                       # everything
go test ./schemas/... -v -cover     # must stay at 100% — see below
make check-boundary                 # open-core boundary
```

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
3. **Acceptance** — every acceptance criterion proven by a named test. Zero new skipped tests.
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

## The contribution ladder

Contributor → Reviewer → Maintainer, with published, mechanical criteria — no discretionary
promotion, because discretion is how a ladder becomes a clique.

The full criteria arrive with `GRVX-1203`. Until then, [`GOVERNANCE.md`](GOVERNANCE.md) describes
the levels and how promotion works.

## Governance

How decisions are made, who can block what, and what is not up for a vote:
[`GOVERNANCE.md`](GOVERNANCE.md). Behaviour expectations:
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## Commit and pull request conventions

- Imperative subject line, under 72 characters: "add burn-rate alerting", not "added" or "adds".
- The body explains **why**, since the diff already shows what.
- One logical change per pull request.
- Sign off every commit.
