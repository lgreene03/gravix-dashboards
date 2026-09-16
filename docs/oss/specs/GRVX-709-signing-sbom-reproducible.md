# SPEC GRVX-709: Sign releases, publish an SBOM, and verify reproducible builds

| Field | Value |
|---|---|
| **Spec ID** | GRVX-709 |
| **Phase** | 7 |
| **Goal** | G1.5, G1.6 |
| **Placement** | `core` (Apache-2.0) |
| **Charter basis** | §7.3 Q3 — supply-chain integrity is a security control, permanently core. A self-hoster must be able to verify what they are running. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701, GRVX-704, GRVX-708 |
| **Blocks** | GRVX-1401, GRVX-1503 |
| **Effort** | 2 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

Make every Gravix release verifiable by a stranger: binaries and container images signed with
keyless cosign, a CycloneDX SBOM published per release, and a `make verify-reproducible` target
proving the same source produces the same binary digest twice.

## 2. Context the implementer needs

- `.github/workflows/release.yml` exists and is the release workflow. Read it in full before editing.
- `Makefile` has `build-oss`, `test-oss`, `check-boundary` (GRVX-704).
- Buildable binaries per `CLAUDE.md`: `./services/ingestion/`, `./transforms/request_metrics_minute/`,
  `./cmd/load_generator/`, `./transforms/compaction/`.
- Go module path `github.com/lgreene/gravix-dashboards`; Go version from `go.mod:3`.
- Go builds are reproducible only when `-trimpath` is set and `-ldflags` embeds no timestamp or
  absolute path. Any `-X main.buildTime=$(date)` pattern makes reproducibility impossible.
- `SECURITY.md` exists (GRVX-708) and must gain a verification section.
- Two compiled binaries are checked into the repository: `cli` (8.7 MB) and `service_events_detail`
  (27 MB). Checked-in binaries cannot be verified against source and are a supply-chain hazard.

## 3. Non-goals for this spec

- Do NOT introduce a long-lived signing key. Use keyless cosign with GitHub OIDC; there is no secret to leak or rotate.
- Do NOT sign historical releases. Signing starts at the next tag.
- Do NOT change what is built or how the product behaves.
- Do NOT delete the checked-in binaries in this spec. Report them as a finding; deletion is its own change.
- Do NOT add dependency-vulnerability scanning. That belongs to Loop L7-sec.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `scripts/verify_reproducible.sh` | Builds every binary twice and compares digests |
| `scripts/generate_sbom.sh` | Produces a CycloneDX SBOM |
| `docs/verifying-releases.md` | How a user verifies a signature and reads the SBOM |

### 4.2 Files to modify

| Path | Change |
|---|---|
| `.github/workflows/release.yml` | Add signing, SBOM generation and attachment. Preserve every existing step. |
| `Makefile` | Add `verify-reproducible` and `sbom` targets plus `.PHONY` entries. Change no existing target. |
| `SECURITY.md` | Append a `## Verifying a release` section linking `docs/verifying-releases.md` |

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `cli`, `service_events_detail` | Checked-in binaries; report them, do not remove them here |
| `.github/workflows/ci.yml` | Release concerns only |
| Any product source | This spec changes build and release plumbing only |

## 5. Interface contract

### 5.1 `scripts/verify_reproducible.sh`

```bash
#!/usr/bin/env bash
# Builds every Gravix binary twice into separate directories and compares SHA-256 digests.
# Usage: ./scripts/verify_reproducible.sh
set -euo pipefail
```

Behaviour:
1. Define the binary set exactly: `ingestion` from `./services/ingestion/`, `rollup` from
   `./transforms/request_metrics_minute/`, `compaction` from `./transforms/compaction/`,
   `load-generator` from `./cmd/load_generator/`, `gravix` from `./cmd/cli/`.
2. Build each twice, into `$TMP/a` and `$TMP/b`, with exactly:
   `CGO_ENABLED=0 GOFLAGS=-trimpath go build -ldflags="-s -w -buildid=" -o <out> <pkg>`
