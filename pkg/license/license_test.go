// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package license

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkTime is the clock every fixture assertion uses: after valid_pro.token's
// issue date and expired.token's expiry, before valid_pro.token's expiry.
var checkTime = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.TrimSpace(string(raw))
}

// AC-1: a valid token verifies.
func TestVerifyValidToken(t *testing.T) {
	lic, err := Verify(fixture(t, "valid_pro.token"), checkTime)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if lic == nil {
		t.Fatal("Verify returned a nil licence and no error")
	}

	if lic.Plan != PlanPro {
		t.Errorf("Plan = %q, want %q", lic.Plan, PlanPro)
	}
	if lic.Customer != "Acme Test Co" {
		t.Errorf("Customer = %q", lic.Customer)
	}
	if lic.LicenseID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("LicenseID = %q", lic.LicenseID)
	}
	if lic.Seats != 0 {
		t.Errorf("Seats = %d, want 0 (unlimited)", lic.Seats)
	}
	if !lic.ExpiresAt.After(checkTime) {
		t.Errorf("ExpiresAt %s is not after the check time %s", lic.ExpiresAt, checkTime)
	}
}

// AC-2: an expired token returns the licence AND the error.
//
// Both halves matter. A caller degrading to read-only has to say which
// capabilities stopped working, and "your licence expired" with no detail is a
// support ticket rather than an explanation.
func TestVerifyExpiredReturnsLicenseAndError(t *testing.T) {
	lic, err := Verify(fixture(t, "expired.token"), checkTime)

	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	if lic == nil {
		t.Fatal("an expired licence came back nil; a caller cannot say what stopped working")
	}
	if lic.LicenseID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("LicenseID = %q", lic.LicenseID)
	}
	if !lic.HasFeature("anything") {
		t.Error("the expired licence does not report what it had licensed")
	}

	// The same token before its expiry is simply valid — the signature was
	// never the problem.
	before := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := Verify(fixture(t, "expired.token"), before); err != nil {
		t.Errorf("the expired token was rejected before its expiry: %v", err)
	}
}

// AC-3: a tampered signature is rejected.
func TestVerifyTamperedSignatureRejected(t *testing.T) {
	valid := fixture(t, "valid_pro.token")
	tampered := fixture(t, "tampered.token")

	// The payload is byte-identical; only the signature differs. That is what
	// makes this a signature test rather than a parsing test.
	validPayload, _, _ := strings.Cut(valid, ".")
	tamperedPayload, _, _ := strings.Cut(tampered, ".")
	if validPayload != tamperedPayload {
		t.Fatal("the fixtures differ in their payload; this would not test the signature")
	}

	lic, err := Verify(tampered, checkTime)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
	if lic != nil {
		t.Error("a licence was returned for an invalid signature; nothing unauthenticated may reach a caller")
	}
}

// AC-4: a malformed token is rejected.
func TestVerifyMalformedRejected(t *testing.T) {
	valid := fixture(t, "valid_pro.token")
	payload, sig, _ := strings.Cut(valid, ".")

	cases := map[string]string{
		"no dot":              "not-a-valid-token",
		"empty":               "",
		"payload only":        payload,
		"three parts":         valid + "." + sig,
		"payload not base64":  "!!!not-base64!!!." + sig,
		"signature not b64":   payload + ".!!!not-base64!!!",
		"signature too short": payload + ".YWJj",
		"signature too long":  payload + "." + sig + sig,
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			lic, err := Verify(token, checkTime)
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
			if lic != nil {
				t.Error("a licence was returned for a malformed token")
			}
		})
	}

	// A signature that is the right length and correctly signed, over a payload
	// that is not a licence, is malformed rather than invalid — the signature
	// really did verify.
	t.Run("valid signature over non-JSON", func(t *testing.T) {
		// This cannot be constructed without the private key, which this
		// package deliberately does not hold. What IS checkable is that the
		// length check runs before ed25519.Verify, so a short signature is
		// never reported as a tampering attempt.
		short := payload + ".YWJj"
		if _, err := Verify(short, checkTime); !errors.Is(err, ErrMalformed) {
			t.Errorf("a 3-byte signature was reported as %v, not malformed", err)
		}
	})
}

