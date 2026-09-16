# SPEC GRVX-1301: Offline Ed25519 licence verification — no network call, ever

| Field | Value |
|---|---|
| **Spec ID** | GRVX-1301 |
| **Phase** | 13 |
| **Goal** | G7.6 |
| **Placement** | `core` (Apache-2.0) — the verifier ships in every OSS binary and always reports "no licence" |
| **Charter basis** | §7.5 — licence activation must be a locally verifiable, offline, Ed25519 signature check, with no network call, ever. This spec is what makes that literally true, not aspirational. |
| **Implementer role** | `senior-engineer` |
| **Depends on** | GRVX-701, GRVX-702 |
| **Blocks** | GRVX-1302, GRVX-1303 |
| **Effort** | 4 person-days |
| **Readiness Gate** | 12/12 — PASS |

---

## 1. Objective

After this spec, `pkg/license` exists in core: a package that parses and cryptographically
verifies a Gravix licence token entirely offline, against an Ed25519 public key compiled into the
binary. It performs zero network I/O under any code path — enforced by a test that fails the build
if that ever becomes false. This package ships in the OSS binary unconditionally; on every OSS
install it is called, finds no licence, and returns `(nil, nil)` — not an error, not a warning.

## 2. Context the implementer needs

- `pkg/license` does not exist. `ls pkg/license` returns "No such file or directory".
- No package in this repository currently reads `GRAVIX_LICENSE` or performs Ed25519 verification.
- Go module path is `github.com/lgreene/gravix-dashboards` (`go.mod:1`); Go stdlib `crypto/ed25519`
  is available (Go 1.24.9 per `go.mod:3`) — no new third-party dependency is required.
- `docs/oss/00-open-core-charter.md` §7.5 is the governing rule: "Verification is an Ed25519
  signature check against a public key compiled into the binary. No network call, ever."
