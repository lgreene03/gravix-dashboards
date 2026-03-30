package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	key, err := Generate("Gravix", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if key.Secret == "" {
		t.Error("secret should not be empty")
	}
	if key.Issuer != "Gravix" {
		t.Errorf("issuer = %q, want Gravix", key.Issuer)
	}
	if key.Account != "alice@example.com" {
		t.Errorf("account = %q, want alice@example.com", key.Account)
	}
	if key.Digits != 6 {
		t.Errorf("digits = %d, want 6", key.Digits)
	}
	if key.Period != 30 {
		t.Errorf("period = %d, want 30", key.Period)
	}

	// Verify the secret is valid base32
	_, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key.Secret)
	if err != nil {
		t.Errorf("secret is not valid base32: %v", err)
	}
}

func TestKeyURL(t *testing.T) {
	key := &Key{
		Secret:    "JBSWY3DPEHPK3PXP",
		Issuer:    "Gravix",
		Account:   "alice@example.com",
		Algorithm: "SHA1",
		Digits:    6,
		Period:    30,
	}
	url := key.URL()
	if !strings.Contains(url, "otpauth://totp/") {
		t.Error("URL should start with otpauth://totp/")
	}
	if !strings.Contains(url, "secret=JBSWY3DPEHPK3PXP") {
		t.Error("URL should contain secret")
	}
	if !strings.Contains(url, "issuer=Gravix") {
		t.Error("URL should contain issuer")
	}
}

func TestValidate_KnownVector(t *testing.T) {
	// RFC 6238 test vector: secret = "12345678901234567890" (base32: GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ)
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	// At time 59 (counter=1), expected code is "287082"
	testTime := time.Unix(59, 0)
	code := "287082"

	if !ValidateAt(code, secret, testTime) {
		t.Errorf("ValidateAt(%q, secret, t=59) = false, want true", code)
	}
}

func TestValidate_CurrentTime(t *testing.T) {
	key, err := Generate("Gravix", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Generate a valid code for current time
	secretBytes, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key.Secret)
	counter := uint64(time.Now().Unix()) / uint64(DefaultPeriod)
	code := generateCode(secretBytes, counter)

	if !Validate(code, key.Secret) {
		t.Error("Validate should accept code for current time period")
	}
}

func TestValidate_WrongCode(t *testing.T) {
	key, err := Generate("Gravix", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if Validate("000000", key.Secret) {
		// This could theoretically be correct, but is extremely unlikely
		t.Log("got unlikely valid code 000000 (acceptable)")
	}
}

func TestValidate_InvalidSecret(t *testing.T) {
	if Validate("123456", "not-valid-base32!!!") {
		t.Error("should not validate with invalid secret")
	}
}

func TestValidate_Skew(t *testing.T) {
	key, err := Generate("Gravix", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}

	secretBytes, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key.Secret)
	now := time.Now()
	// Generate code for previous period
	prevCounter := uint64(now.Unix())/uint64(DefaultPeriod) - 1
	prevCode := generateCode(secretBytes, prevCounter)

	// Should still validate due to skew=1
	if !ValidateAt(prevCode, key.Secret, now) {
		t.Error("should accept code from previous time period (skew=1)")
	}
}

func TestEncryptDecryptSecret(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	plaintext := "JBSWY3DPEHPK3PXP"
	encrypted, err := EncryptSecret(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == plaintext {
		t.Error("encrypted should differ from plaintext")
	}

	decrypted, err := DecryptSecret(encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != plaintext {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptSecret_WrongKeySize(t *testing.T) {
	_, err := EncryptSecret("test", []byte("short"))
	if err == nil {
		t.Error("should error on wrong key size")
	}
}

func TestDecryptSecret_WrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1

	encrypted, err := EncryptSecret("test", key1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecryptSecret(encrypted, key2)
	if err == nil {
		t.Error("should error with wrong key")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := RecoveryCodes(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 8 {
		t.Errorf("got %d codes, want 8", len(codes))
	}

	// Check format: xxxx-xxxx
	for _, code := range codes {
		if len(code) != 9 || code[4] != '-' {
			t.Errorf("code %q not in xxxx-xxxx format", code)
		}
	}

	// Check uniqueness
	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] {
			t.Errorf("duplicate code: %s", code)
		}
		seen[code] = true
	}
}
