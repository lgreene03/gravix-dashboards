package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/email"
	"github.com/lgreene/gravix-dashboards/pkg/password"
	"github.com/lgreene/gravix-dashboards/pkg/ratelimit"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/pkg/totp"
	"golang.org/x/crypto/bcrypt"
)

// loginAttemptTracker tracks failed login attempts per email for account lockout.
var loginAttemptTracker sync.Map // email -> *loginAttempts

const (
	maxLoginAttempts    = 5
	loginLockoutWindow = 15 * time.Minute
)

type loginAttempts struct {
	mu       sync.Mutex
	failures []time.Time
}

// recordFailure records a failed login attempt and returns true if the account is now locked out.
func recordLoginFailure(emailAddr string) bool {
	val, _ := loginAttemptTracker.LoadOrStore(emailAddr, &loginAttempts{})
	attempts := val.(*loginAttempts)
	attempts.mu.Lock()
	defer attempts.mu.Unlock()

	now := time.Now()
	// Prune old entries outside the lockout window
	cutoff := now.Add(-loginLockoutWindow)
	fresh := attempts.failures[:0]
	for _, t := range attempts.failures {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	fresh = append(fresh, now)
	attempts.failures = fresh

	return len(fresh) >= maxLoginAttempts
}

// isLoginLocked checks if the email is currently locked out due to too many failures.
func isLoginLocked(emailAddr string) bool {
	val, ok := loginAttemptTracker.Load(emailAddr)
	if !ok {
		return false
	}
	attempts := val.(*loginAttempts)
	attempts.mu.Lock()
	defer attempts.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-loginLockoutWindow)
	fresh := attempts.failures[:0]
	for _, t := range attempts.failures {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	attempts.failures = fresh

	return len(fresh) >= maxLoginAttempts
}

// clearLoginFailures removes tracked failures for the email (on successful login).
func clearLoginFailures(emailAddr string) {
	loginAttemptTracker.Delete(emailAddr)
}

// handleLogout revokes the current JWT by adding its JTI to the blacklist.
func (gw *gateway) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil || claims.ID == "" {
		writeError(w, http.StatusBadRequest, "token has no ID")
		return
	}

	// Store the JTI in DB so it persists across restarts and replicas
	_ = gw.db.RevokedTokens().Revoke(r.Context(), claims.ID, claims.ExpiresAt.Time)

	gw.auditDirect(claims.TenantID, claims.UserID, "user.logout", "user", claims.UserID, "", r.RemoteAddr)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logged out successfully",
	})
}

// isTokenBlacklisted checks if a JTI has been revoked (DB-backed, works across replicas).
func (gw *gateway) isTokenBlacklisted(jti string) bool {
	if jti == "" {
		return false
	}
	return gw.db.RevokedTokens().IsRevoked(context.Background(), jti)
}

// handleChangePassword lets an authenticated user change their password.
// PUT /api/gateway/password  body: {"current_password": "...", "new_password": "..."}
func (gw *gateway) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "PUT required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}

	// Validate new password strength
	if err := password.Validate(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Verify current password
	user, err := gw.db.Users().GetByEmail(r.Context(), claims.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve user")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	// Hash and update
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	if err := gw.db.Users().UpdatePassword(r.Context(), user.ID, string(hashed)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	gw.auditDirect(claims.TenantID, claims.UserID, "user.password_changed", "user", claims.UserID, "", r.RemoteAddr)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "password changed successfully",
	})
}

// handleRefreshToken exchanges a valid JWT for a fresh one with a new expiry.
// POST /api/gateway/refresh
func (gw *gateway) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())

	// Generate a fresh token with same claims but new expiry and JTI
	newToken, err := gw.tokens.Generate(claims.TenantID, claims.UserID, claims.Email, claims.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	// Revoke the old token so it can't be reused
	if claims.ID != "" {
		_ = gw.db.RevokedTokens().Revoke(context.Background(), claims.ID, claims.ExpiresAt.Time)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     newToken,
		"tenant_id": claims.TenantID,
		"user_id":   claims.UserID,
		"email":     claims.Email,
		"role":      claims.Role,
	})
}