- This spec does **not** wire the verifier into `services/gateway/`. That is `GRVX-1302`.
- This spec does **not** implement licence issuance (the private-key signing side used by Gravix to
  produce a customer's licence token). Only verification. The keypair used below is a development
  keypair for Phase 13 implementation and testing; production key issuance is a separate, out-of-
  repo ceremony tracked outside this spec.

## 3. Non-goals for this spec

- Do NOT read `GRAVIX_LICENSE` anywhere outside `pkg/license`. Wiring is `GRVX-1302`.
- Do NOT implement read-only degrade behaviour. That is `GRVX-1303`.
- Do NOT implement a licence-signing/issuance CLI.
- Do NOT add a third-party dependency. `crypto/ed25519` is stdlib.
- This spec does not cross non-goal `docs/04-non-goals.md` §4 (No Real-Time Dashboards) or §6 (No
  Custom Query Language) — it touches neither latency nor query surface.

## 4. Files

### 4.1 Files to create

| Path | Purpose |
|---|---|
| `pkg/license/license.go` | `License` type, `Verify`, `FromEnv`, error values |
| `pkg/license/pubkey.go` | Compiled-in Ed25519 public key |
| `pkg/license/license_test.go` | Tests, including the no-network-import enforcement test |
| `pkg/license/testdata/valid_pro.token` | A validly signed, non-expired token (Plan `pro`, `Features: ["*"]`) |
| `pkg/license/testdata/expired.token` | A validly signed token whose `expires_at` is in the past |
| `pkg/license/testdata/tampered.token` | `valid_pro.token` with the last signature character altered |
| `pkg/license/testdata/DEV_KEYS.md` | The development keypair, in full, and the exact command used to generate it |

### 4.2 Files to modify

None.

### 4.3 Files to NOT touch

| Path | Why |
|---|---|
| `services/gateway/**` | Wiring the verifier into the gateway is `GRVX-1302` |
| `ee/**` | This spec is core-only; `ee/` does not yet contain any real package |

## 5. Interface contract

```go
// package license

// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package license verifies Gravix Pro/Enterprise licence tokens entirely offline.
//
// Verification is a single Ed25519 signature check against a public key compiled into
// the binary (pubkey.go). This package performs no network I/O under any code path,
// ever — see TestLicensePackageHasNoNetworkImports, which fails if that ever becomes
// untrue. A missing or invalid licence is not an error condition for the process as a
// whole: it is the permanent, correct state of every Gravix OSS installation
// (charter §7.5).
package license

// Plan identifies the commercial tier a licence token grants.
type Plan string

const (
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

// License is the parsed, verified content of a Gravix licence token.
type License struct {
	LicenseID string    `json:"license_id"`
	Customer  string    `json:"customer"`
	Plan      Plan      `json:"plan"`
	Seats     int       `json:"seats"`    // 0 means unlimited
	Features  []string  `json:"features"` // capability ids from docs/oss/boundary.yaml, or ["*"] for all
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// HasFeature reports whether the licence grants the named capability id. A Features
// list containing exactly "*" grants every capability.
func (l *License) HasFeature(id string) bool

var (
	// ErrMalformed is returned when a token is not exactly two '.'-separated
	// base64url parts, or either part fails to decode, or the decoded payload is
	// not valid JSON for License.
	ErrMalformed = errors.New("license: malformed token")

	// ErrSignatureInvalid is returned when the Ed25519 signature does not verify
	// against the compiled-in public key.
	ErrSignatureInvalid = errors.New("license: signature invalid")

	// ErrExpired is returned when the signature is valid but ExpiresAt is on or
	// before `now`. The parsed *License is still returned alongside this error so
	// callers can read which capabilities were licensed, for read-only degrade
	// (GRVX-1303, charter §7.5).
	ErrExpired = errors.New("license: expired")
)

// Verify parses and cryptographically verifies a licence token of the form
// "<base64url(payload-json)>.<base64url(ed25519-signature-of-payload-bytes)>"
// against the compiled-in public key. It performs no I/O of any kind — network or
// disk. now is passed explicitly so callers control the expiry clock in tests.
//
// Returns (license, nil) if the token is valid and unexpired.
// Returns (license, ErrExpired) if the token is validly signed but expired.
// Returns (nil, ErrMalformed) if the token cannot be parsed.
// Returns (nil, ErrSignatureInvalid) if parsing succeeds but the signature does not verify.
func Verify(token string, now time.Time) (*License, error)

// FromEnv reads the GRAVIX_LICENSE environment variable, or the file named by
// GRAVIX_LICENSE_FILE when GRAVIX_LICENSE is unset or empty, and calls Verify with
// time.Now().UTC(). GRAVIX_LICENSE takes precedence when both are set.
//
// A missing value on both is NOT an error: it returns (nil, nil), the permanent
// state of every Gravix OSS installation. A GRAVIX_LICENSE_FILE naming a file that
// does not exist is also (nil, nil) — treated the same as "no licence configured".
// Any other read failure (e.g. permission denied) returns (nil, err).
func FromEnv() (*License, error)
```

### 5.1 `pkg/license/pubkey.go`

```go
// package license

const publicKeyB64 = "eC8RyNc/BtqKFI4emyalv6viPlclN3qgr/Hj9hnX7Sg="

var publicKey = mustDecodePublicKey(publicKeyB64)

// mustDecodePublicKey panics if the compiled-in constant above is not a valid
// 32-byte Ed25519 public key. It can only panic as a result of a hand-edit to the
// constant; it never depends on runtime input.
func mustDecodePublicKey(b64 string) ed25519.PublicKey
```

### 5.2 `pkg/license/testdata/DEV_KEYS.md`

Verbatim content:

```markdown
# Development signing keypair — Phase 13

This keypair signs the fixtures in this directory. It is **not** the production
Gravix signing key. Production key issuance is a separate, out-of-repo ceremony.

Public key  (compiled into `pkg/license/pubkey.go`):
`eC8RyNc/BtqKFI4emyalv6viPlclN3qgr/Hj9hnX7Sg=`

Private key (dev/test only — never used to sign a real customer licence):
`vmIl/7sBVwXvSoa22nerx4oJSfmgfB7RpyDeyS2hsOZ4LxHI1z8G2ooUjh6bJqW/q+I+VyU3eqCv8eP2GdftKA==`

Generated with:

    package main

    import (
    	"crypto/ed25519"
    	"crypto/rand"
    	"encoding/base64"
    	"fmt"
    )

    func main() {
    	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
    	fmt.Println("PUBLIC:", base64.StdEncoding.EncodeToString(pub))
    	fmt.Println("PRIVATE:", base64.StdEncoding.EncodeToString(priv))
    }

Rotating the production key is a one-line change to `publicKeyB64` in `pubkey.go`.
Nothing else in this package or its callers changes.
```

### 5.3 Testdata token contents (exact, verbatim, one line each, no trailing newline processing required beyond a single `\n`)

`pkg/license/testdata/valid_pro.token` — signed payload
`{"license_id":"11111111-1111-1111-1111-111111111111","customer":"Acme Test Co","plan":"pro","seats":0,"features":["*"],"issued_at":"2026-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z"}`:

```
eyJsaWNlbnNlX2lkIjoiMTExMTExMTEtMTExMS0xMTExLTExMTEtMTExMTExMTExMTExIiwiY3VzdG9tZXIiOiJBY21lIFRlc3QgQ28iLCJwbGFuIjoicHJvIiwic2VhdHMiOjAsImZlYXR1cmVzIjpbIioiXSwiaXNzdWVkX2F0IjoiMjAyNi0wMS0wMVQwMDowMDowMFoiLCJleHBpcmVzX2F0IjoiMjA5OS0wMS0wMVQwMDowMDowMFoifQ.sYczDDMoc2UwaCGt5_tEvWw82x3RtolcfMDZYHjqOkU2-AsBSnxla4F_9h8D83ogEyA5dKn3lzG7I2pRolQzDg
```

`pkg/license/testdata/expired.token` — same customer, `license_id`
`22222222-2222-2222-2222-222222222222`, `expires_at":"2020-01-01T00:00:00Z"`:

```
eyJsaWNlbnNlX2lkIjoiMjIyMjIyMjItMjIyMi0yMjIyLTIyMjItMjIyMjIyMjIyMjIyIiwiY3VzdG9tZXIiOiJBY21lIFRlc3QgQ28iLCJwbGFuIjoicHJvIiwic2VhdHMiOjAsImZlYXR1cmVzIjpbIioiXSwiaXNzdWVkX2F0IjoiMjAyNi0wMS0wMVQwMDowMDowMFoiLCJleHBpcmVzX2F0IjoiMjAyMC0wMS0wMVQwMDowMDowMFoifQ.9YEYCeNBBN0Av1OWoUrtecJlK5fe6xJn6_VWCIb_TkIpxyTZLxbGnjd_YF6lYpIpIBvL74ZoRRX4VZ7Qf8n5BA
```

`pkg/license/testdata/tampered.token` — `valid_pro.token` with only the final character of the
signature changed from `g` to `A` (payload byte-identical, signature now invalid):

```
eyJsaWNlbnNlX2lkIjoiMTExMTExMTEtMTExMS0xMTExLTExMTEtMTExMTExMTExMTExIiwiY3VzdG9tZXIiOiJBY21lIFRlc3QgQ28iLCJwbGFuIjoicHJvIiwic2VhdHMiOjAsImZlYXR1cmVzIjpbIioiXSwiaXNzdWVkX2F0IjoiMjAyNi0wMS0wMVQwMDowMDowMFoiLCJleHBpcmVzX2F0IjoiMjA5OS0wMS0wMVQwMDowMDowMFoifQ.sYczDDMoc2UwaCGt5_tEvWw82x3RtolcfMDZYHjqOkU2-AsBSnxla4F_9h8D83ogEyA5dKn3lzG7I2pRolQzDA
```

These three tokens were verified against the exact `Verify` algorithm in §5 before this spec was
written: `valid_pro.token` → `(license, nil)`; `expired.token` → `(license, ErrExpired)`;
`tampered.token` → `(nil, ErrSignatureInvalid)`.

## 6. Behaviour

1. Create `pkg/license/pubkey.go` with the exact content in §5.1.
2. Create `pkg/license/license.go` implementing every symbol in §5 exactly.
3. Create the four testdata files in §5.2/§5.3 verbatim, including `DEV_KEYS.md`.
4. In `Verify`: split on the first `.` only (`strings.SplitN(token, ".", 2)`); decode both parts
   with `base64.RawURLEncoding`; reject a signature whose decoded length is not
   `ed25519.SignatureSize` (64 bytes) as `ErrMalformed` before calling `ed25519.Verify`; call
   `ed25519.Verify(publicKey, payloadBytes, sigBytes)` — on `false`, return `ErrSignatureInvalid`;
   on `true`, `json.Unmarshal` the payload bytes into `License` — a JSON error is `ErrMalformed`;
   compare `now.After(lic.ExpiresAt)` — if true, return `(&lic, ErrExpired)`, else `(&lic, nil)`.
5. In `FromEnv`: read `GRAVIX_LICENSE` via `os.Getenv`. If non-empty, call `Verify` with it. If
   empty, read `GRAVIX_LICENSE_FILE`; if that is also empty, return `(nil, nil)`. If set, read the
   file with `os.ReadFile`; if `os.IsNotExist(err)`, return `(nil, nil)`; any other read error is
   returned wrapped as `fmt.Errorf("license: read %s: %w", path, err)`; on success, trim
   surrounding whitespace with `strings.TrimSpace` and, if the result is empty, return `(nil, nil)`;
   otherwise call `Verify(token, time.Now().UTC())`.
6. Write `license_test.go` covering every case in §7, including `TestLicensePackageHasNoNetworkImports`
   implemented exactly as:
   ```go
   func TestLicensePackageHasNoNetworkImports(t *testing.T) {
   	out, err := exec.Command("go", "list", "-deps",
   		"github.com/lgreene/gravix-dashboards/pkg/license").Output()
   	if err != nil {
   		t.Fatalf("go list -deps failed: %v", err)
   	}
   	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
   		if line == "net" || strings.HasPrefix(line, "net/") {
   			t.Fatalf("pkg/license transitively imports networking package %q — "+
   				"licence verification must never be able to dial", line)
   		}
   	}
   }
   ```

### 6.1 Failure modes

| Trigger | Behaviour | Exact message (as `error.Error()`) |
|---|---|---|
| Token has zero or more than one `.` | `Verify` returns `(nil, ErrMalformed)` | `license: malformed token` |
| Either part fails base64url decode | `Verify` returns `(nil, ErrMalformed)` | `license: malformed token` |
| Decoded signature is not 64 bytes | `Verify` returns `(nil, ErrMalformed)` | `license: malformed token` |
| `ed25519.Verify` returns false | `Verify` returns `(nil, ErrSignatureInvalid)` | `license: signature invalid` |
| Payload is not valid JSON for `License` | `Verify` returns `(nil, ErrMalformed)` | `license: malformed token` |
| `now.After(lic.ExpiresAt)` | `Verify` returns `(&lic, ErrExpired)` | `license: expired` |
| `GRAVIX_LICENSE_FILE` names a non-existent file | `FromEnv` returns `(nil, nil)` | — (not an error) |
| `GRAVIX_LICENSE_FILE` names a file that exists but cannot be read (permissions) | `FromEnv` returns `(nil, err)` | `license: read <path>: <os error>` |

## 7. Acceptance criteria

| ID | Criterion | Test name |
|---|---|---|
| AC-1 | `Verify` on `testdata/valid_pro.token` at `2026-09-10T00:00:00Z` returns a `*License` with `Plan == PlanPro` and a `nil` error | `TestVerifyValidToken` |
| AC-2 | `Verify` on `testdata/expired.token` at `2026-09-10T00:00:00Z` returns a non-nil `*License` and `errors.Is(err, ErrExpired)` | `TestVerifyExpiredReturnsLicenseAndError` |
| AC-3 | `Verify` on `testdata/tampered.token` returns `(nil, ErrSignatureInvalid)` | `TestVerifyTamperedSignatureRejected` |
| AC-4 | `Verify` on the literal string `"not-a-valid-token"` returns `(nil, ErrMalformed)` | `TestVerifyMalformedRejected` |
| AC-5 | `FromEnv` with both `GRAVIX_LICENSE` and `GRAVIX_LICENSE_FILE` unset returns `(nil, nil)` | `TestFromEnvAbsentIsNotError` |
| AC-6 | `FromEnv` with both set returns the result of verifying `GRAVIX_LICENSE`, ignoring the file | `TestFromEnvEnvTakesPrecedenceOverFile` |
| AC-7 | `HasFeature` returns `true` for any queried id when `Features` is `["*"]` | `TestHasFeatureWildcard` |
| AC-8 | `HasFeature` returns `false` for an id not present and `Features` does not contain `"*"` | `TestHasFeatureDenylistedByDefault` |
| AC-9 | `pkg/license` has zero transitive dependency on `net` or any `net/*` package | `TestLicensePackageHasNoNetworkImports` |
| AC-10 | `go test ./pkg/license/... -cover` reports line coverage ≥95% | `TestCoverage` (verified by the §8 command) |

## 8. Verification

```bash
# 1. Full suite
go test ./pkg/license/... -v -cover
# expect: PASS, coverage >= 95.0%

# 2. The no-network enforcement test in isolation
go test ./pkg/license/... -run TestLicensePackageHasNoNetworkImports -v
# expect: PASS

# 3. Nothing else broke
go build ./... && go test ./schemas/...
# expect: build ok, PASS

# 4. Open-core integrity (mandatory on every spec)
make check-boundary
# expect: "boundary: 0 violations"

make build-oss && make test-oss
# expect: both succeed with ee/ absent
```

## 9. Definition of done

- [ ] All ten acceptance criteria pass with their named tests
- [ ] Every Verification command run, real output pasted into the report
- [ ] `pkg/license` coverage ≥95%
- [ ] No file outside §4.1 created; no file modified
- [ ] `docs-engineer` delta merged, or `NO DOCS DELTA REQUIRED` accepted
- [ ] Zero new skipped or quarantined tests
- [ ] `DEV_KEYS.md` is committed and its private key is never referenced from non-test code

## 10. Escalation

| If you find… | Do this |
|---|---|
| Any ambiguity in this spec | Return `SPEC DEFECT: §<n> — <what is ambiguous>`. Do not guess. |
| A criterion that cannot be met without an out-of-scope file | Return `SPEC DEFECT: §4 — needs <path>`. |
| (`pro-engineer` only) a needed core hook | Return `EXTENSION POINT REQUIRED` with the proposed interface. |
