// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package license verifies Gravix Pro/Enterprise licence tokens entirely offline.
//
// Verification is a single Ed25519 signature check against a public key compiled
// into the binary (pubkey.go). This package performs no network I/O under any
// code path, ever — see TestLicensePackageHasNoNetworkImports, which fails if
// that ever becomes untrue. A missing or invalid licence is not an error
// condition for the process as a whole: it is the permanent, correct state of
// every Gravix OSS installation (charter §7.5).
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

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

// FeatureWildcard in Features grants every capability.
const FeatureWildcard = "*"

// HasFeature reports whether the licence grants the named capability id. A
// Features list containing exactly "*" grants every capability.
//
// A nil receiver reports false for everything, so an unlicensed install — which
// is every OSS install — needs no nil check at the call site. That is the whole
// point: the free tier is the default path, not the exception path.
func (l *License) HasFeature(id string) bool {
	if l == nil {
		return false
	}
	for _, f := range l.Features {
		if f == FeatureWildcard || f == id {
			return true
		}
	}
	return false
}

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

// Environment variables a licence may arrive in.
const (
	EnvLicense     = "GRAVIX_LICENSE"
	EnvLicenseFile = "GRAVIX_LICENSE_FILE"
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
func Verify(token string, now time.Time) (*License, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || strings.Contains(parts[1], ".") {
		return nil, ErrMalformed
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	// Checked before ed25519.Verify: that function returns false for a
	// wrong-length signature, and reporting a 3-byte signature as "invalid" would
	// send somebody looking for a tampering attempt rather than a truncated copy
	// and paste.
	if len(sig) != ed25519.SignatureSize {
		return nil, ErrMalformed
	}

	// The signature is checked BEFORE the payload is parsed. Unmarshalling first
	// would mean running a parser over bytes nobody has authenticated.
	if !ed25519.Verify(publicKey, payload, sig) {
		return nil, ErrSignatureInvalid
	}

	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, ErrMalformed
	}

	if now.After(lic.ExpiresAt) {
		// The licence comes back WITH the error. A caller degrading to read-only
		// needs to know what was licensed in order to say what stopped working,
		// and "your licence expired" with no detail is a support ticket.
		return &lic, ErrExpired
	}
	return &lic, nil
}

// FromEnv reads GRAVIX_LICENSE, or the file named by GRAVIX_LICENSE_FILE when
// GRAVIX_LICENSE is unset or empty, and calls Verify with time.Now().UTC().
// GRAVIX_LICENSE takes precedence when both are set.
//
// A missing value on both is NOT an error: it returns (nil, nil), the permanent
// state of every Gravix OSS installation. A GRAVIX_LICENSE_FILE naming a file
// that does not exist is also (nil, nil) — treated the same as "no licence
// configured". Any other read failure (e.g. permission denied) returns (nil, err).
func FromEnv() (*License, error) {
	if token := strings.TrimSpace(os.Getenv(EnvLicense)); token != "" {
		return Verify(token, time.Now().UTC())
	}

	path := strings.TrimSpace(os.Getenv(EnvLicenseFile))
	if path == "" {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Not an error. An operator who has not put a licence there yet is in
		// exactly the same position as one who never will be.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("license: read %s: %w", path, err)
	}

	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, nil
	}
	return Verify(token, time.Now().UTC())
}