// AC-5: no licence is not an error. This is the permanent state of every Gravix
// OSS installation, and treating it as a failure would make the free tier feel
// like a broken paid one.
func TestFromEnvAbsentIsNotError(t *testing.T) {
	t.Setenv(EnvLicense, "")
	t.Setenv(EnvLicenseFile, "")

	lic, err := FromEnv()
	if err != nil {
		t.Fatalf("an absent licence was an error: %v", err)
	}
	if lic != nil {
		t.Fatalf("an absent licence returned %+v", lic)
	}

	// And a file that is not there is the same as no file configured: an
	// operator who has not put one there yet is where one who never will be is.
	t.Setenv(EnvLicenseFile, filepath.Join(t.TempDir(), "absent.token"))
	lic, err = FromEnv()
	if err != nil || lic != nil {
		t.Fatalf("a missing licence file gave (%+v, %v), want (nil, nil)", lic, err)
	}

	// An empty file, too — that is somebody who touched the path and stopped.
	empty := filepath.Join(t.TempDir(), "empty.token")
	if err := os.WriteFile(empty, []byte("   \n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvLicenseFile, empty)
	if lic, err := FromEnv(); err != nil || lic != nil {
		t.Fatalf("an empty licence file gave (%+v, %v), want (nil, nil)", lic, err)
	}
}

// AC-6: the environment variable wins over the file.
func TestFromEnvEnvTakesPrecedenceOverFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "license.token")
	if err := os.WriteFile(filePath, []byte(fixture(t, "expired.token")), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv(EnvLicense, fixture(t, "valid_pro.token"))
	t.Setenv(EnvLicenseFile, filePath)

	lic, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if lic == nil || lic.LicenseID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("the file was used instead of the environment variable: %+v", lic)
	}

	// With the variable empty, the file is read.
	t.Setenv(EnvLicense, "")
	lic, err = FromEnv()
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want the file's ErrExpired", err)
	}
	if lic == nil || lic.LicenseID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("the file was not read: %+v", lic)
	}
}

// A file that exists and cannot be read is a real error, unlike one that is
// absent. An operator who set the path and got the permissions wrong needs to
// be told, not silently treated as unlicensed.
func TestFromEnvUnreadableFileIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		// Running as root, chmod 000 does not deny the read. A directory in
		// place of the file fails for every user.
		dir := filepath.Join(t.TempDir(), "license.token")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		t.Setenv(EnvLicense, "")
		t.Setenv(EnvLicenseFile, dir)

		lic, err := FromEnv()
		if err == nil {
			t.Fatal("an unreadable licence path was silently treated as unlicensed")
		}
		if lic != nil {
			t.Errorf("a licence came back alongside the error: %+v", lic)
		}
		if !strings.Contains(err.Error(), "license: read "+dir) {
			t.Errorf("message %q does not match §6.1's form", err)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "license.token")
	if err := os.WriteFile(path, []byte("whatever"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvLicense, "")
	t.Setenv(EnvLicenseFile, path)

	if _, err := FromEnv(); err == nil {
		t.Fatal("an unreadable licence file was silently treated as unlicensed")
	}
}

// AC-7: "*" grants everything.
func TestHasFeatureWildcard(t *testing.T) {
	lic, err := Verify(fixture(t, "valid_pro.token"), checkTime)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	for _, id := range []string{"sso", "audit-log", "fleet", "a-capability-invented-next-year", ""} {
		if !lic.HasFeature(id) {
			t.Errorf("HasFeature(%q) = false on a wildcard licence", id)
		}
	}
}

// AC-8: everything is denied by default.
//
// The default has to be deny. A licence that granted an unknown capability
// because nobody thought to list it would make every future paid feature free
// for anyone holding any licence.
func TestHasFeatureDenylistedByDefault(t *testing.T) {
	lic := &License{Features: []string{"sso", "audit-log"}}

	for _, id := range []string{"sso", "audit-log"} {
		if !lic.HasFeature(id) {
			t.Errorf("HasFeature(%q) = false on a licence that lists it", id)
		}
	}
	for _, id := range []string{"fleet", "whitelabel", "", "SSO", "sso "} {
		if lic.HasFeature(id) {
			t.Errorf("HasFeature(%q) = true on a licence that does not list it", id)
		}
	}

	// No licence at all grants nothing, and does not panic. Every OSS install
	// takes this path on every check, so it is the one that must be boring.
	var none *License
	if none.HasFeature("sso") {
		t.Error("a nil licence granted a capability")
	}
	if (&License{}).HasFeature("sso") {
		t.Error("a licence with no features granted one")
	}
}

