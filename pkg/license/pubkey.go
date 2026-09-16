// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
)

// publicKeyB64 is the Ed25519 public key every Gravix binary verifies licences
// against. It is compiled in rather than fetched, because charter §7.5 says
// verification is an offline signature check with no network call, ever — and a
// key that arrives over the network is a key an attacker can substitute.
//
// This is the DEVELOPMENT key. It signs the fixtures in testdata/ and nothing
// else; see testdata/DEV_KEYS.md. Rotating to the production key is a one-line
// change here, and nothing in this package or its callers changes with it.
const publicKeyB64 = "eC8RyNc/BtqKFI4emyalv6viPlclN3qgr/Hj9hnX7Sg="

var publicKey = mustDecodePublicKey(publicKeyB64)

// mustDecodePublicKey panics if the compiled-in constant above is not a valid
// 32-byte Ed25519 public key.
//
// It can only panic as a result of a hand-edit to the constant; it never depends
// on runtime input. A malformed key would otherwise make every licence check
// fail identically to an invalid licence, which is the one failure an operator
// would never think to look for.
func mustDecodePublicKey(b64 string) ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		panic(fmt.Sprintf("license: compiled-in public key is not valid base64: %v", err))
	}
	if len(raw) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("license: compiled-in public key is %d bytes, want %d",
			len(raw), ed25519.PublicKeySize))
	}
	return ed25519.PublicKey(raw)
}