3. Compare `sha256sum` of each pair.
4. Print one line per binary: `<name>: <digest> reproducible` or `<name>: MISMATCH <digestA> != <digestB>`.
5. Print `reproducible: <n>/<total>` and exit 0 only when all match; exit 1 otherwise.

Exit codes: `0` all reproducible; `1` any mismatch or build failure.

### 5.2 `scripts/generate_sbom.sh`

```bash
#!/usr/bin/env bash
# Generates a CycloneDX SBOM for the Gravix core.
# Usage: ./scripts/generate_sbom.sh <output-path>
set -euo pipefail
```

Behaviour: require exactly one argument, else exit 2 with `usage: generate_sbom.sh <output-path>`.
Run `cyclonedx-gomod mod -json -licenses -output <output-path> .`. If `cyclonedx-gomod` is not on
`PATH`, exit 1 with `cyclonedx-gomod not found: install with 'go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest'`.
Then assert the output parses as JSON and contains a non-empty `components` array; if not, exit 1
with `sbom generation produced no components`.

### 5.3 Makefile targets

```make
.PHONY: verify-reproducible sbom

verify-reproducible: ## Build every binary twice and compare digests
	./scripts/verify_reproducible.sh

sbom: ## Generate a CycloneDX SBOM at sbom.json
	./scripts/generate_sbom.sh sbom.json
```

### 5.4 Release workflow additions

Add to `.github/workflows/release.yml`, preserving all existing steps:

- Job-level `permissions:` must include `id-token: write` (required for keyless signing) and
  `contents: write` (required to attach assets).
- After the existing build step, in this order:
  1. `sigstore/cosign-installer@v3`
  2. Generate `sbom.json` via `make sbom`
  3. Produce `checksums.txt` containing the SHA-256 of every release binary
  4. `cosign sign-blob --yes` each binary and `checksums.txt`, writing `<file>.sig` and `<file>.pem`
  5. For each container image: `cosign sign --yes <image>@<digest>` — sign by **digest**, never by tag
  6. Attach every binary, `checksums.txt`, all `.sig` and `.pem` files, and `sbom.json` to the release
- The signing steps must not be `continue-on-error`. An unsigned release is not a release.

### 5.5 `docs/verifying-releases.md` — required sections

1. `## Verify a binary` — the exact command:
   ```bash
   cosign verify-blob \
     --certificate-identity-regexp 'https://github.com/lgreene03/gravix-dashboards/.*' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     --certificate <file>.pem --signature <file>.sig <file>
   ```
2. `## Verify a container image` — the equivalent `cosign verify` command using the image digest.
3. `## Verify the checksums` — `sha256sum -c checksums.txt`.
4. `## Read the SBOM` — what CycloneDX is, and a `jq` command listing components and licences.
5. `## Reproduce the build yourself` — `make verify-reproducible`, and the statement that a
   mismatch is a bug worth reporting through `SECURITY.md`.
6. `## What signing does and does not prove` — verbatim:
   `A signature proves this artefact was built by this repository's release workflow from a specific commit. It does not prove the code is free of vulnerabilities. Both matter; they are different questions.`

## 6. Behaviour

1. Read `.github/workflows/release.yml` in full; list its existing steps in the report.
2. Write `scripts/verify_reproducible.sh` per §5.1 and `chmod +x`.
3. Write `scripts/generate_sbom.sh` per §5.2 and `chmod +x`.
4. Add the two `Makefile` targets from §5.3 without altering existing targets.
5. Add the §5.4 release steps, preserving all existing steps and their order.
6. Write `docs/verifying-releases.md` with all six §5.5 sections.
7. Append `## Verifying a release` to `SECURITY.md`, changing nothing else in that file.
8. Run `make verify-reproducible` locally and record the output.
9. Grep the repository for any `-ldflags` containing `date`, `$(date)`, or `buildTime`. Each hit
   breaks reproducibility — list every one in the report; if any exists in a path this spec builds,
   return a `SPEC DEFECT`.
10. Record the presence and sizes of the checked-in `cli` and `service_events_detail` binaries as a
    supply-chain finding for `security-engineer`, with the recommendation that they be removed and
    built from source.