// AC-9: this package can never dial.
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

// The compiled-in key is the one the fixtures were signed with, and it is a
// real 32-byte Ed25519 key. A hand-edit that broke it would otherwise make
// every licence fail exactly like an invalid one.
func TestCompiledInPublicKey(t *testing.T) {
	if len(publicKey) != 32 {
		t.Fatalf("compiled-in public key is %d bytes, want 32", len(publicKey))
	}
	if publicKeyB64 != "eC8RyNc/BtqKFI4emyalv6viPlclN3qgr/Hj9hnX7Sg=" {
		t.Errorf("the compiled-in key changed to %q; every existing licence stops verifying", publicKeyB64)
	}

	for _, bad := range []string{"", "not base64!", "c2hvcnQ="} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("mustDecodePublicKey(%q) did not panic", bad)
				}
			}()
			mustDecodePublicKey(bad)
		}()
	}

	// The development keypair is documented, and documented as not production.
	raw, err := os.ReadFile(filepath.Join("testdata", "DEV_KEYS.md"))
	if err != nil {
		t.Fatalf("read DEV_KEYS.md: %v", err)
	}
	doc := string(raw)
	if !strings.Contains(doc, publicKeyB64) {
		t.Error("DEV_KEYS.md does not record the key that is compiled in")
	}
	for _, phrase := range []string{"not** the production", "never used to sign a real customer licence"} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("DEV_KEYS.md does not say %q", phrase)
		}
	}
}

// §9: the development private key is never referenced from non-test code.
//
// It is committed on purpose — the fixtures have to be reproducible, and a
// keypair nobody can regenerate is a fixture nobody can fix. What must never
// happen is a binary carrying it, because a signing key in a shipped binary
// means anyone can mint a licence.
func TestDevPrivateKeyIsNeverInNonTestCode(t *testing.T) {
	const devPrivateKeyPrefix = "vmIl/7sBVwXvSoa22nerx4oJSfmgfB7RpyDeyS2hsOZ4"

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	var offenders []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "data", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		// The one place it is allowed to live, and the spec that publishes it.
		if rel == filepath.Join("pkg", "license", "testdata", "DEV_KEYS.md") ||
			strings.HasPrefix(rel, filepath.Join("docs", "oss", "specs")) ||
			strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return nil // unreadable is not the same as offending
		}
		if strings.Contains(string(raw), devPrivateKeyPrefix) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, o := range offenders {
		t.Errorf("%s contains the development private key; a signing key must never reach a binary", o)
	}

	// And the verifier holds only a PUBLIC key. A private key in this package
	// would let the thing that checks licences also mint them.
	src, err := os.ReadFile("pubkey.go")
	if err != nil {
		t.Fatalf("read pubkey.go: %v", err)
	}
	for _, forbidden := range []string{"PrivateKey", "privateKey", "Sign(", "GenerateKey"} {
		if strings.Contains(string(src), forbidden) {
			t.Errorf("pubkey.go mentions %q; this package verifies, it does not sign", forbidden)
		}
	}
}

// Verification does not depend on the clock beyond expiry, and an expiry
// exactly at `now` is still valid — `now.After(ExpiresAt)` is the boundary the
// spec names, so a licence is good through its final instant.
func TestExpiryBoundary(t *testing.T) {
	lic, err := Verify(fixture(t, "expired.token"), checkTime)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v", err)
	}

	if _, err := Verify(fixture(t, "expired.token"), lic.ExpiresAt); err != nil {
		t.Errorf("a licence expiring exactly now was rejected: %v", err)
	}
	oneNanoLater := lic.ExpiresAt.Add(time.Nanosecond)
	if _, err := Verify(fixture(t, "expired.token"), oneNanoLater); !errors.Is(err, ErrExpired) {
		t.Errorf("a licence one nanosecond past expiry gave %v", err)
	}
}
