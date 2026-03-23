package captcha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoopVerifier_AlwaysPasses(t *testing.T) {
	v := &NoopVerifier{}
	if err := v.Verify("", ""); err != nil {
		t.Fatalf("NoopVerifier should always pass, got: %v", err)
	}
	if err := v.Verify("some-token", "1.2.3.4"); err != nil {
		t.Fatalf("NoopVerifier should always pass, got: %v", err)
	}
}

func TestRecaptchaV3Verifier_NoSecretKey(t *testing.T) {
	v := &RecaptchaV3Verifier{}
	err := v.Verify("token", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for missing secret key")
	}
}

func TestRecaptchaV3Verifier_EmptyToken(t *testing.T) {
	v := &RecaptchaV3Verifier{SecretKey: "test-secret"}
	err := v.Verify("", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestRecaptchaV3Verifier_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("secret") != "test-secret" {
			t.Error("expected secret to be passed")
		}
		if r.FormValue("response") != "valid-token" {
			t.Error("expected response token to be passed")
		}
		json.NewEncoder(w).Encode(recaptchaResponse{
			Success: true,
			Score:   0.9,
		})
	}))
	defer srv.Close()

	v := &RecaptchaV3Verifier{
		SecretKey: "test-secret",
		MinScore:  0.5,
		VerifyURL: srv.URL,
	}
	if err := v.Verify("valid-token", "1.2.3.4"); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestRecaptchaV3Verifier_LowScore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(recaptchaResponse{
			Success: true,
			Score:   0.2,
		})
	}))
	defer srv.Close()

	v := &RecaptchaV3Verifier{
		SecretKey: "test-secret",
		MinScore:  0.5,
		VerifyURL: srv.URL,
	}
	err := v.Verify("bot-token", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for low score")
	}
}

func TestRecaptchaV3Verifier_VerificationFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(recaptchaResponse{
			Success:    false,
			ErrorCodes: []string{"invalid-input-response"},
		})
	}))
	defer srv.Close()

	v := &RecaptchaV3Verifier{
		SecretKey: "test-secret",
		MinScore:  0.5,
		VerifyURL: srv.URL,
	}
	err := v.Verify("bad-token", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for failed verification")
	}
}

func TestHCaptchaVerifier_NoSecretKey(t *testing.T) {
	v := &HCaptchaVerifier{}
	err := v.Verify("token", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for missing secret key")
	}
}

func TestHCaptchaVerifier_EmptyToken(t *testing.T) {
	v := &HCaptchaVerifier{SecretKey: "test-secret"}
	err := v.Verify("", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestHCaptchaVerifier_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("secret") != "test-secret" {
			t.Error("expected secret to be passed")
		}
		json.NewEncoder(w).Encode(hcaptchaResponse{
			Success: true,
		})
	}))
	defer srv.Close()

	v := &HCaptchaVerifier{
		SecretKey: "test-secret",
		VerifyURL: srv.URL,
	}
	if err := v.Verify("valid-token", "1.2.3.4"); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestHCaptchaVerifier_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(hcaptchaResponse{
			Success:    false,
			ErrorCodes: []string{"invalid-or-already-seen-response"},
		})
	}))
	defer srv.Close()

	v := &HCaptchaVerifier{
		SecretKey: "test-secret",
		VerifyURL: srv.URL,
	}
	err := v.Verify("used-token", "1.2.3.4")
	if err == nil {
		t.Fatal("expected error for failed verification")
	}
}

func TestNewVerifierFromEnv_Default(t *testing.T) {
	v := NewVerifierFromEnv()
	if _, ok := v.(*NoopVerifier); !ok {
		t.Fatal("expected NoopVerifier as default")
	}
}
