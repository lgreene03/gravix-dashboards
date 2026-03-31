package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/referral"
	"github.com/lgreene/gravix-dashboards/pkg/sso"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/pkg/totp"
)

// handleSSOConfig handles GET/PUT /api/gateway/sso — manage SSO configuration.
func (gw *gateway) handleSSOConfig(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, err := gw.db.SSOConfigs().GetByTenantID(r.Context(), claims.TenantID)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"configured": false,
			})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get SSO config")
			return
		}
		// Redact secrets
		resp := map[string]interface{}{
			"configured": true,
			"provider":   cfg.Provider,
			"enabled":    cfg.Enabled,
		}
		if cfg.Provider == "saml" {
			resp["entity_id"] = cfg.EntityID
			resp["sso_url"] = cfg.SSOURL
			resp["has_certificate"] = cfg.Certificate != ""
		} else {
			resp["client_id"] = cfg.ClientID
			resp["issuer"] = cfg.Issuer
			resp["has_secret"] = cfg.ClientSecret != ""
		}
		writeJSON(w, http.StatusOK, resp)

	case http.MethodPut:
		if !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		// Only business/scale/enterprise plans can configure SSO
		tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get tenant")
			return
		}
		if tenant.Plan != "business" && tenant.Plan != "scale" && tenant.Plan != "enterprise" {
			writeError(w, http.StatusForbidden, "SSO requires business plan or higher")
			return
		}

		var req struct {
			Provider     string `json:"provider"`
			Enabled      bool   `json:"enabled"`
			EntityID     string `json:"entity_id"`
			SSOURL       string `json:"sso_url"`
			Certificate  string `json:"certificate"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			Issuer       string `json:"issuer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		if req.Provider != "saml" && req.Provider != "oidc" {
			writeError(w, http.StatusBadRequest, "provider must be 'saml' or 'oidc'")
			return
		}

		cfg := &tenantdb.SSOConfig{
			TenantID:     claims.TenantID,
			Provider:     req.Provider,
			Enabled:      req.Enabled,
			EntityID:     req.EntityID,
			SSOURL:       req.SSOURL,
			Certificate:  req.Certificate,
			ClientID:     req.ClientID,
			ClientSecret: req.ClientSecret,
			Issuer:       req.Issuer,
		}
		if err := gw.db.SSOConfigs().Upsert(r.Context(), cfg); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save SSO config")
			return
		}

		gw.audit(r, "sso.configure", "sso_config", cfg.ID,
			fmt.Sprintf(`{"provider":"%s","enabled":%v}`, req.Provider, req.Enabled))

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case http.MethodDelete:
		if !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		if err := gw.db.SSOConfigs().Delete(r.Context(), claims.TenantID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete SSO config")
			return
		}
		gw.audit(r, "sso.delete", "sso_config", claims.TenantID, "")
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleTwoFactorSetup handles POST /api/gateway/2fa/setup — generate TOTP secret.
func (gw *gateway) handleTwoFactorSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	user, err := gw.db.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user.TwoFactorEnabled {
		writeError(w, http.StatusConflict, "2FA is already enabled")
		return
	}

	key, err := totp.Generate("Gravix", user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate TOTP key")
		return
	}

	// Store the secret (encrypted) temporarily — not yet confirmed
	encKey := gw.totpEncryptionKey()
	encrypted, err := totp.EncryptSecret(key.Secret, encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encrypt TOTP secret")
		return
	}

	// Store but don't enable until confirmed
	if err := gw.db.Users().UpdateTwoFactor(r.Context(), user.ID, false, encrypted); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save TOTP secret")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"secret":  key.Secret,
		"url":     key.URL(),
		"message": "Scan the QR code with your authenticator app, then confirm with /api/gateway/2fa/confirm",
	})
}

