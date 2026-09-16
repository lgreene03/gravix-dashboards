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