// handleLogin authenticates a user with email/password and returns a JWT.
func (gw *gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		TOTPCode     string `json:"totp_code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	// Account lockout: reject if too many recent failures
	if isLoginLocked(req.Email) {
		slog.Warn("login locked out due to too many failed attempts", "email", req.Email)
		writeError(w, http.StatusTooManyRequests, "too many failed login attempts, please try again later")
		return
	}

	user, err := gw.db.Users().GetByEmail(r.Context(), req.Email)
	if err != nil {
		recordLoginFailure(req.Email)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		recordLoginFailure(req.Email)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Check if user is deactivated
	if user.Status == "deactivated" {
		writeError(w, http.StatusForbidden, "account has been deactivated")
		return
	}

	// Get tenant info for the token
	tenant, err := gw.db.Tenants().GetByID(r.Context(), user.TenantID)
	if err != nil || tenant.Status != "active" {
		writeError(w, http.StatusForbidden, "tenant is not active")
		return
	}

	// Check if 2FA is enabled — require TOTP code or recovery code
	if user.TwoFactorEnabled {
		if req.TOTPCode == "" && req.RecoveryCode == "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"requires_2fa": true,
				"user_id":      user.ID,
				"message":      "provide totp_code or recovery_code to complete login",
			})
			return
		}

		if req.RecoveryCode != "" {
			// Validate recovery code (SHA-256 hash, one-time use)
			codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.RecoveryCode)))
			valid, rcErr := gw.db.RecoveryCodes().Validate(r.Context(), user.ID, codeHash)
			if rcErr != nil || !valid {
				writeError(w, http.StatusUnauthorized, "invalid recovery code")
				return
			}
		} else {
			encKey := gw.totpEncryptionKey()
			secret, decErr := totp.DecryptSecret(user.TwoFactorSecret, encKey)
			if decErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to validate 2FA")
				return
			}
			if !totp.Validate(req.TOTPCode, secret) {
				writeError(w, http.StatusUnauthorized, "invalid TOTP code")
				return
			}
		}
	}

	token, err := gw.tokens.Generate(user.TenantID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	// Clear login failure tracker on successful login
	clearLoginFailures(req.Email)

	// Update last login time
	_ = gw.db.Users().UpdateLastLogin(r.Context(), user.ID, time.Now())

	gw.auditDirect(user.TenantID, user.ID, "user.login", "user", user.ID, "", r.RemoteAddr)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":          token,
		"tenant_id":      user.TenantID,
		"user_id":        user.ID,
		"email":          user.Email,
		"role":           user.Role,
		"plan":           tenant.Plan,
		"email_verified": user.EmailVerified,
	})
}

// handleRegister creates a new tenant, admin user, initial API key, and
// optionally a Stripe customer with a Free subscription.
func (gw *gateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Name         string `json:"name"`
		Email        string `json:"email"`
		Password     string `json:"password"`
		CaptchaToken string `json:"captcha_token"`
		AcceptTOS    bool   `json:"accept_tos"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Name == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "name, email, and password are required")
		return
	}
	if !req.AcceptTOS {
		writeError(w, http.StatusBadRequest, "you must accept the Terms of Service")
		return
	}

	// Verify CAPTCHA if configured
	if gw.captchaVerifier != nil {
		ip := ratelimit.ExtractIP(r)
		if err := gw.captchaVerifier.Verify(req.CaptchaToken, ip); err != nil {
			writeError(w, http.StatusBadRequest, "CAPTCHA verification failed")
			return
		}
	}

	if err := password.Validate(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()

	// Check if email already exists
	if _, err := gw.db.Tenants().GetByEmail(ctx, req.Email); err == nil {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}

	// Create tenant
	tenant := &tenantdb.Tenant{
		Name:   req.Name,
		Email:  req.Email,
		Plan:   "free",
		Status: "active",
	}
	if err := gw.db.Tenants().Create(ctx, tenant); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create tenant")
		return
	}

	// Create Stripe customer + subscription if billing is enabled
	if gw.billing != nil {
		customerID, err := gw.billing.CreateCustomer(ctx, req.Name, req.Email, tenant.ID)
		if err != nil {
			slog.Warn("failed to create stripe customer", "tenant_id", tenant.ID, "error", err)
		} else {
			subID, err := gw.billing.CreateSubscription(ctx, customerID, gw.billing.FreePriceID())
			if err != nil {
				slog.Warn("failed to create stripe subscription", "tenant_id", tenant.ID, "error", err)
			}
			if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, customerID, subID); err != nil {
				slog.Warn("failed to store stripe IDs", "tenant_id", tenant.ID, "error", err)
			}
		}
	}

	// Create admin user
	user := &tenantdb.User{
		TenantID:     tenant.ID,
		Email:        req.Email,
		PasswordHash: req.Password, // Will be hashed by the repo
		Role:         "admin",
	}
	if err := gw.db.Users().Create(ctx, user); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// Record TOS consent
	_ = gw.db.ConsentRecords().Create(ctx, &tenantdb.ConsentRecord{
		TenantID:  tenant.ID,
		UserID:    user.ID,
		Type:      "tos",
		Version:   "1.0",
		Accepted:  true,
		IPAddress: ratelimit.ExtractIP(r),
		CreatedAt: time.Now().UTC(),
	})
	_ = gw.db.ConsentRecords().Create(ctx, &tenantdb.ConsentRecord{
		TenantID:  tenant.ID,
		UserID:    user.ID,
		Type:      "privacy",
		Version:   "1.0",
		Accepted:  true,
		IPAddress: ratelimit.ExtractIP(r),
		CreatedAt: time.Now().UTC(),
	})

	// Send email verification token
	gw.sendVerificationEmail(ctx, user)

	// Generate JWT for immediate login (limited until email verified)
	token, err := gw.tokens.Generate(tenant.ID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	gw.auditDirect(tenant.ID, user.ID, "tenant.register", "tenant", tenant.ID,
		fmt.Sprintf(`{"name":%q,"email":%q}`, req.Name, req.Email), r.RemoteAddr)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":                      token,
		"tenant_id":                  tenant.ID,
		"user_id":                    user.ID,
		"email":                      user.Email,
		"role":                       user.Role,
		"plan":                       tenant.Plan,
		"email_verification_required": true,
	})
}