### 6.1 Failure modes

| Trigger | Behaviour | Exact message |
|---|---|---|
| Any binary is not reproducible | exit 1 | `<name>: MISMATCH <digestA> != <digestB>` |
| `cyclonedx-gomod` absent | exit 1 | `cyclonedx-gomod not found: install with 'go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest'` |
| SBOM has no components | exit 1 | `sbom generation produced no components` |
| `generate_sbom.sh` given no argument | exit 2 | `usage: generate_sbom.sh <output-path>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `make verify-reproducible` reports every binary reproducible and exits 0 | `TestAllBinariesReproducible` |
| AC-2 | `make sbom` produces valid CycloneDX JSON with a non-empty `components` array | `TestSBOMIsValidCycloneDX` |
| AC-3 | `generate_sbom.sh` exits 2 with no argument | `TestSBOMScriptRequiresOutputPath` |
| AC-4 | `release.yml` grants `id-token: write` | `TestReleaseHasOIDCPermission` |
| AC-5 | `release.yml` signs every binary and `checksums.txt` | `TestReleaseSignsAllArtefacts` |
| AC-6 | `release.yml` signs images by digest, never by tag | `TestReleaseSignsByDigest` |
| AC-7 | No signing step is `continue-on-error` | `TestSigningIsMandatory` |
| AC-8 | Every pre-existing `release.yml` step is still present | `TestReleaseWorkflowStepsPreserved` |
| AC-9 | `docs/verifying-releases.md` contains all six §5.5 sections | `TestVerifyingReleasesComplete` |
| AC-10 | No `-ldflags` in a built package embeds a timestamp | `TestNoTimestampInLdflags` |
| AC-11 | `SECURITY.md` gains a `## Verifying a release` section and nothing else changed | `TestSecurityGainsVerificationSection` |

## 8. Verification

```bash
# 1. Reproducibility
make verify-reproducible
# expect: one "reproducible" line per binary, then "reproducible: 5/5", exit 0

# 2. SBOM
make sbom && jq '.components | length' sbom.json
# expect: a number > 0
jq -e '.bomFormat == "CycloneDX"' sbom.json
# expect: true

# 3. Argument handling
./scripts/generate_sbom.sh ; echo "exit=$?"
# expect: usage message, exit=2

# 4. Workflow properties
grep -c "id-token: write" .github/workflows/release.yml
# expect: >= 1
grep -c "cosign sign" .github/workflows/release.yml
# expect: >= 2
grep -c "continue-on-error" .github/workflows/release.yml || true
# expect: 0

# 5. No timestamps in ldflags for built packages
grep -rn "ldflags" Makefile .github/workflows/release.yml | grep -i "date\|buildTime" | grep -c . || true
# expect: 0

# 6. Tests
go test ./tests/... -run 'TestReproducible|TestSBOM|TestRelease|TestSigning|TestVerifyingReleases|TestNoTimestamp|TestSecurityGains' -v
# expect: PASS

# 7. Open-core integrity
make check-boundary && make build-oss && make test-oss
# expect: boundary: 0 violations; both builds succeed
```

## 9. Definition of done

- [ ] All eleven acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] Existing `release.yml` steps enumerated before and after, proving none were lost
- [ ] Every `-ldflags` timestamp hit listed in the report
- [ ] Checked-in `cli` and `service_events_detail` binaries recorded as a finding for `security-engineer`
- [ ] No file outside §4.1/§4.2 modified
- [ ] `docs-engineer` delta merged (README links `docs/verifying-releases.md`)
- [ ] Zero new skipped tests

## 10. Escalation

| If you find… | Do this |
|---|---|
| A binary that cannot be made reproducible | Return `SPEC DEFECT: §5.1 — <name> non-reproducible because <cause>`. Do not remove it from the set to make the target pass. |
| `release.yml` has no tag trigger | Return `SPEC DEFECT: §5.4 — release.yml lacks a tag trigger` |
| `-ldflags` embedding a timestamp in a package this spec builds | Return `SPEC DEFECT: §6 step 9 — <path>` |
