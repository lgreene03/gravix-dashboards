# SPEC GRVX-1401: Prove Gravix Cloud runs the unmodified OSS core, by build provenance

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1401 |
| **Phase** | 14 |
| **Goal** | G8.1 |
| **Placement** | `ee/` (BUSL-1.1) |
| **Charter basis** | §7.2 — this spec solves a problem that appears only when running Gravix *for other people* (proving to a paying stranger that the operator did not fork the code). Crippleware Test: Q1 NO (a self-hoster has no cloud image to verify), Q2 NO, Q3 NO, Q4 NO (never previously core), Q5 NO (the only reason to gate it is not money — a self-hoster gains nothing from this tooling). |
| **Implementer role** | `senior-engineer` for the `ci.yml` attestation step (core); `pro-engineer` for `ee/cloud/provenance/` |
| **Depends on** | none |
| **Blocks** | GRVX-1402, GRVX-1405, GRVX-1406, GRVX-1407 |
| **Effort** | 6 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, every container image `docker-build` publishes to `ghcr.io/lgreene/gravix-dashboards-*`
carries a signed GitHub build-provenance attestation, and a new verifier tool
(`ee/cloud/provenance`) proves — by checking that attestation against the public commit
history of `github.com/lgreene/gravix-dashboards` — that the image was built from an
unmodified, public commit on `main`, by the project's own GitHub Actions workflow, and not
from a private fork or a locally patched tree. G8.1 ("Cloud runs the same OSS core, no private
fork") stops being an assertion and becomes a command anyone can run.

## 2. Context the implementer needs

- `.github/workflows/ci.yml:198-254` — job `docker-build` builds and pushes four images
  (`ingestion`, `rollup`, `load-generator`, `gateway`) to
  `ghcr.io/${{ github.repository }}-<image>` using `docker/build-push-action@v6`, gated to
  `github.event_name == 'push' && github.ref == 'refs/heads/main'`, with
  `permissions: { contents: read, packages: write }` (`ci.yml:203-205`).
- `.github/workflows/ci.yml:225-241` — `docker/metadata-action@v5` produces the tags consumed
  by the build step: `sha-<shortsha>`, semver tags, and `latest` on the default branch.
- The repository has no build-provenance attestation step today. Verified:
  `grep -c attest-build-provenance .github/workflows/ci.yml` returns `0`.
- Go module path is `github.com/lgreene/gravix-dashboards` (`go.mod:1`).
- `ee/` does not exist yet in this checkout; it is created by `GRVX-702` and is assumed present
  by the time this spec is built, per the Phase 7 → Phase 14 sequencing in
  `docs/oss/20-roadmap-horizon-2.md`.
- The GitHub CLI's native attestation verification command, `gh attestation verify`, and the
  GitHub Action `actions/attest-build-provenance`, are the mechanism used here — no `cosign`
  key material is generated or stored by this spec.

## 3. Non-goals for this spec

- Do NOT modify what the four images build or how they are tagged. Only the provenance
  attestation step and its verifier are added.
- Do NOT implement Bring-Your-Own-Bucket. That is `GRVX-1402`.
- Do NOT implement SBOM generation or `cosign` artifact signing (`GRVX-709`, Phase 7). This spec
  depends only on GitHub's native attestation feature.
- Do NOT touch `services/`, `transforms/`, `pkg/`, `schemas/`, `cmd/`, or `deploy/gravix/`. This
  spec's only core-file touchpoint is `.github/workflows/ci.yml`, and only to add an
  attestation step — it does not change what is built.
- This spec does not cross non-goal §7 (No Feature Parity with Datadog) — it is release
  infrastructure, not a product feature.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `ee/cloud/provenance/provenance.go` | `VerifyImage` — checks an image's attestation against the public commit history |
| `ee/cloud/provenance/provenance_test.go` | Tests for `VerifyImage` using a fake `gh` binary |
| `ee/cloud/provenance/ci_wiring_test.go` | Parses `.github/workflows/ci.yml` and asserts the attestation step exists |
| `ee/cloud/provenance/testdata/gh-attestation-verify-valid.json` | Canned `gh attestation verify --format json` output for a valid attestation |
| `ee/cloud/provenance/testdata/gh-attestation-verify-wrong-repo.json` | Canned output where the attested repository does not match |
| `ee/cloud/provenance/testdata/fake-gh.sh` | Test double for the `gh` CLI, dispatches on `$1 $2` to the two fixtures above |
| `ee/cloud/cmd/verify-provenance/main.go` | CLI entrypoint wrapping `provenance.VerifyImage` |
| `ee/cloud/cmd/verify-provenance/main_test.go` | Tests for flag parsing and exit codes |

### 4.1a Split of work — read before starting

This spec spans the boundary and therefore has **two implementers**, because
`pro-engineer` cannot edit a core file (charter §7.2, `.claude/agents/pro-engineer.md`).

| Part | Files | Role | Placement |
|---|---|---|---|
| Emit build provenance | `.github/workflows/ci.yml` | `senior-engineer` | **core** |
| Verify build provenance | `ee/cloud/provenance/**`, `ee/cloud/cmd/**` | `pro-engineer` | `ee/` |

The `ci.yml` change is a **core** change: emitting an attestation belongs with the signing and SBOM
work in `GRVX-709`, benefits every self-hoster, and must keep working with `ee/` deleted. Only the
*verification tooling that Gravix Cloud runs against its own images* is commercial.

`pro-engineer` must therefore land the `ee/` half only, against a `ci.yml` that already emits
attestations. If it does not, return `EXTENSION POINT REQUIRED` naming the missing attestation step
rather than editing the workflow.

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/workflows/ci.yml` | In job `docker-build`, add `id-token: write` and `attestations: write` to `permissions` (`ci.yml:203-205`), and add a step using `actions/attest-build-provenance@v2` immediately after the `Build and push` step (`ci.yml:244-253`), once per matrix image |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/ingestion/`, `services/gateway/`, `transforms/`, `pkg/`, `schemas/`, `cmd/` | This spec verifies the build; it does not change what is built. |
| Everything under `ee/` (for `senior-engineer`) and everything outside `ee/` (for `pro-engineer`) | The two halves are landed by different roles per §4.1a. Neither crosses into the other's tree. |
| `.github/workflows/deploy.yml`, `.github/workflows/release.yml` | Out of scope; provenance is attached at build time in `ci.yml`, not at deploy or release time |
| `ee/tenancy/`, `ee/billing/` | Out of scope for this spec; other Phase 14 specs own them |

## 5. Interface contract

### 5.1 `.github/workflows/ci.yml` — `docker-build` job changes

Permissions block (`ci.yml:203-205`) becomes:

```yaml
    permissions:
      contents: read
      packages: write
      id-token: write
      attestations: write
```

Immediately after the existing `Build and push` step (`ci.yml:244-253`), add:

```yaml
      - name: Attest build provenance
        uses: actions/attest-build-provenance@v2
        with:
          subject-name: ${{ env.REGISTRY }}/${{ env.IMAGE_PREFIX }}-${{ matrix.image }}
          subject-digest: ${{ steps.build.outputs.digest }}
          push-to-registry: true
```

The existing `Build and push` step must be given `id: build` (it currently has no `id`) so
`steps.build.outputs.digest` resolves.

### 5.2 `ee/cloud/provenance/provenance.go`

```go
// Package provenance verifies that a Gravix Cloud container image was built
// by the project's own public GitHub Actions workflow from a commit that is
// part of the public history of github.com/lgreene/gravix-dashboards, and not
// from a private fork or a locally modified tree.
package provenance

import (
	"context"
	"errors"
	"time"
)

// Result is the outcome of a provenance check for one image reference.
type Result struct {
	ImageRef      string
	Verified      bool
	CommitSHA     string
	RepositoryRef string    // e.g. "lgreene/gravix-dashboards"
	WorkflowRef   string    // e.g. ".github/workflows/ci.yml@refs/heads/main"
	CheckedAt     time.Time
	Reason        string // non-empty when Verified is false
}

// VerifyImage runs `gh attestation verify oci://<imageRef> --owner <ownerLogin> --format json`,
// parses the result, confirms the attested repository equals wantRepo (e.g.
// "lgreene/gravix-dashboards"), and confirms the attested commit SHA is an
// ancestor of wantBranch in the git history at repoDir (a local clone of the
// public repository). ghBinary is the path to the `gh` executable; pass "gh"
// to resolve it from PATH.
func VerifyImage(ctx context.Context, ghBinary, imageRef, ownerLogin, wantRepo, wantBranch, repoDir string) (Result, error)

var (
	// ErrGHCLIMissing is returned when ghBinary cannot be executed.
	ErrGHCLIMissing = errors.New("provenance: gh CLI not found or not executable")
	// ErrAttestationMissing is returned when gh reports no attestation for the image.
	ErrAttestationMissing = errors.New("provenance: no attestation found for image")
	// ErrRepositoryMismatch is returned when the attested repository differs from wantRepo.
	ErrRepositoryMismatch = errors.New("provenance: attested repository does not match")
	// ErrCommitNotOnBranch is returned when the attested commit is not an ancestor of wantBranch.
	ErrCommitNotOnBranch = errors.New("provenance: attested commit is not an ancestor of the public branch")
)
```

### 5.3 `ee/cloud/cmd/verify-provenance/main.go`

Flags (all `flag` package, `flag.ExitOnError`):

| Flag | Type | Default | Help string |
|---|---|---|---|
| `--image` | string | `""` | Fully qualified image reference to verify, e.g. ghcr.io/lgreene/gravix-dashboards-ingestion:sha-abc1234 (required) |
| `--owner` | string | `"lgreene"` | GitHub account or organisation that must own the attestation |
| `--repo` | string | `"lgreene/gravix-dashboards"` | Repository the attested commit must belong to |
| `--branch` | string | `"main"` | Branch the attested commit must be an ancestor of |
| `--repo-dir` | string | `"."` | Path to a local clone of --repo used to check commit ancestry |
| `--gh-binary` | string | `"gh"` | Path to the gh CLI executable |

Exit codes: `0` verified; `1` verification failed (`Result.Verified == false`); `2` invalid
flags (`--image` empty). On exit `1`, prints `Result.Reason` to stderr. On exit `0`, prints the
JSON-encoded `Result` to stdout.

## 6. Behaviour

1. `main.go` parses flags. If `--image` is empty, print `usage: verify-provenance --image <ref> [flags]` to stderr and exit `2`.
2. `main.go` calls `provenance.VerifyImage(ctx, ghBinary, image, owner, repo, branch, repoDir)`.
3. `VerifyImage` runs `exec.CommandContext(ctx, ghBinary, "attestation", "verify", "oci://"+imageRef, "--owner", ownerLogin, "--format", "json")`. If the command cannot start (binary not found), return `ErrGHCLIMissing` wrapped with the underlying `exec.Error`.
4. If the command exits non-zero and stdout is empty, return `ErrAttestationMissing`.
5. Parse stdout as a JSON array; take the first element's `.verificationResult.statement.predicate.buildDefinition.externalParameters.workflow.repository` as the attested repository, and `.verificationResult.statement.predicate.buildDefinition.internalParameters.github.event.head_commit.id` (or, when absent, `.verificationResult.statement.predicate.buildDefinition.resolvedDependencies[0].digest.gitCommit`) as the attested commit SHA. If neither field is present, return `ErrAttestationMissing`.
6. If the attested repository does not equal `wantRepo`, set `Result.Reason` to `attested repository "<got>" does not match "<want>"` and return `(Result{Verified: false, ...}, ErrRepositoryMismatch)`.
7. Run `exec.CommandContext(ctx, "git", "-C", repoDir, "merge-base", "--is-ancestor", commitSHA, wantBranch)`. Exit code `0` means the commit is an ancestor. Any non-zero exit means it is not.
8. If step 7 is not an ancestor, set `Result.Reason` to `commit <sha> is not an ancestor of <branch> in <repoDir>` and return `(Result{Verified: false, ...}, ErrCommitNotOnBranch)`.
9. Otherwise return `(Result{Verified: true, CommitSHA: <sha>, RepositoryRef: <repo>, WorkflowRef: <workflow ref from the same JSON>, CheckedAt: time.Now().UTC()}, nil)`.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| `gh` not on PATH and `--gh-binary` not overridden | return `ErrGHCLIMissing` | `provenance: gh CLI not found or not executable` |
| `gh attestation verify` exits non-zero, empty stdout | return `ErrAttestationMissing` | `provenance: no attestation found for image` |
| Attested repository differs from `--repo` | return `ErrRepositoryMismatch`, `Verified: false` | `provenance: attested repository does not match` |
| Attested commit not an ancestor of `--branch` | return `ErrCommitNotOnBranch`, `Verified: false` | `provenance: attested commit is not an ancestor of the public branch` |
| `main.go` called with `--image ""` | exit 2 | `usage: verify-provenance --image <ref> [flags]` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `VerifyImage` returns `Verified: true` for a fixture attestation whose repository and commit ancestry both match | `TestVerifyImageAcceptsValidAttestation` |
| AC-2 | `VerifyImage` returns `ErrRepositoryMismatch` when the fixture's repository differs from `--repo` | `TestVerifyImageRejectsWrongRepository` |
| AC-3 | `VerifyImage` returns `ErrCommitNotOnBranch` when the attested commit is not an ancestor of the test repo's branch | `TestVerifyImageRejectsCommitNotOnPublicBranch` |
| AC-4 | `VerifyImage` returns `ErrGHCLIMissing` when `ghBinary` does not exist on disk | `TestVerifyImageMissingGHBinary` |
| AC-5 | `.github/workflows/ci.yml`'s `docker-build` job carries `id-token: write` and `attestations: write` permissions | `TestDockerBuildJobHasAttestationPermissions` |
| AC-6 | `.github/workflows/ci.yml`'s `docker-build` job has one step per matrix image using `actions/attest-build-provenance@v2` | `TestDockerBuildJobAttestsProvenance` |
| AC-7 | `verify-provenance` exits `2` when `--image` is empty | `TestMainExitsTwoOnMissingImageFlag` |
| AC-8 | `verify-provenance` exits `1` and prints `Result.Reason` to stderr on a failed verification | `TestMainExitsOneOnFailedVerification` |

## 8. Verification

```bash
# 1. Package tests, including the fake-gh-driven acceptance criteria
go test ./ee/cloud/provenance/... -v -cover
# expect: PASS, TestVerifyImageAcceptsValidAttestation and 3 sibling tests pass

# 2. CI wiring is enforced, not promised
go test ./ee/cloud/provenance/... -run TestDockerBuildJobAttestsProvenance -v
# expect: PASS

# 3. CLI behaviour
go test ./ee/cloud/cmd/verify-provenance/... -v
# expect: PASS

# 4. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All eight acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `make check-boundary` clean
- [ ] `make build-oss && make test-oss` pass with `ee/` deleted
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (a short "Verify Cloud provenance" page linked from the Cloud SLA docs), or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] `.github/workflows/ci.yml` diff touches only the `docker-build` job

## 10. Escalation

| If you find… | Do this |
|---|---|
| `docker/build-push-action@v6`'s output does not expose a `digest` output under the name assumed in §5.1 | Return `SPEC DEFECT: §5.1 — build-push-action output name` |
| The `gh attestation verify --format json` schema does not match §6 step 5's field paths | Return `SPEC DEFECT: §6 — attestation JSON schema` |
| `ee/` is not present when this spec is dispatched | Return `SPEC DEFECT: §2 — GRVX-702 not yet merged` |
| A criterion cannot be met without touching a file outside §4.1/§4.2 | Return `SPEC DEFECT: §4 — needs <path>` |