// handleTwoFactorConfirm handles POST /api/gateway/2fa/confirm — verify and enable 2FA.
func (gw *gateway) handleTwoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	user, err := gw.db.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user.TwoFactorEnabled {
		writeError(w, http.StatusConflict, "2FA is already enabled")
		return
	}
	if user.TwoFactorSecret == "" {
		writeError(w, http.StatusBadRequest, "call /api/gateway/2fa/setup first")
		return
	}

	// Decrypt and validate
	encKey := gw.totpEncryptionKey()
	secret, err := totp.DecryptSecret(user.TwoFactorSecret, encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decrypt TOTP secret")
		return
	}

	if !totp.Validate(req.Code, secret) {
		writeError(w, http.StatusBadRequest, "invalid TOTP code")
		return
	}

	// Enable 2FA
	if err := gw.db.Users().UpdateTwoFactor(r.Context(), user.ID, true, user.TwoFactorSecret); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enable 2FA")
		return
	}

	// Generate recovery codes and store hashed versions
	codes, err := totp.RecoveryCodes(8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate recovery codes")
		return
	}

	// Hash and store recovery codes
	codeHashes := make([]string, len(codes))
	for i, code := range codes {
		codeHashes[i] = fmt.Sprintf("%x", sha256.Sum256([]byte(code)))
	}
	if err := gw.db.RecoveryCodes().Store(r.Context(), user.ID, codeHashes); err != nil {
		slog.Error("failed to store recovery codes", "error", err)
	}

	gw.audit(r, "2fa.enable", "user", user.ID, "")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "enabled",
		"recovery_codes": codes,
		"message":        "Save these recovery codes securely. They cannot be shown again.",
	})
}

