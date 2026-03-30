// Package captcha provides CAPTCHA verification for protecting registration and
// other abuse-prone endpoints. Supports reCAPTCHA v3, hCaptcha, and a no-op verifier
// for development/testing.
package captcha

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Verifier validates CAPTCHA tokens from the client.
type Verifier interface {
	// Verify checks a CAPTCHA response token. Returns nil if valid.
	Verify(token string, remoteIP string) error
}

// NewVerifierFromEnv creates a Verifier based on environment variables:
//   - CAPTCHA_PROVIDER=recaptcha → RecaptchaV3Verifier (requires RECAPTCHA_SECRET_KEY, optional RECAPTCHA_MIN_SCORE)
//   - CAPTCHA_PROVIDER=hcaptcha  → HCaptchaVerifier (requires HCAPTCHA_SECRET_KEY)
//   - CAPTCHA_PROVIDER=""        → NoopVerifier (always passes)
func NewVerifierFromEnv() Verifier {
	provider := os.Getenv("CAPTCHA_PROVIDER")
	switch provider {
	case "recaptcha":
		minScore := 0.5
		return &RecaptchaV3Verifier{
			SecretKey: os.Getenv("RECAPTCHA_SECRET_KEY"),
			MinScore:  minScore,
		}
	case "hcaptcha":
		return &HCaptchaVerifier{
			SecretKey: os.Getenv("HCAPTCHA_SECRET_KEY"),
		}
	default:
		return &NoopVerifier{}
	}
}

// NoopVerifier always passes. Used in development and testing.
type NoopVerifier struct{}

// Verify always returns nil.
func (n *NoopVerifier) Verify(token string, remoteIP string) error {
	return nil
}

// RecaptchaV3Verifier verifies Google reCAPTCHA v3 tokens.
type RecaptchaV3Verifier struct {
	SecretKey string
	MinScore  float64
	// VerifyURL can be overridden for testing. Defaults to Google's API.
	VerifyURL string
}

type recaptchaResponse struct {
	Success     bool     `json:"success"`
	Score       float64  `json:"score"`
	Action      string   `json:"action"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
}

// Verify checks a reCAPTCHA v3 token against Google's verification API.
func (v *RecaptchaV3Verifier) Verify(token string, remoteIP string) error {
	if v.SecretKey == "" {
		return fmt.Errorf("reCAPTCHA secret key not configured")
	}
	if token == "" {
		return fmt.Errorf("CAPTCHA token is required")
	}

	verifyURL := v.VerifyURL
	if verifyURL == "" {
		verifyURL = "https://www.google.com/recaptcha/api/siteverify"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.PostForm(verifyURL, url.Values{
		"secret":   {v.SecretKey},
		"response": {token},
		"remoteip": {remoteIP},
	})
	if err != nil {
		return fmt.Errorf("failed to verify CAPTCHA: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read CAPTCHA response: %w", err)
	}

	var result recaptchaResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse CAPTCHA response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("CAPTCHA verification failed: %v", result.ErrorCodes)
	}

	if result.Score < v.MinScore {
		return fmt.Errorf("CAPTCHA score too low: %.2f (minimum: %.2f)", result.Score, v.MinScore)
	}

	return nil
}

// HCaptchaVerifier verifies hCaptcha tokens.
type HCaptchaVerifier struct {
	SecretKey string
	// VerifyURL can be overridden for testing. Defaults to hCaptcha's API.
	VerifyURL string
}

type hcaptchaResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify checks an hCaptcha token against hCaptcha's verification API.
func (v *HCaptchaVerifier) Verify(token string, remoteIP string) error {
	if v.SecretKey == "" {
		return fmt.Errorf("hCaptcha secret key not configured")
	}
	if token == "" {
		return fmt.Errorf("CAPTCHA token is required")
	}

	verifyURL := v.VerifyURL
	if verifyURL == "" {
		verifyURL = "https://hcaptcha.com/siteverify"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.PostForm(verifyURL, url.Values{
		"secret":   {v.SecretKey},
		"response": {token},
		"remoteip": {remoteIP},
	})
	if err != nil {
		return fmt.Errorf("failed to verify CAPTCHA: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read CAPTCHA response: %w", err)
	}

	var result hcaptchaResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse CAPTCHA response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("CAPTCHA verification failed: %v", result.ErrorCodes)
	}

	return nil
}
