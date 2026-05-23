// Package totp provides TOTP two-factor authentication with encrypted secret storage.
package totp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const (
	// DefaultPeriod is the TOTP time step in seconds (RFC 6238).
	DefaultPeriod = 30
	// DefaultDigits is the number of digits in a TOTP code.
	DefaultDigits = 6
	// DefaultSkew allows codes from adjacent time periods.
	DefaultSkew = 1
)

// Key wraps a TOTP secret with metadata for QR code generation.
type Key struct {
	Secret    string // base32-encoded secret
	Issuer    string
	Account   string // user email
	Algorithm string // SHA1
	Digits    int
	Period    int
}

// URL returns the otpauth:// URL for QR code generation.
func (k *Key) URL() string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=%s&digits=%d&period=%d",
		k.Issuer, k.Account, k.Secret, k.Issuer, k.Algorithm, k.Digits, k.Period)
}

// Generate creates a new TOTP key for a user.
func Generate(issuer, account string) (*Key, error) {
	secret := make([]byte, 20) // 160 bits
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, fmt.Errorf("totp: failed to generate secret: %w", err)
	}

	return &Key{
		Secret:    base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret),
		Issuer:    issuer,
		Account:   account,
		Algorithm: "SHA1",
		Digits:    DefaultDigits,
		Period:    DefaultPeriod,
	}, nil
}

// Validate checks a TOTP code against a base32-encoded secret.
// It allows codes from the current and adjacent time periods (skew=1).
func Validate(code, secret string) bool {
	return ValidateAt(code, secret, time.Now())
}

// ValidateAt checks a TOTP code against a secret at a specific time.
func ValidateAt(code, secret string, t time.Time) bool {
	secretBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		strings.TrimRight(strings.ToUpper(secret), "="),
	)
	if err != nil {
		return false
	}

	counter := uint64(t.Unix()) / uint64(DefaultPeriod)

	for i := -DefaultSkew; i <= DefaultSkew; i++ {
		c := counter
		if i < 0 {
			c = counter - uint64(-i)
		} else {
			c = counter + uint64(i)
		}
		expected := generateCode(secretBytes, c)
		if code == expected {
			return true
		}
	}
	return false
}

// generateCode generates a TOTP code for a given counter value using HMAC-SHA1.
func generateCode(secret []byte, counter uint64) string {
	// Convert counter to big-endian bytes
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	// HMAC-SHA1
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	hash := mac.Sum(nil)

	// Dynamic truncation (RFC 4226)
	offset := hash[len(hash)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff

	// Generate digits
	code := truncated % uint32(math.Pow10(DefaultDigits))
	return fmt.Sprintf("%06d", code)
}

// EncryptSecret encrypts a TOTP secret using AES-256-GCM.
// The encryption key must be exactly 32 bytes.
func EncryptSecret(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("totp: encryption key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("totp: failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("totp: failed to create GCM: %w", err)
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("totp: failed to generate nonce: %w", err)
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptSecret decrypts a TOTP secret encrypted with EncryptSecret.
func DecryptSecret(encrypted string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("totp: encryption key must be 32 bytes, got %d", len(key))
	}

	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("totp: failed to decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("totp: failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("totp: failed to create GCM: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("totp: ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("totp: decryption failed: %w", err)
	}

	return string(plaintext), nil
}

// RecoveryCodes generates a set of one-time recovery codes.
func RecoveryCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		b := make([]byte, 4)
		if _, err := io.ReadFull(rand.Reader, b); err != nil {
			return nil, fmt.Errorf("totp: failed to generate recovery code: %w", err)
		}
		codes[i] = fmt.Sprintf("%04x-%04x", binary.BigEndian.Uint16(b[:2]), binary.BigEndian.Uint16(b[2:]))
	}
	return codes, nil
}
