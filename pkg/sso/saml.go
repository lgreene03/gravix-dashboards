package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// SAMLAuthenticator handles SAML 2.0 SSO flows.
type SAMLAuthenticator struct{}

// NewSAMLAuthenticator creates a new SAML authenticator.
func NewSAMLAuthenticator() *SAMLAuthenticator {
	return &SAMLAuthenticator{}
}

func (s *SAMLAuthenticator) InitiateLogin(cfg *Config, callbackURL string) (string, string, error) {
	if cfg.Provider != ProviderSAML {
		return "", "", fmt.Errorf("sso: expected SAML provider, got %s", cfg.Provider)
	}
	if cfg.SSOURL == "" {
		return "", "", fmt.Errorf("sso: SSO URL not configured")
	}
	if cfg.EntityID == "" {
		return "", "", fmt.Errorf("sso: Entity ID not configured")
	}

	// Generate request ID and relay state
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	// Build AuthnRequest ID
	reqID := "_" + hex.EncodeToString(stateBytes)

	// Build SAML AuthnRequest
	authnRequest := fmt.Sprintf(`<samlp:AuthnRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="%s" Version="2.0" IssueInstant="%s" AssertionConsumerServiceURL="%s" ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"><saml:Issuer>%s</saml:Issuer></samlp:AuthnRequest>`,
		reqID,
		time.Now().UTC().Format(time.RFC3339),
		callbackURL,
		"gravix-sp",
	)

	// Encode for HTTP-Redirect binding (base64 + deflate would be used in production)
	encoded := base64.StdEncoding.EncodeToString([]byte(authnRequest))

	u, err := url.Parse(cfg.SSOURL)
	if err != nil {
		return "", "", fmt.Errorf("sso: invalid SSO URL: %w", err)
	}
	q := u.Query()
	q.Set("SAMLRequest", encoded)
	q.Set("RelayState", state)
	u.RawQuery = q.Encode()

	return u.String(), state, nil
}

func (s *SAMLAuthenticator) ValidateCallback(ctx context.Context, cfg *Config, params map[string]string) (*SSOUser, error) {
	if cfg.Provider != ProviderSAML {
		return nil, fmt.Errorf("sso: expected SAML provider, got %s", cfg.Provider)
	}

	samlResponse, ok := params["SAMLResponse"]
	if !ok || samlResponse == "" {
		return nil, ErrInvalidAssertion
	}

	// Decode the SAML response
	decoded, err := base64.StdEncoding.DecodeString(samlResponse)
	if err != nil {
		return nil, fmt.Errorf("sso: failed to decode SAML response: %w", err)
	}

	// In production, this would:
	// 1. Parse XML
	// 2. Validate signature against cfg.Certificate
	// 3. Check NotBefore/NotOnOrAfter conditions
	// 4. Extract NameID and attributes
	// For now, extract email from the response XML as a basic check
	responseStr := string(decoded)
	if !strings.Contains(responseStr, "saml") && !strings.Contains(responseStr, "SAML") {
		return nil, ErrInvalidAssertion
	}

	// Extract email from NameID (simplified parsing)
	email := extractBetween(responseStr, "<saml:NameID", "</saml:NameID>")
	if email == "" {
		email = extractBetween(responseStr, "EmailAddress\">", "</saml:AttributeValue>")
	}
	if email == "" {
		return nil, fmt.Errorf("sso: could not extract email from SAML assertion")
	}

	// Clean up the email (remove tag close)
	if idx := strings.Index(email, ">"); idx >= 0 {
		email = email[idx+1:]
	}
	email = strings.TrimSpace(email)

	return &SSOUser{
		Email: email,
	}, nil
}

// extractBetween extracts text between start marker and end marker.
func extractBetween(s, start, end string) string {
	startIdx := strings.Index(s, start)
	if startIdx < 0 {
		return ""
	}
	startIdx += len(start)
	endIdx := strings.Index(s[startIdx:], end)
	if endIdx < 0 {
		return ""
	}
	return s[startIdx : startIdx+endIdx]
}