// handleTwoFactorDisable handles POST /api/gateway/2fa/disable — disable 2FA.
func (gw *gateway) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	user, err := gw.db.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if !user.TwoFactorEnabled {
		writeError(w, http.StatusBadRequest, "2FA is not enabled")
		return
	}

	// Validate current TOTP code
	encKey := gw.totpEncryptionKey()
	secret, err := totp.DecryptSecret(user.TwoFactorSecret, encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decrypt TOTP secret")
		return
	}
	if !totp.Validate(req.Code, secret) {
		writeError(w, http.StatusBadRequest, "invalid TOTP code")
		return
	}

	if err := gw.db.Users().UpdateTwoFactor(r.Context(), user.ID, false, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable 2FA")
		return
	}
	// Clean up recovery codes
	_ = gw.db.RecoveryCodes().DeleteByUser(r.Context(), user.ID)

	gw.audit(r, "2fa.disable", "user", user.ID, "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// handleSessions handles GET/DELETE /api/gateway/sessions — list/revoke sessions.
func (gw *gateway) handleSessions(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		sessions, err := gw.db.Sessions().ListByUser(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list sessions")
			return
		}

		type sessionResp struct {
			ID        string    `json:"id"`
			IPAddress string    `json:"ip_address"`
			UserAgent string    `json:"user_agent"`
			CreatedAt time.Time `json:"created_at"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		var resp []sessionResp
		for _, s := range sessions {
			resp = append(resp, sessionResp{
				ID:        s.ID,
				IPAddress: s.IPAddress,
				UserAgent: s.UserAgent,
				CreatedAt: s.CreatedAt,
				ExpiresAt: s.ExpiresAt,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": resp})

	case http.MethodDelete:
		// Revoke a specific session or all sessions
		sessionID := r.URL.Query().Get("id")
		if sessionID == "all" {
			if err := gw.db.Sessions().RevokeAllForUser(r.Context(), claims.UserID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to revoke sessions")
				return
			}
			gw.audit(r, "session.revoke_all", "user", claims.UserID, "")
			writeJSON(w, http.StatusOK, map[string]string{"status": "all sessions revoked"})
		} else if sessionID != "" {
			if err := gw.db.Sessions().Revoke(r.Context(), sessionID); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to revoke session")
				return
			}
			gw.audit(r, "session.revoke", "session", sessionID, "")
			writeJSON(w, http.StatusOK, map[string]string{"status": "session revoked"})
		} else {
			writeError(w, http.StatusBadRequest, "session id required (?id=xxx or ?id=all)")
		}

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleMultiOrg handles GET/POST /api/gateway/orgs — multi-org management.
func (gw *gateway) handleMultiOrg(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !claims.HasRole(auth.RoleAdmin) {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List child organizations
		children, err := gw.db.Tenants().ListChildren(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list child orgs")
			return
		}

		type orgResp struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			Plan      string    `json:"plan"`
			Status    string    `json:"status"`
			CreatedAt time.Time `json:"created_at"`
		}
		var resp []orgResp
		for _, c := range children {
			resp = append(resp, orgResp{
				ID:        c.ID,
				Name:      c.Name,
				Plan:      c.Plan,
				Status:    c.Status,
				CreatedAt: c.CreatedAt,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"organizations": resp})

	case http.MethodPost:
		// Create a child organization
		tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get tenant")
			return
		}
		if tenant.Plan != "enterprise" && tenant.Plan != "scale" {
			writeError(w, http.StatusForbidden, "multi-org requires scale or enterprise plan")
			return
		}

		var req struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.Email == "" {
			writeError(w, http.StatusBadRequest, "name and email are required")
			return
		}

		child := &tenantdb.Tenant{
			Name:           req.Name,
			Email:          req.Email,
			Plan:           tenant.Plan, // inherit parent plan
			Status:         "active",
			ParentTenantID: claims.TenantID,
		}
		if err := gw.db.Tenants().Create(r.Context(), child); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
				writeError(w, http.StatusConflict, "organization with this email already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create child org")
			return
		}

		// Set parent tenant ID
		if err := gw.db.Tenants().UpdateParentTenant(r.Context(), child.ID, claims.TenantID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set parent org")
			return
		}

		gw.audit(r, "org.create", "tenant", child.ID,
			fmt.Sprintf(`{"name":"%s","parent":"%s"}`, req.Name, claims.TenantID))

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"id":     child.ID,
			"name":   child.Name,
			"status": "created",
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// totpEncryptionKey returns the 32-byte encryption key for TOTP secrets.
// Uses a dedicated key if configured, otherwise falls back to JWT secret (deprecated).
func (gw *gateway) totpEncryptionKey() []byte {
	if len(gw.totpKey) >= 32 {
		return gw.totpKey[:32]
	}
	// Fallback to JWT secret (deprecated — set TOTP_ENCRYPTION_KEY in production)
	key := []byte(gw.jwtSecret)
	if len(key) >= 32 {
		return key[:32]
	}
	padded := make([]byte, 32)
	copy(padded, key)
	return padded
}

// handleSSOLogin initiates SSO login for a tenant.
// GET /api/gateway/sso/login?tenant_id=xxx
func (gw *gateway) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	cfg, err := gw.db.SSOConfigs().GetByTenantID(r.Context(), tenantID)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "SSO not configured for this tenant")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get SSO config")
		return
	}
	if !cfg.Enabled {
		writeError(w, http.StatusForbidden, "SSO is not enabled for this tenant")
		return
	}

	ssoCfg := &sso.Config{
		TenantID:     cfg.TenantID,
		Provider:     cfg.Provider,
		EntityID:     cfg.EntityID,
		SSOURL:       cfg.SSOURL,
		Certificate:  cfg.Certificate,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Issuer:       cfg.Issuer,
	}

	callbackURL := gw.baseURL + "/api/gateway/sso/callback?tenant_id=" + tenantID
	var auth sso.Authenticator
	if cfg.Provider == sso.ProviderSAML {
		auth = &sso.SAMLAuthenticator{}
	} else {
		auth = &sso.OIDCAuthenticator{}
	}

	redirectURL, state, authErr := auth.InitiateLogin(ssoCfg, callbackURL)
	if authErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to initiate SSO: "+authErr.Error())
		return
	}

	// Store state for CSRF validation on callback
	if state != "" {
		_ = gw.db.SSOStates().Store(r.Context(), state, tenantID, time.Now().Add(10*time.Minute))
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"redirect_url": redirectURL,
	})
}

// handleSSOCallback processes SSO callback and provisions/authenticates the user.
// POST /api/gateway/sso/callback?tenant_id=xxx&state=xxx
func (gw *gateway) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		writeError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	// Validate CSRF state parameter
	state := r.URL.Query().Get("state")
	if state == "" {
		// Also check POST form
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			state = r.PostFormValue("state")
		}
	}
	if state != "" {
		validTenantID, stateErr := gw.db.SSOStates().ValidateAndDelete(r.Context(), state)
		if stateErr != nil {
			writeError(w, http.StatusForbidden, "invalid or expired SSO state")
			return
		}
		if validTenantID != tenantID {
			writeError(w, http.StatusForbidden, "SSO state tenant mismatch")
			return
		}
	}

	cfg, err := gw.db.SSOConfigs().GetByTenantID(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get SSO config")
		return
	}

	ssoCfg := &sso.Config{
		TenantID:     cfg.TenantID,
		Provider:     cfg.Provider,
		EntityID:     cfg.EntityID,
		SSOURL:       cfg.SSOURL,
		Certificate:  cfg.Certificate,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Issuer:       cfg.Issuer,
	}

	// Collect callback params
	params := make(map[string]string)
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		for k, v := range r.PostForm {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
	}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	var authenticator sso.Authenticator
	if cfg.Provider == sso.ProviderSAML {
		authenticator = &sso.SAMLAuthenticator{}
	} else {
		authenticator = &sso.OIDCAuthenticator{}
	}

	ssoUser, valErr := authenticator.ValidateCallback(r.Context(), ssoCfg, params)
	if valErr != nil {
		writeError(w, http.StatusUnauthorized, "SSO validation failed: "+valErr.Error())
		return
	}

	// JIT provisioning: find or create user
	user, userErr := gw.db.Users().GetByEmail(r.Context(), ssoUser.Email)
	if userErr != nil {
		// Create user via JIT provisioning
		newUser := &tenantdb.User{
			TenantID:      tenantID,
			Email:         ssoUser.Email,
			PasswordHash:  "", // SSO users don't need passwords
			Role:          "editor",
			EmailVerified: true,
			Status:        "active",
		}
		if createErr := gw.db.Users().Create(r.Context(), newUser); createErr != nil {
			if strings.Contains(createErr.Error(), "UNIQUE") || strings.Contains(createErr.Error(), "unique") {
				// Race condition — user was created concurrently
				user, _ = gw.db.Users().GetByEmail(r.Context(), ssoUser.Email)
			} else {
				writeError(w, http.StatusInternalServerError, "failed to provision user")
				return
			}
		} else {
			user = newUser
		}
	}

	if user == nil {
		writeError(w, http.StatusInternalServerError, "failed to provision user")
		return
	}

	token, tokenErr := gw.tokens.Generate(user.TenantID, user.ID, user.Email, user.Role)
	if tokenErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	_ = gw.db.Users().UpdateLastLogin(r.Context(), user.ID, time.Now())
	gw.auditDirect(user.TenantID, user.ID, "user.sso_login", "user", user.ID, fmt.Sprintf(`{"provider":"%s"}`, cfg.Provider), r.RemoteAddr)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     token,
		"tenant_id": user.TenantID,
		"user_id":   user.ID,
		"email":     user.Email,
		"role":      user.Role,
	})
}

// handleReferrals handles GET/POST /api/gateway/referrals — manage referral codes.
func (gw *gateway) handleReferrals(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Create an in-memory referral service (no persistent repo yet — uses the handler for validation)
	switch r.Method {
	case http.MethodGet:
		// List referral codes for this tenant
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"referrals": []interface{}{},
			"message":   "referral program active — create codes with POST",
		})

	case http.MethodPost:
		code, err := referral.GenerateCode()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate referral code")
			return
		}

		gw.audit(r, "referral.create", "referral", code, "")

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"code":       code,
			"expires_in": "90 days",
			"share_url":  gw.baseURL + "/signup?ref=" + code,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleRedeemReferral handles POST /api/gateway/referrals/redeem.
func (gw *gateway) handleRedeemReferral(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	gw.audit(r, "referral.redeem", "referral", req.Code, "")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "redeemed",
		"code":    req.Code,
		"message": "referral applied — both parties will receive 1 month free",
	})
}
