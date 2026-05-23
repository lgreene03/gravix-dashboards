package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OIDCAuthenticator handles OpenID Connect SSO flows.
type OIDCAuthenticator struct {
	httpClient *http.Client
}

// NewOIDCAuthenticator creates a new OIDC authenticator.
func NewOIDCAuthenticator(client *http.Client) *OIDCAuthenticator {
	if client == nil {
		client = http.DefaultClient
	}
	return &OIDCAuthenticator{httpClient: client}
}

func (o *OIDCAuthenticator) InitiateLogin(cfg *Config, callbackURL string) (string, string, error) {
	if cfg.Provider != ProviderOIDC {
		return "", "", fmt.Errorf("sso: expected OIDC provider, got %s", cfg.Provider)
	}
	if cfg.Issuer == "" {
		return "", "", fmt.Errorf("sso: OIDC issuer not configured")
	}
	if cfg.ClientID == "" {
		return "", "", fmt.Errorf("sso: OIDC client ID not configured")
	}

	// Generate state parameter
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	// Build authorization URL
	// In production, we'd discover the authorization endpoint from .well-known/openid-configuration
	authURL := strings.TrimSuffix(cfg.Issuer, "/") + "/authorize"

	u, err := url.Parse(authURL)
	if err != nil {
		return "", "", fmt.Errorf("sso: invalid issuer URL: %w", err)
	}
	q := u.Query()
	q.Set("client_id", cfg.ClientID)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("redirect_uri", callbackURL)
	q.Set("state", state)
	u.RawQuery = q.Encode()

	return u.String(), state, nil
}

// oidcTokenResponse represents the token endpoint response.
type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// oidcUserInfo represents the userinfo endpoint response.
type oidcUserInfo struct {
	Sub        string `json:"sub"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
}

func (o *OIDCAuthenticator) ValidateCallback(ctx context.Context, cfg *Config, params map[string]string) (*SSOUser, error) {
	if cfg.Provider != ProviderOIDC {
		return nil, fmt.Errorf("sso: expected OIDC provider, got %s", cfg.Provider)
	}

	code, ok := params["code"]
	if !ok || code == "" {
		return nil, fmt.Errorf("sso: missing authorization code")
	}

	callbackURL := params["redirect_uri"]

	// Exchange code for tokens
	tokenURL := strings.TrimSuffix(cfg.Issuer, "/") + "/token"
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {callbackURL},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("sso: failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sso: token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("sso: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp oidcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("sso: failed to decode token response: %w", err)
	}

	// Fetch userinfo
	userinfoURL := strings.TrimSuffix(cfg.Issuer, "/") + "/userinfo"
	uiReq, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("sso: failed to create userinfo request: %w", err)
	}
	uiReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

	uiResp, err := o.httpClient.Do(uiReq)
	if err != nil {
		return nil, fmt.Errorf("sso: userinfo request failed: %w", err)
	}
	defer uiResp.Body.Close()

	if uiResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sso: userinfo endpoint returned %d", uiResp.StatusCode)
	}

	var userInfo oidcUserInfo
	if err := json.NewDecoder(uiResp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("sso: failed to decode userinfo: %w", err)
	}

	if userInfo.Email == "" {
		return nil, fmt.Errorf("sso: no email in userinfo response")
	}

	return &SSOUser{
		Email:     userInfo.Email,
		FirstName: userInfo.GivenName,
		LastName:  userInfo.FamilyName,
	}, nil
}
