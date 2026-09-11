<!-- Findings about the CODEBASE, discovered while executing specs. -->
<!-- Defects in the SPECS themselves go in spec-defects.md. Append; do not rewrite. -->
# Repository findings

Things noticed while implementing Horizon 2 that are real problems but outside the scope of the
spec that found them. Each names what is wrong, why it matters, and who should fix it.

A finding is recorded rather than fixed in place because a spec that quietly widens to fix
everything it notices becomes unreviewable. See `11-agent-loops.md` §L4, scope gate.

---

## F-001 — compiled binaries are committed to version control

**Found by:** `senior-engineer` executing GRVX-709
**Owner:** `security-engineer`
**Severity:** medium — supply-chain hygiene

Two compiled binaries are tracked in git at the repository root:

| Path | Size | What it actually is |
|---|---|---|
| `cli` | 8.7 MB | **Mach-O 64-bit arm64 executable** — a macOS binary |
| `service_events_detail` | 27.5 MB | compiled binary |

Three problems:

1. **They cannot be verified against source.** GRVX-709 makes every released binary reproducible
   and signed. These are neither. A user who runs `./cli` from a checkout is executing something
   nobody can attest to.
2. **`cli` is a macOS ARM64 binary in a project whose CI, containers and deployment target Linux.**
   It is not usable by most of the people who will clone the repository, so it is not even serving
   the convenience it presumably existed for.
3. **36 MB of the repository is build output.** Every clone pays for it, forever, including the
   history.

**Recommendation:** delete both, add them to `.gitignore`, and let `make build` produce them into
`bin/` as it already does. `make build` already builds `bin/gravix`, so `cli` is redundant as well
as unverifiable.

**Why this was not fixed here:** GRVX-709 §3 explicitly says to report rather than delete —
removing tracked files is its own change with its own review, and bundling it into a signing
change would hide it.

---

## F-002 — 29 Go files are not gofmt-formatted

**Found by:** `senior-engineer` executing GRVX-701
**Owner:** `senior-engineer`
**Severity:** low — consistency

29 files under `services/`, `transforms/` and `pkg/` do not match `gofmt` output. Verified against a
stashed baseline that **all 29 were already unformatted before Horizon 2 began**, and that adding
licence headers introduced none of them.

**Recommendation:** one mechanical `gofmt -w` change, reviewed as a formatting-only diff so it is
easy to verify by eye, ideally followed by a CI check so it cannot recur.

**Why this was not fixed here:** GRVX-701 §4.2 limited that spec to prepending headers. A
formatting pass touching 29 files inside a licensing change would have been a scope violation and
would have made the licence diff unreadable.