// generateSecureToken creates a cryptographically random token and returns (plaintext, sha256hash).
func generateSecureToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plain := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(hash[:]), nil
}

// sendVerificationEmail creates a verification token and sends the verification email.
func (gw *gateway) sendVerificationEmail(ctx context.Context, user *tenantdb.User) {
	plain, tokenHash, err := generateSecureToken()
	if err != nil {
		slog.Error("failed to generate verification token", "error", err)
		return
	}

	token := &tenantdb.EmailVerificationToken{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := gw.db.EmailVerifications().Create(ctx, token); err != nil {
		slog.Error("failed to store verification token", "error", err)
		return
	}

	htmlBody, textBody, err := email.RenderEmailVerification(email.TemplateData{
		UserName: user.Email,
		Token:    plain,
		BaseURL:  gw.baseURL,
	})
	if err != nil {
		slog.Error("failed to render verification email", "error", err)
		return
	}

	if err := gw.emailSender.Send(ctx, user.Email, "Verify your Gravix email", htmlBody, textBody); err != nil {
		slog.Error("failed to send verification email", "error", err, "to", user.Email)
	}
}

// handleForgotPassword initiates a password reset flow.
// Always returns 200 regardless of whether the email exists (prevents enumeration).
func (gw *gateway) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Always return success to prevent email enumeration
	defer func() {
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "If that email is registered, a password reset link has been sent.",
		})
	}()

	if req.Email == "" {
		return
	}

	ctx := r.Context()
	user, err := gw.db.Users().GetByEmail(ctx, req.Email)
	if err != nil {
		return // user not found, but return 200 anyway
	}

	plain, tokenHash, err := generateSecureToken()
	if err != nil {
		slog.Error("failed to generate reset token", "error", err)
		return
	}

	resetToken := &tenantdb.PasswordResetToken{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := gw.db.PasswordResets().Create(ctx, resetToken); err != nil {
		slog.Error("failed to store reset token", "error", err)
		return
	}

	htmlBody, textBody, err := email.RenderPasswordReset(email.TemplateData{
		UserName: user.Email,
		Token:    plain,
		BaseURL:  gw.baseURL,
	})
	if err != nil {
		slog.Error("failed to render reset email", "error", err)
		return
	}

	if err := gw.emailSender.Send(ctx, user.Email, "Reset your Gravix password", htmlBody, textBody); err != nil {
		slog.Error("failed to send reset email", "error", err, "to", user.Email)
	}

	gw.auditDirect(user.TenantID, user.ID, "user.password_reset_requested", "user", user.ID, "", r.RemoteAddr)
}

