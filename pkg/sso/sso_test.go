package sso

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSAMLInitiateLogin(t *testing.T) {
	auth := NewSAMLAuthenticator()
	cfg := &Config{
		Provider: ProviderSAML,
		EntityID: "https://idp.example.com",
		SSOURL:   "https://idp.example.com/sso",
	}

	redirectURL, state, err := auth.InitiateLogin(cfg, "https://app.gravix.io/sso/callback")
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Error("state should not be empty")
	}
	if redirectURL == "" {
		t.Error("redirect URL should not be empty")
	}
	if !strings.Contains(redirectURL, "SAMLRequest=") {
		t.Error("redirect URL should contain SAMLRequest parameter")
	}
	if !strings.Contains(redirectURL, "RelayState=") {
		t.Error("redirect URL should contain RelayState parameter")
	}
}

func TestSAMLInitiateLogin_MissingSSOURL(t *testing.T) {
	auth := NewSAMLAuthenticator()
	cfg := &Config{
		Provider: ProviderSAML,
		EntityID: "https://idp.example.com",
	}
	_, _, err := auth.InitiateLogin(cfg, "https://app.gravix.io/sso/callback")
	if err == nil {
		t.Error("should error on missing SSO URL")
	}
}

func TestSAMLInitiateLogin_WrongProvider(t *testing.T) {
	auth := NewSAMLAuthenticator()
	cfg := &Config{Provider: ProviderOIDC}
	_, _, err := auth.InitiateLogin(cfg, "https://app.gravix.io/sso/callback")
	if err == nil {
		t.Error("should error on wrong provider")
	}
}

func TestSAMLValidateCallback_MissingResponse(t *testing.T) {
	auth := NewSAMLAuthenticator()
	cfg := &Config{Provider: ProviderSAML}
	_, err := auth.ValidateCallback(context.Background(), cfg, map[string]string{})
	if err == nil {
		t.Error("should error on missing SAMLResponse")
	}
}

func TestSAMLValidateCallback_ValidResponse(t *testing.T) {
	auth := NewSAMLAuthenticator()
	cfg := &Config{
		Provider:    ProviderSAML,
		Certificate: "test-cert",
	}

	// Build a minimal SAML response with NameID
	samlResp := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"><saml:Assertion><saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">alice@example.com</saml:NameID></saml:Subject></saml:Assertion></samlp:Response>`
	encoded := base64.StdEncoding.EncodeToString([]byte(samlResp))

	user, err := auth.ValidateCallback(context.Background(), cfg, map[string]string{
		"SAMLResponse": encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "alice@example.com" {
		t.Errorf("email = %q, want alice@example.com", user.Email)
	}
}

func TestSAMLValidateCallback_InvalidBase64(t *testing.T) {
	auth := NewSAMLAuthenticator()
	cfg := &Config{Provider: ProviderSAML}
	_, err := auth.ValidateCallback(context.Background(), cfg, map[string]string{
		"SAMLResponse": "not-valid-base64!!!",
	})
	if err == nil {
		t.Error("should error on invalid base64")
	}
}

func TestOIDCInitiateLogin(t *testing.T) {
	auth := NewOIDCAuthenticator(nil)
	cfg := &Config{
		Provider: ProviderOIDC,
		ClientID: "my-client-id",
		Issuer:   "https://accounts.google.com",
	}

	redirectURL, state, err := auth.InitiateLogin(cfg, "https://app.gravix.io/sso/callback")
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Error("state should not be empty")
	}
	if !strings.Contains(redirectURL, "client_id=my-client-id") {
		t.Errorf("redirect URL should contain client_id, got %s", redirectURL)
	}
	if !strings.Contains(redirectURL, "response_type=code") {
		t.Error("redirect URL should contain response_type=code")
	}
	if !strings.Contains(redirectURL, "scope=openid") {
		t.Error("redirect URL should contain openid scope")
	}
}

func TestOIDCInitiateLogin_MissingIssuer(t *testing.T) {
	auth := NewOIDCAuthenticator(nil)
	cfg := &Config{
		Provider: ProviderOIDC,
		ClientID: "my-client-id",
	}
	_, _, err := auth.InitiateLogin(cfg, "https://app.gravix.io/sso/callback")
	if err == nil {
		t.Error("should error on missing issuer")
	}
}

func TestOIDCValidateCallback(t *testing.T) {
	// Mock OIDC provider
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(oidcTokenResponse{
			AccessToken: "test-access-token",
			IDToken:     "test-id-token",
			TokenType:   "Bearer",
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(oidcUserInfo{
			Sub:        "user123",
			Email:      "bob@example.com",
			GivenName:  "Bob",
			FamilyName: "Smith",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	auth := NewOIDCAuthenticator(server.Client())
	cfg := &Config{
		Provider:     ProviderOIDC,
		ClientID:     "my-client-id",
		ClientSecret: "my-secret",
		Issuer:       server.URL,
	}

	user, err := auth.ValidateCallback(context.Background(), cfg, map[string]string{
		"code":         "auth-code-123",
		"redirect_uri": "https://app.gravix.io/sso/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "bob@example.com" {
		t.Errorf("email = %q, want bob@example.com", user.Email)
	}
	if user.FirstName != "Bob" {
		t.Errorf("first name = %q, want Bob", user.FirstName)
	}
}

func TestOIDCValidateCallback_MissingCode(t *testing.T) {
	auth := NewOIDCAuthenticator(nil)
	cfg := &Config{Provider: ProviderOIDC, Issuer: "https://example.com"}
	_, err := auth.ValidateCallback(context.Background(), cfg, map[string]string{})
	if err == nil {
		t.Error("should error on missing code")
	}
}

func TestExtractBetween(t *testing.T) {
	tests := []struct {
		s, start, end, want string
	}{
		{"hello <b>world</b>", "<b>", "</b>", "world"},
		{"no match here", "<b>", "</b>", ""},
		{"<a>test", "<a>", "</a>", ""},
	}
	for _, tt := range tests {
		got := extractBetween(tt.s, tt.start, tt.end)
		if got != tt.want {
			t.Errorf("extractBetween(%q, %q, %q) = %q, want %q", tt.s, tt.start, tt.end, got, tt.want)
		}
	}
}