// handleResetPassword completes a password reset using a valid token.
func (gw *gateway) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Token == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "token and new_password are required")
		return
	}

	if err := password.Validate(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	hash := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(hash[:])

	resetToken, err := gw.db.PasswordResets().FindByTokenHash(ctx, tokenHash)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}

	if resetToken.UsedAt != nil {
		writeError(w, http.StatusBadRequest, "reset token has already been used")
		return
	}

	if time.Now().After(resetToken.ExpiresAt) {
		writeError(w, http.StatusBadRequest, "reset token has expired")
		return
	}

	// Hash the new password
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := gw.db.Users().UpdatePassword(ctx, resetToken.UserID, string(bcryptHash)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	if err := gw.db.PasswordResets().MarkUsed(ctx, resetToken.ID); err != nil {
		slog.Error("failed to mark reset token used", "error", err)
	}

	// Look up user for audit
	user, _ := gw.db.Users().GetByID(ctx, resetToken.UserID)
	if user != nil {
		gw.auditDirect(user.TenantID, user.ID, "user.password_reset", "user", user.ID, "", r.RemoteAddr)
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Password has been reset successfully. You can now log in.",
	})
}

// handleVerifyEmail verifies a user's email address using a verification token.
func (gw *gateway) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
		return
	}

	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		// Try JSON body for POST
		if r.Method == http.MethodPost {
			var req struct {
				Token string `json:"token"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			tokenStr = req.Token
		}
	}

	if tokenStr == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	ctx := r.Context()
	hash := sha256.Sum256([]byte(tokenStr))
	tokenHash := hex.EncodeToString(hash[:])

	verifyToken, err := gw.db.EmailVerifications().FindByTokenHash(ctx, tokenHash)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or expired verification token")
		return
	}

	if verifyToken.VerifiedAt != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"verified": true,
			"message":  "Email already verified.",
		})
		return
	}

	if time.Now().After(verifyToken.ExpiresAt) {
		writeError(w, http.StatusBadRequest, "verification token has expired, please request a new one")
		return
	}

	// Mark token as verified
	if err := gw.db.EmailVerifications().MarkVerified(ctx, verifyToken.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify email")
		return
	}

	// Mark user as verified
	if err := gw.db.Users().UpdateEmailVerified(ctx, verifyToken.UserID, true); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Create the initial API key now that email is verified
	user, _ := gw.db.Users().GetByID(ctx, verifyToken.UserID)
	var apiKeyPlain string
	if user != nil {
		plainKey, _, err := gw.db.APIKeys().Create(ctx, user.TenantID, "default", nil)
		if err != nil {
			slog.Error("failed to create API key after verification", "error", err)
		} else {
			apiKeyPlain = plainKey
		}
		gw.auditDirect(user.TenantID, user.ID, "user.email_verified", "user", user.ID, "", r.RemoteAddr)
	}

	resp := map[string]interface{}{
		"verified": true,
		"message":  "Email verified successfully.",
	}
	if apiKeyPlain != "" {
		resp["api_key"] = apiKeyPlain
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleResendVerification resends the email verification link (JWT required).
func (gw *gateway) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	user, err := gw.db.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	if user.EmailVerified {
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "Email is already verified.",
		})
		return
	}

	gw.sendVerificationEmail(ctx, user)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Verification email has been resent.",
	})
}

// requireAuth wraps a handler to require a valid JWT in the Authorization header.
func (gw *gateway) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := gw.tokens.Validate(tokenStr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		// Check token blacklist (logout revocation)
		if gw.isTokenBlacklisted(claims.ID) {
			writeError(w, http.StatusUnauthorized, "token has been revoked")
			return
		}

		// Store claims in request context
		r = r.WithContext(auth.WithClaims(r.Context(), claims))
		next(w, r)
	}
}

// handleMe returns current user and tenant info.
func (gw *gateway) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	// Fetch user for additional profile info
	user, _ := gw.db.Users().GetByEmail(r.Context(), claims.Email)
	result := map[string]interface{}{
		"user_id":        claims.UserID,
		"email":          claims.Email,
		"role":           claims.Role,
		"tenant_id":      claims.TenantID,
		"tenant_name":    tenant.Name,
		"plan":           tenant.Plan,
		"email_verified": false,
	}
	if user != nil {
		result["email_verified"] = user.EmailVerified
		result["status"] = user.Status
		if user.LastLoginAt != nil {
			result["last_login_at"] = user.LastLoginAt.Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, result)
}
