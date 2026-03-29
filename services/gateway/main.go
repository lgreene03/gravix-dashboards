// Gateway service provides tenant management, user login, API key management,
// and Stripe billing for multi-tenant Gravix deployments.
//
// Endpoints:
//
//	POST /api/gateway/login            — Authenticate user, return JWT
//	POST /api/gateway/register         — Create tenant + user + Stripe customer
//	GET  /api/gateway/me               — Get current user info (JWT required)
//	POST /api/gateway/api-keys         — Create API key (JWT required, admin only)
//	GET  /api/gateway/api-keys         — List API keys (JWT required)
//	DELETE /api/gateway/api-keys/:id   — Revoke API key (JWT required, admin only)
//	POST /api/gateway/webhooks/stripe  — Handle Stripe webhooks (no auth, signature verified)
//	POST /api/gateway/billing/portal   — Get Stripe Customer Portal URL (JWT required)
//	POST /api/gateway/billing/checkout — Create Stripe Checkout Session (JWT required, admin)
//	GET  /api/gateway/billing/usage    — Get current usage stats (JWT required)
//	GET  /live                         — Health check
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bytes"

	"bufio"
	"sort"
	"strconv"

	"archive/tar"
	"compress/gzip"
	"math"
	"sync"

	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/lgreene/gravix-dashboards/pkg/logging"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/billing"
	"github.com/lgreene/gravix-dashboards/pkg/captcha"
	"github.com/lgreene/gravix-dashboards/pkg/email"
	"github.com/lgreene/gravix-dashboards/pkg/notify"
	"github.com/lgreene/gravix-dashboards/pkg/password"
	"github.com/lgreene/gravix-dashboards/pkg/ratelimit"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/lgreene/gravix-dashboards/pkg/totp"
	"github.com/montanaflynn/stats"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/bcrypt"
)

// Prometheus metrics — names match PrometheusRule expressions in deploy/gravix/templates/prometheusrule.yaml
var (
	gatewayRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of gateway HTTP requests.",
		},
		[]string{"path", "method", "status"},
	)
	gatewayRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Duration of gateway HTTP requests in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
	gatewayRateLimitRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_rate_limit_rejected_total",
			Help: "Total number of requests rejected by rate limiting.",
		},
		[]string{"tenant_id"},
	)
	gatewayAuditErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_audit_errors_total",
			Help: "Total audit log write failures.",
		},
	)
	gatewayAlertNotificationErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_alert_notification_errors_total",
			Help: "Total alert notification delivery failures.",
		},
		[]string{"channel_type"},
	)
	gatewayAlertEvalErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_alert_eval_errors_total",
			Help: "Total alert evaluation errors.",
		},
	)
)

func init() {
	prometheus.MustRegister(gatewayRequestsTotal)
	prometheus.MustRegister(gatewayRequestDurationSeconds)
	prometheus.MustRegister(gatewayRateLimitRejectedTotal)
	prometheus.MustRegister(gatewayAuditErrorsTotal)
	prometheus.MustRegister(gatewayAlertNotificationErrorsTotal)
	prometheus.MustRegister(gatewayAlertEvalErrorsTotal)
	prometheus.MustRegister(collectors.NewBuildInfoCollector())
}

// metricsResponseWriter captures the HTTP status code for metrics recording.
type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *metricsResponseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// normalizePath maps dynamic URL segments to fixed route patterns to avoid
// high-cardinality Prometheus labels.
func normalizePath(path string) string {
	if strings.HasPrefix(path, "/api/gateway/api-keys/") {
		return "/api/gateway/api-keys/{id}"
	}
	if strings.HasPrefix(path, "/api/gateway/channels/") {
		return "/api/gateway/channels/{id}"
	}
	if strings.HasPrefix(path, "/api/gateway/alert-rules/") {
		return "/api/gateway/alert-rules/{id}"
	}
	return path
}

// metricsMiddleware records request count and duration for all gateway endpoints.
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &metricsResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		route := normalizePath(r.URL.Path)
		status := strconv.Itoa(rw.statusCode)
		gatewayRequestsTotal.WithLabelValues(route, r.Method, status).Inc()
		gatewayRequestDurationSeconds.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{"error": msg, "code": code})
}

func main() {
	logging.Init("gateway")

	port := flag.Int("port", 8091, "HTTP port")
	tenantDBPath := flag.String("tenant-db", "", "Path to tenant SQLite database")
	jwtSecret := flag.String("jwt-secret", "", "JWT signing secret")
	flag.Parse()

	if *tenantDBPath == "" {
		*tenantDBPath = os.Getenv("TENANT_DB_PATH")
	}
	if *jwtSecret == "" {
		*jwtSecret = os.Getenv("JWT_SECRET")
	}

	if *tenantDBPath == "" && os.Getenv("DB_DRIVER") == "" {
		slog.Error("TENANT_DB_PATH or DB_DRIVER is required")
		os.Exit(1)
	}
	if *jwtSecret == "" {
		slog.Error("JWT_SECRET is required")
		os.Exit(1)
	}
	if len(*jwtSecret) < 32 {
		slog.Error("JWT_SECRET must be at least 32 characters")
		os.Exit(1)
	}

	// Validate optional Stripe config
	if sk := os.Getenv("STRIPE_SECRET_KEY"); sk != "" && !strings.HasPrefix(sk, "sk_") {
		slog.Error("STRIPE_SECRET_KEY must start with 'sk_'")
		os.Exit(1)
	}
	if wh := os.Getenv("STRIPE_WEBHOOK_SECRET"); wh != "" && !strings.HasPrefix(wh, "whsec_") {
		slog.Error("STRIPE_WEBHOOK_SECRET must start with 'whsec_'")
		os.Exit(1)
	}

	// Validate CUBE_API_URL if set
	if cubeEnv := os.Getenv("CUBE_API_URL"); cubeEnv != "" && !strings.HasPrefix(cubeEnv, "http") {
		slog.Error("CUBE_API_URL must be a valid HTTP URL", "value", cubeEnv)
		os.Exit(1)
	}

	var db tenantdb.DB
	if dbDriver := os.Getenv("DB_DRIVER"); dbDriver != "" {
		var err error
		db, err = tenantdb.OpenFromEnv()
		if err != nil {
			slog.Error("failed to open tenant database", "error", err)
			os.Exit(1)
		}
		slog.Info("tenant database opened", "driver", dbDriver)
	} else {
		var err error
		db, err = tenantdb.Open(*tenantDBPath)
		if err != nil {
			slog.Error("failed to open tenant database", "error", err)
			os.Exit(1)
		}
	}
	defer db.Close()

	tokens := auth.NewTokenService(*jwtSecret, 24*time.Hour)

	cubeURL := os.Getenv("CUBE_API_URL")
	if cubeURL == "" {
		cubeURL = "http://localhost:4000/cubejs-api/v1/load"
	}

	// Initialize object store for DLQ access
	rawDir := os.Getenv("RAW_DATA_DIR")
	if rawDir == "" {
		rawDir = "./data/raw"
	}
	objStore, err := storage.NewLocalStore(rawDir)
	if err != nil {
		slog.Warn("DLQ store init failed, DLQ endpoints will be unavailable", "error", err)
	} else {
		slog.Info("DLQ store initialized", "dir", rawDir)
	}

	// Per-tenant rate limiting (fallback 100/s for legacy mode)
	trl := ratelimit.NewTenantLimiter(100, 200)
	defer trl.Close()

	// Per-IP rate limiting: 10 requests/minute, burst of 5 for auth endpoints
	iprl := ratelimit.NewIPLimiter(10, 5)
	defer iprl.Close()

	gw := &gateway{
		db:              db,
		tokens:          tokens,
		notifier:        notify.NewDispatcher(),
		store:           objStore,
		rateLimiter:     trl,
		ipLimiter:       iprl,
		emailSender:     email.NewSenderFromEnv(),
		captchaVerifier: captcha.NewVerifierFromEnv(),
		cubeAPIURL:      cubeURL,
		jwtSecret:       *jwtSecret,
		baseURL:         email.BaseURLFromEnv(),
		activeExports:   make(map[string]bool),
	}

	// Master context for background goroutines — cancelled on shutdown
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	// Start alert evaluator background loop
	go gw.alertEvaluatorLoop(bgCtx)

	// Initialize Stripe billing if configured
	stripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if stripeKey != "" {
		plans := billing.DefaultPlans(
			os.Getenv("STRIPE_PRICE_FREE"),
			os.Getenv("STRIPE_PRICE_TEAM"),
			os.Getenv("STRIPE_PRICE_BUSINESS"),
			os.Getenv("STRIPE_PRICE_SCALE"),
			os.Getenv("STRIPE_PRICE_ENTERPRISE"),
		)
		gw.billing = billing.NewStripeService(
			stripeKey,
			os.Getenv("STRIPE_WEBHOOK_SECRET"),
			plans,
		)
		slog.Info("stripe billing enabled")

		// Start background usage metering (hourly)
		go gw.usageMeteringLoop(bgCtx)
	} else {
		slog.Info("stripe billing disabled, STRIPE_SECRET_KEY not set")
	}

	// Body size limit: 1MB default, configurable
	maxBodyBytes := int64(1 << 20) // 1MB
	if envMax := os.Getenv("GATEWAY_MAX_BODY_BYTES"); envMax != "" {
		if v, err := strconv.ParseInt(envMax, 10, 64); err == nil && v > 0 {
			maxBodyBytes = v
		}
	}
	bodyLimit := bodyLimitMiddleware(maxBodyBytes)

	mux := http.NewServeMux()
	// Public auth endpoints — IP rate limited + body limited
	mux.HandleFunc("/api/gateway/login", gw.ipRateLimitMiddleware(bodyLimit(gw.handleLogin)))
	mux.HandleFunc("/api/gateway/register", gw.ipRateLimitMiddleware(bodyLimit(gw.handleRegister)))
	mux.HandleFunc("/api/gateway/forgot-password", gw.ipRateLimitMiddleware(bodyLimit(gw.handleForgotPassword)))
	mux.HandleFunc("/api/gateway/reset-password", gw.ipRateLimitMiddleware(bodyLimit(gw.handleResetPassword)))
	mux.HandleFunc("/api/gateway/verify-email", gw.handleVerifyEmail)
	mux.HandleFunc("/api/gateway/resend-verification", gw.requireAuth(gw.ipRateLimitMiddleware(gw.handleResendVerification)))
	mux.HandleFunc("/api/gateway/logout", gw.requireAuth(gw.handleLogout))
	mux.HandleFunc("/api/gateway/password", gw.requireAuth(bodyLimit(gw.handleChangePassword)))
	mux.HandleFunc("/api/gateway/refresh", gw.requireAuth(gw.handleRefreshToken))
	mux.HandleFunc("/api/gateway/webhooks/stripe", gw.handleStripeWebhook)

	// Authenticated + rate-limited API endpoints
	mux.HandleFunc("/api/gateway/me", gw.requireAuth(gw.rateLimitMiddleware(gw.handleMe)))
	mux.HandleFunc("/api/gateway/api-keys", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAPIKeys)))
	mux.HandleFunc("/api/gateway/api-keys/expiring", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAPIKeysExpiring)))
	mux.HandleFunc("/api/gateway/billing/portal", gw.requireAuth(gw.rateLimitMiddleware(gw.handleBillingPortal)))
	mux.HandleFunc("/api/gateway/billing/checkout", gw.requireAuth(gw.rateLimitMiddleware(gw.handleBillingCheckout)))
	mux.HandleFunc("/api/gateway/billing/usage", gw.requireAuth(gw.rateLimitMiddleware(gw.cacheableHandler(gw.handleBillingUsage))))
	mux.HandleFunc("/api/gateway/billing/invoices", gw.requireAuth(gw.rateLimitMiddleware(gw.handleBillingInvoices)))
	mux.HandleFunc("/api/gateway/billing/usage/history", gw.requireAuth(gw.rateLimitMiddleware(gw.cacheableHandler(gw.handleBillingUsageHistory))))
	mux.HandleFunc("/api/gateway/channels", gw.requireAuth(gw.rateLimitMiddleware(gw.handleChannels)))
	mux.HandleFunc("/api/gateway/channels/", gw.requireAuth(gw.rateLimitMiddleware(gw.handleChannelByID)))
	mux.HandleFunc("/api/gateway/alert-rules", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAlertRules)))
	mux.HandleFunc("/api/gateway/alert-rules/", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAlertRuleByID)))
	mux.HandleFunc("/api/gateway/alert-history", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAlertHistory)))
	mux.HandleFunc("/api/gateway/traces/recent", gw.requireAuth(gw.rateLimitMiddleware(gw.cacheableHandler(gw.handleTracesRecent))))
	mux.HandleFunc("/api/gateway/traces", gw.requireAuth(gw.rateLimitMiddleware(gw.cacheableHandler(gw.handleTraceByID))))
	mux.HandleFunc("/api/gateway/dlq", gw.requireAuth(gw.rateLimitMiddleware(gw.handleDLQ)))
	mux.HandleFunc("/api/gateway/dlq/replay", gw.requireAuth(gw.rateLimitMiddleware(gw.handleDLQReplay)))
	mux.HandleFunc("/api/gateway/audit-log", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAuditLog)))
	mux.HandleFunc("/api/gateway/retention", gw.requireAuth(gw.rateLimitMiddleware(gw.handleRetention)))
	mux.HandleFunc("/api/gateway/export", gw.requireAuth(gw.rateLimitMiddleware(gw.handleExport)))
	mux.HandleFunc("/api/gateway/invitations", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleInvitations))))
	mux.HandleFunc("/api/gateway/invitations/accept", gw.ipRateLimitMiddleware(bodyLimit(gw.handleAcceptInvitation)))
	mux.HandleFunc("/api/gateway/team", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleTeam))))
	mux.HandleFunc("/api/gateway/gdpr/access", gw.requireAuth(gw.rateLimitMiddleware(gw.handleGDPRAccess)))
	mux.HandleFunc("/api/gateway/gdpr/portability", gw.requireAuth(gw.rateLimitMiddleware(gw.handleGDPRPortability)))
	mux.HandleFunc("/api/gateway/consent", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleConsent))))
	mux.HandleFunc("/api/gateway/account", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleAccountDeletion))))
	mux.HandleFunc("/api/gateway/account/cancel-deletion", gw.requireAuth(gw.rateLimitMiddleware(gw.handleCancelDeletion)))
	mux.HandleFunc("/api/gateway/openapi.json", gw.handleOpenAPI)

	// Enterprise features (Phase 31)
	mux.HandleFunc("/api/gateway/sso", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleSSOConfig))))
	mux.HandleFunc("/api/gateway/sso/login", gw.ipRateLimitMiddleware(gw.handleSSOLogin))
	mux.HandleFunc("/api/gateway/sso/callback", gw.ipRateLimitMiddleware(gw.handleSSOCallback))
	mux.HandleFunc("/api/gateway/2fa/setup", gw.requireAuth(gw.rateLimitMiddleware(gw.handleTwoFactorSetup)))
	mux.HandleFunc("/api/gateway/2fa/confirm", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleTwoFactorConfirm))))
	mux.HandleFunc("/api/gateway/2fa/disable", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleTwoFactorDisable))))
	mux.HandleFunc("/api/gateway/sessions", gw.requireAuth(gw.rateLimitMiddleware(gw.handleSessions)))
	mux.HandleFunc("/api/gateway/orgs", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleMultiOrg))))

	// Growth features (Phase 32)
	mux.HandleFunc("/api/gateway/referrals", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleReferrals))))
	mux.HandleFunc("/api/gateway/referrals/redeem", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleRedeemReferral))))
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]string{
			"db": "ok",
		}
		ready := true

		// DB probe
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := gw.db.Tenants().List(ctx); err != nil {
			status["db"] = "unavailable"
			ready = false
		}

		// Stripe probe (optional)
		if os.Getenv("STRIPE_SECRET_KEY") != "" {
			status["stripe"] = "configured"
		}

		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(status)
	})
	mux.Handle("/metrics", promhttp.Handler())

	// Metrics middleware wraps CORS so all responses (including OPTIONS) are counted
	handler := logging.RequestIDMiddleware(metricsMiddleware(securityHeadersMiddleware(mux)))

	addr := fmt.Sprintf(":%d", *port)
	if envAddr := os.Getenv("GATEWAY_ADDR"); envAddr != "" {
		addr = envAddr
	}
	srv := &http.Server{
		Addr:           addr,
		Handler:        handler,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	// Graceful shutdown: listen for SIGINT/SIGTERM
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-shutdownCh
		slog.Info("received shutdown signal, draining connections", "signal", sig.String(), "timeout", "10s")
		bgCancel() // stop background goroutines
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("HTTP shutdown error", "error", err)
		}
	}()

	slog.Info("gateway service starting", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
	slog.Info("gateway stopped gracefully")
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	// CORS: comma-separated list of allowed origins, or "*" for dev
	allowedOrigins := os.Getenv("CORS_ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = os.Getenv("CORS_ALLOWED_ORIGIN") // backward compat
	}
	if allowedOrigins == "" {
		allowedOrigins = "*"
	}
	originSet := make(map[string]bool)
	allowAll := false
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o == "*" {
			allowAll = true
		}
		originSet[o] = true
	}

	// CSP: configurable connect-src and script-src
	cspConnectSrc := os.Getenv("CSP_CONNECT_SRC")
	if cspConnectSrc == "" {
		cspConnectSrc = "'self' http://localhost:4000 http://localhost:8091"
	}
	cspScriptSrc := os.Getenv("CSP_SCRIPT_SRC")
	if cspScriptSrc == "" {
		cspScriptSrc = "'self' 'unsafe-inline' https://cdn.jsdelivr.net"
	}

	csp := fmt.Sprintf(
		"default-src 'self'; script-src %s; style-src 'self' 'unsafe-inline'; connect-src %s; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
		cspScriptSrc, cspConnectSrc,
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS: match Origin header against allowed list
		origin := r.Header.Get("Origin")
		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && originSet[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		// If origin doesn't match and not allowAll, no ACAO header is set (browser blocks)

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", csp)

		// HSTS only when behind TLS-terminating proxy
		if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type gateway struct {
	db              tenantdb.DB
	tokens          *auth.TokenService
	billing         billing.Service // nil if Stripe is not configured
	notifier        *notify.Dispatcher
	store           storage.ObjectStore // for DLQ reads
	rateLimiter     *ratelimit.TenantLimiter
	ipLimiter       *ratelimit.IPLimiter // per-IP rate limiting for auth endpoints
	emailSender     email.Sender
	captchaVerifier captcha.Verifier
	cubeAPIURL      string
	jwtSecret       string
	baseURL         string // for email links (e.g., https://app.gravix.io)
	tokenBlacklist  sync.Map // JTI → expiry time for revoked tokens
	activeExports   map[string]bool // tracks in-progress exports per tenant
	activeExportsMu sync.Mutex
}

// ipRateLimitMiddleware applies per-IP rate limiting. Returns 429 with Retry-After header when exceeded.
func (gw *gateway) ipRateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := ratelimit.ExtractIP(r)
		if !gw.ipLimiter.Allow(ip) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "too many requests, please try again later")
			return
		}
		next(w, r)
	}
}

// bodyLimitMiddleware wraps the request body with a size limit.
func bodyLimitMiddleware(maxBytes int64) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next(w, r)
		}
	}
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

	// Store the JTI with its expiry so we can clean up later
	gw.tokenBlacklist.Store(claims.ID, claims.ExpiresAt.Time)

	gw.auditDirect(claims.TenantID, claims.UserID, "user.logout", "user", claims.UserID, "", r.RemoteAddr)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logged out successfully",
	})
}

// isTokenBlacklisted checks if a JTI has been revoked.
func (gw *gateway) isTokenBlacklisted(jti string) bool {
	if jti == "" {
		return false
	}
	val, ok := gw.tokenBlacklist.Load(jti)
	if !ok {
		return false
	}
	// Clean up expired blacklist entries
	if expiry, ok := val.(time.Time); ok && time.Now().After(expiry) {
		gw.tokenBlacklist.Delete(jti)
		return false
	}
	return true
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

	// Blacklist the old token so it can't be reused
	if claims.ID != "" {
		gw.tokenBlacklist.Store(claims.ID, claims.ExpiresAt.Time)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     newToken,
		"tenant_id": claims.TenantID,
		"user_id":   claims.UserID,
		"email":     claims.Email,
		"role":      claims.Role,
	})
}

// seatLimit returns the max users per plan.
func seatLimit(plan string) int {
	return billing.PlanSeatLimit(plan)
}

// handleInvitations manages team invitations (POST to send, GET to list).
func (gw *gateway) handleInvitations(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		invitations, err := gw.db.Invitations().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invitations")
			return
		}
		if invitations == nil {
			invitations = []*tenantdb.Invitation{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"invitations": invitations,
		})

	case http.MethodPost:
		if !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		var req struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Email == "" {
			writeError(w, http.StatusBadRequest, "email is required")
			return
		}
		if req.Role == "" {
			req.Role = "viewer"
		}
		if req.Role != "editor" && req.Role != "viewer" && req.Role != "admin" {
			writeError(w, http.StatusBadRequest, "role must be viewer, editor, or admin")
			return
		}

		// Check seat limits
		tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		userCount, _ := gw.db.Users().CountByTenant(r.Context(), claims.TenantID)
		limit := seatLimit(tenant.Plan)
		if userCount >= limit {
			writeError(w, http.StatusForbidden, fmt.Sprintf("seat limit reached (%d/%d). Upgrade your plan.", userCount, limit))
			return
		}

		// Check if email is already a member
		if existingUser, _ := gw.db.Users().GetByEmail(r.Context(), req.Email); existingUser != nil {
			writeError(w, http.StatusConflict, "user already exists with this email")
			return
		}

		// Generate invite token
		plainToken, tokenHash, err := generateSecureToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate invite token")
			return
		}

		inv := &tenantdb.Invitation{
			TenantID:  claims.TenantID,
			Email:     req.Email,
			Role:      req.Role,
			TokenHash: tokenHash,
			Status:    "pending",
			InvitedBy: claims.UserID,
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour), // 7 days
		}
		if err := gw.db.Invitations().Create(r.Context(), inv); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create invitation")
			return
		}

		// Send invitation email
		inviteURL := gw.baseURL + "/accept-invite?token=" + plainToken
		go func() {
			subject := "You've been invited to join " + tenant.Name + " on Gravix"
			body := fmt.Sprintf(`<h2>Team Invitation</h2>
<p>You've been invited to join <strong>%s</strong> on Gravix as a <strong>%s</strong>.</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#3b82f6;color:white;text-decoration:none;border-radius:8px;font-weight:600;">Accept Invitation</a></p>
<p>This invitation expires in 7 days.</p>
<p style="color:#64748b;font-size:0.875rem;">If you didn't expect this invitation, you can safely ignore this email.</p>`,
				tenant.Name, req.Role, inviteURL)

			textBody := fmt.Sprintf("You've been invited to join %s on Gravix as a %s.\n\nAccept: %s\n\nThis invitation expires in 7 days.", tenant.Name, req.Role, inviteURL)
			if err := gw.emailSender.Send(context.Background(), req.Email, subject, body, textBody); err != nil {
				slog.Error("failed to send invitation email", "email", req.Email, "error", err)
			}
		}()

		gw.auditDirect(claims.TenantID, claims.UserID, "team.invite_sent", "invitation", inv.ID, req.Email, r.RemoteAddr)

		writeJSON(w, http.StatusCreated, map[string]string{
			"id":      inv.ID,
			"message": "invitation sent",
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

// handleAcceptInvitation accepts a team invite and creates the user account.
// POST /api/gateway/invitations/accept  body: {"token": "...", "password": "..."}
func (gw *gateway) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Token == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "token and password are required")
		return
	}

	if err := password.Validate(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Find invitation by token hash
	hash := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(hash[:])
	inv, err := gw.db.Invitations().FindByTokenHash(r.Context(), tokenHash)
	if err != nil {
		writeError(w, http.StatusNotFound, "invitation not found or expired")
		return
	}

	if inv.Status != "pending" {
		writeError(w, http.StatusBadRequest, "invitation has already been used")
		return
	}
	if time.Now().After(inv.ExpiresAt) {
		writeError(w, http.StatusBadRequest, "invitation has expired")
		return
	}

	// Check if email is already registered
	if existingUser, _ := gw.db.Users().GetByEmail(r.Context(), inv.Email); existingUser != nil {
		writeError(w, http.StatusConflict, "a user with this email already exists")
		return
	}

	// Create user
	user := &tenantdb.User{
		TenantID:      inv.TenantID,
		Email:         inv.Email,
		PasswordHash:  req.Password, // will be hashed by repo
		Role:          inv.Role,
		EmailVerified: true, // invited users are pre-verified
	}
	if err := gw.db.Users().Create(r.Context(), user); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	// Mark invitation as accepted
	_ = gw.db.Invitations().MarkAccepted(r.Context(), inv.ID)

	// Generate JWT
	token, err := gw.tokens.Generate(inv.TenantID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     token,
		"tenant_id": inv.TenantID,
		"user_id":   user.ID,
		"email":     user.Email,
		"role":      user.Role,
	})
}

// handleTeam manages team members (GET to list, PUT to update role/status).
func (gw *gateway) handleTeam(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		users, err := gw.db.Users().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list team members")
			return
		}
		// Sanitize — don't expose password hashes
		type teamMember struct {
			ID            string  `json:"id"`
			Email         string  `json:"email"`
			Role          string  `json:"role"`
			Status        string  `json:"status"`
			EmailVerified bool    `json:"email_verified"`
			LastLoginAt   *string `json:"last_login_at,omitempty"`
		}
		members := make([]teamMember, 0, len(users))
		for _, u := range users {
			m := teamMember{
				ID:            u.ID,
				Email:         u.Email,
				Role:          u.Role,
				Status:        u.Status,
				EmailVerified: u.EmailVerified,
			}
			if u.LastLoginAt != nil {
				s := u.LastLoginAt.Format(time.RFC3339)
				m.LastLoginAt = &s
			}
			members = append(members, m)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"members": members,
		})

	case http.MethodPut:
		if !claims.HasRole(auth.RoleAdmin) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		var req struct {
			UserID string `json:"user_id"`
			Role   string `json:"role,omitempty"`
			Status string `json:"status,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		// Don't allow admins to modify themselves
		if req.UserID == claims.UserID {
			writeError(w, http.StatusBadRequest, "cannot modify your own role/status")
			return
		}

		// Verify user belongs to same tenant
		targetUser, err := gw.db.Users().GetByID(r.Context(), req.UserID)
		if err != nil || targetUser.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}

		if req.Role != "" {
			if req.Role != "admin" && req.Role != "editor" && req.Role != "viewer" {
				writeError(w, http.StatusBadRequest, "role must be admin, editor, or viewer")
				return
			}
			if err := gw.db.Users().UpdateRole(r.Context(), req.UserID, req.Role); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update role")
				return
			}
			gw.auditDirect(claims.TenantID, claims.UserID, "team.role_changed",
				"user", req.UserID, fmt.Sprintf("role=%s", req.Role), r.RemoteAddr)
		}
		if req.Status != "" {
			if req.Status != "active" && req.Status != "deactivated" {
				writeError(w, http.StatusBadRequest, "status must be active or deactivated")
				return
			}
			if err := gw.db.Users().UpdateStatus(r.Context(), req.UserID, req.Status); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update status")
				return
			}
			gw.auditDirect(claims.TenantID, claims.UserID, "team.status_changed",
				"user", req.UserID, fmt.Sprintf("status=%s", req.Status), r.RemoteAddr)
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "user updated"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or PUT required")
	}
}

// requireRole returns middleware that checks if the authenticated user has one of the given roles.
// Must be used inside requireAuth (assumes claims are in context).
func (gw *gateway) requireRole(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims := auth.ClaimsFromContext(r.Context())
			if claims == nil || !claims.HasRole(roles...) {
				writeError(w, http.StatusForbidden, fmt.Sprintf("requires one of roles: %v", roles))
				return
			}
			next(w, r)
		}
	}
}

// planRank maps plan names to a numeric rank for comparison.
var planRank = map[string]int{
	"free":    0,
	"starter": 1,
	"pro":     2,
}

// requirePlan returns middleware that checks if the tenant's plan meets the minimum.
// Must be used inside requireAuth.
func (gw *gateway) requirePlan(minPlan string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims := auth.ClaimsFromContext(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
			if err != nil {
				writeError(w, http.StatusNotFound, "tenant not found")
				return
			}
			if planRank[tenant.Plan] < planRank[minPlan] {
				writeError(w, http.StatusForbidden,
					fmt.Sprintf("upgrade required: %s plan needed (current: %s)", minPlan, tenant.Plan))
				return
			}
			next(w, r)
		}
	}
}

// rateLimitMiddleware checks tenant-specific rate limits on API requests.
// Must be used inside requireAuth (assumes claims are in context).
// Returns 429 with standard X-RateLimit headers when exceeded.
func (gw *gateway) rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		var tenantID, plan string
		if claims != nil {
			tenantID = claims.TenantID
			// Look up tenant plan
			if tenant, err := gw.db.Tenants().GetByID(r.Context(), tenantID); err == nil {
				plan = tenant.Plan
			}
		}

		if !gw.rateLimiter.Allow(tenantID, plan) {
			gatewayRateLimitRejectedTotal.WithLabelValues(tenantID).Inc()
			// Set rate limit headers
			rl := gw.rateLimiter.Get(tenantID)
			if rl != nil {
				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", rl.Max()))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Second).Unix()))
			}
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later")
			return
		}

		// Set rate limit headers on successful requests too
		rl := gw.rateLimiter.Get(tenantID)
		if rl != nil {
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", rl.Max()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", rl.Remaining()))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Second).Unix()))
		}

		next(w, r)
	}
}

// cacheableHandler wraps a handler with Cache-Control headers for analytics endpoints.
// Allows clients to use stale data while revalidating in the background.
func (gw *gateway) cacheableHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "max-age=60, stale-while-revalidate=300")
		}
		next(w, r)
	}
}

// audit records an immutable audit entry for a mutation. Runs async (fire-and-forget)
// so it never blocks the HTTP request path. Errors are logged but not propagated.
func (gw *gateway) audit(r *http.Request, action, resource, resourceID, detail string) {
	claims := auth.ClaimsFromContext(r.Context())
	entry := &tenantdb.AuditEntry{
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Detail:     detail,
		IPAddress:  r.RemoteAddr,
	}
	if claims != nil {
		entry.TenantID = claims.TenantID
		entry.UserID = claims.UserID
	}
	go func() {
		if err := gw.db.AuditLog().Log(context.Background(), entry); err != nil {
			slog.Error("audit log error", "error", err)
			gatewayAuditErrorsTotal.Inc()
		}
	}()
}

// auditDirect records an audit entry with explicit tenant/user IDs.
// Used by pre-auth endpoints (register) and system actions (webhooks).
func (gw *gateway) auditDirect(tenantID, userID, action, resource, resourceID, detail, ip string) {
	entry := &tenantdb.AuditEntry{
		TenantID:   tenantID,
		UserID:     userID,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Detail:     detail,
		IPAddress:  ip,
	}
	go func() {
		if err := gw.db.AuditLog().Log(context.Background(), entry); err != nil {
			slog.Error("audit log error", "error", err)
			gatewayAuditErrorsTotal.Inc()
		}
	}()
}

// handleAuditLog lists audit entries for the authenticated tenant (admin only).
func (gw *gateway) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if !claims.HasRole(auth.RoleAdmin) {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	entries, total, err := gw.db.AuditLog().ListByTenant(r.Context(), claims.TenantID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit log")
		return
	}
	if entries == nil {
		entries = []*tenantdb.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// planMinRetentionDays returns the minimum allowed retention in days for a given plan.
// Custom retention policies cannot go below these minimums.
func planMinRetentionDays(plan string) int {
	switch plan {
	case "free":
		return 1
	case "starter":
		return 7
	case "pro":
		return 7
	default:
		return 7
	}
}

// planMaxRetentionDays returns the maximum allowed retention in days for a given plan.
func planMaxRetentionDays(plan string) int {
	switch plan {
	case "free":
		return 7
	case "starter":
		return 30
	case "pro":
		return 365
	default:
		return 90
	}
}

// handleRetention handles GET/PUT for per-tenant retention policies.
func (gw *gateway) handleRetention(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if !claims.HasRole(auth.RoleAdmin) {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		policy, err := gw.db.RetentionPolicies().GetByTenantID(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get retention policy")
			return
		}

		// Get tenant for plan info
		tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get tenant")
			return
		}

		resp := map[string]interface{}{
			"plan":        tenant.Plan,
			"min_days":    planMinRetentionDays(tenant.Plan),
			"max_days":    planMaxRetentionDays(tenant.Plan),
			"plan_default_facts_days": planDefaultFactsDays(tenant.Plan),
		}
		if policy != nil {
			resp["facts_days"] = policy.FactsDays
			resp["metrics_days"] = policy.MetricsDays
			resp["traces_days"] = policy.TracesDays
			resp["updated_at"] = policy.UpdatedAt
		}
		writeJSON(w, http.StatusOK, resp)

	case http.MethodPut:
		var req struct {
			FactsDays   int `json:"facts_days"`
			MetricsDays int `json:"metrics_days"`
			TracesDays  int `json:"traces_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Get tenant for plan-based validation
		tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get tenant")
			return
		}

		minDays := planMinRetentionDays(tenant.Plan)
		maxDays := planMaxRetentionDays(tenant.Plan)

		// Validate: 0 means "use default", otherwise must be within plan bounds
		for _, pair := range []struct {
			name string
			val  int
		}{
			{"facts_days", req.FactsDays},
			{"metrics_days", req.MetricsDays},
			{"traces_days", req.TracesDays},
		} {
			if pair.val != 0 && (pair.val < minDays || pair.val > maxDays) {
				writeError(w, http.StatusBadRequest,
					fmt.Sprintf("%s must be 0 (default) or between %d and %d for %s plan",
						pair.name, minDays, maxDays, tenant.Plan))
				return
			}
		}

		policy := &tenantdb.RetentionPolicy{
			TenantID:    claims.TenantID,
			FactsDays:   req.FactsDays,
			MetricsDays: req.MetricsDays,
			TracesDays:  req.TracesDays,
		}
		if err := gw.db.RetentionPolicies().Upsert(r.Context(), policy); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save retention policy")
			return
		}

		// Audit log
		detail := fmt.Sprintf(`{"facts_days":%d,"metrics_days":%d,"traces_days":%d}`,
			req.FactsDays, req.MetricsDays, req.TracesDays)
		gw.db.AuditLog().Log(r.Context(), &tenantdb.AuditEntry{
			TenantID:   claims.TenantID,
			UserID:     claims.UserID,
			Action:     "retention.update",
			Resource:   "retention_policy",
			ResourceID: claims.TenantID,
			Detail:     detail,
		})

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"facts_days":   policy.FactsDays,
			"metrics_days": policy.MetricsDays,
			"traces_days":  policy.TracesDays,
			"updated_at":   policy.UpdatedAt,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or PUT required")
	}
}

// planDefaultFactsDays returns the default facts retention for a plan.
func planDefaultFactsDays(plan string) int {
	switch plan {
	case "free":
		return 7
	case "starter":
		return 30
	case "pro":
		return 90
	default:
		return 30
	}
}

// handleExport streams a tar.gz archive of raw JSONL files for a date range.
// Limited to 1 concurrent export per tenant, max 30-day range.
func (gw *gateway) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())

	var req struct {
		StartDate string `json:"start_date"` // YYYY-MM-DD
		EndDate   string `json:"end_date"`   // YYYY-MM-DD
		DataType  string `json:"data_type"`  // request_facts, service_events (default: request_facts)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.DataType == "" {
		req.DataType = "request_facts"
	}
	if req.DataType != "request_facts" && req.DataType != "service_events" {
		writeError(w, http.StatusBadRequest, "data_type must be request_facts or service_events")
		return
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "start_date must be YYYY-MM-DD format")
		return
	}
	endDate, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "end_date must be YYYY-MM-DD format")
		return
	}

	if endDate.Before(startDate) {
		writeError(w, http.StatusBadRequest, "end_date must be after start_date")
		return
	}

	daySpan := int(endDate.Sub(startDate).Hours()/24) + 1
	if daySpan > 30 {
		writeError(w, http.StatusBadRequest, "export range cannot exceed 30 days")
		return
	}

	// Enforce 1 concurrent export per tenant
	gw.activeExportsMu.Lock()
	if gw.activeExports[claims.TenantID] {
		gw.activeExportsMu.Unlock()
		writeError(w, http.StatusTooManyRequests, "an export is already in progress for this tenant")
		return
	}
	gw.activeExports[claims.TenantID] = true
	gw.activeExportsMu.Unlock()
	defer func() {
		gw.activeExportsMu.Lock()
		delete(gw.activeExports, claims.TenantID)
		gw.activeExportsMu.Unlock()
	}()

	// Collect files matching the date range
	prefix := fmt.Sprintf("raw/%s/%s", claims.TenantID, req.DataType)
	keys, err := gw.store.List(r.Context(), prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list files")
		return
	}

	var matchedKeys []string
	for _, key := range keys {
		for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
			dateStr := d.Format("2006-01-02")
			if strings.Contains(key, dateStr) {
				matchedKeys = append(matchedKeys, key)
				break
			}
		}
	}

	if len(matchedKeys) == 0 {
		writeError(w, http.StatusNotFound, "no data found for the specified date range")
		return
	}

	// Audit log
	detail := fmt.Sprintf(`{"data_type":"%s","start_date":"%s","end_date":"%s","file_count":%d}`,
		req.DataType, req.StartDate, req.EndDate, len(matchedKeys))
	gw.db.AuditLog().Log(r.Context(), &tenantdb.AuditEntry{
		TenantID:   claims.TenantID,
		UserID:     claims.UserID,
		Action:     "data.export",
		Resource:   req.DataType,
		ResourceID: claims.TenantID,
		Detail:     detail,
	})

	// Stream tar.gz response
	filename := fmt.Sprintf("gravix-export-%s-%s-to-%s.tar.gz", req.DataType, req.StartDate, req.EndDate)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)

	gzw := gzip.NewWriter(w)
	defer gzw.Close()
	tw := tar.NewWriter(gzw)
	defer tw.Close()

	for _, key := range matchedKeys {
		rc, err := gw.store.Get(r.Context(), key)
		if err != nil {
			slog.Error("export: failed to read file", "key", key, "error", err)
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			slog.Error("export: failed to read file data", "key", key, "error", err)
			continue
		}

		hdr := &tar.Header{
			Name:    key,
			Size:    int64(len(data)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			slog.Error("export: failed to write tar header", "key", key, "error", err)
			return
		}
		if _, err := tw.Write(data); err != nil {
			slog.Error("export: failed to write tar data", "key", key, "error", err)
			return
		}
	}

	slog.Info("export complete", "tenant_id", claims.TenantID, "data_type", req.DataType,
		"start", req.StartDate, "end", req.EndDate, "files", len(matchedKeys))
}

// handleLogin authenticates a user with email/password and returns a JWT.
func (gw *gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := gw.db.Users().GetByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
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

	// Check if 2FA is enabled — require TOTP code
	if user.TwoFactorEnabled {
		var totpCode string
		// Check if TOTP code was provided in the login request
		var loginReq struct {
			TOTPCode string `json:"totp_code"`
		}
		// Re-parse won't work since body is consumed; check the original req fields
		// The totp_code should be provided as a query param or we return requires_2fa
		totpCode = r.URL.Query().Get("totp_code")
		_ = loginReq // suppress unused
		if totpCode == "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"requires_2fa": true,
				"user_id":      user.ID,
				"message":      "provide totp_code to complete login",
			})
			return
		}

		encKey := gw.totpEncryptionKey()
		secret, decErr := totp.DecryptSecret(user.TwoFactorSecret, encKey)
		if decErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate 2FA")
			return
		}
		if !totp.Validate(totpCode, secret) {
			writeError(w, http.StatusUnauthorized, "invalid TOTP code")
			return
		}
	}

	token, err := gw.tokens.Generate(user.TenantID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

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

// handleAPIKeys handles CRUD for API keys.
func (gw *gateway) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		keys, err := gw.db.APIKeys().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list keys")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})

	case http.MethodPost:
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		// Block unverified users from creating API keys
		user, err := gw.db.Users().GetByID(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get user")
			return
		}
		if !user.EmailVerified {
			writeError(w, http.StatusForbidden, "email verification required before creating API keys")
			return
		}

		var req struct {
			Name          string `json:"name"`
			ExpiresInDays *int   `json:"expires_in_days,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}

		var expiresAt *time.Time
		if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
			t := time.Now().UTC().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
			expiresAt = &t
		}

		plain, key, err := gw.db.APIKeys().Create(r.Context(), claims.TenantID, req.Name, expiresAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create key")
			return
		}

		gw.audit(r, "api_key.create", "api_key", key.ID,
			fmt.Sprintf(`{"name":%q,"prefix":%q}`, key.Name, key.KeyPrefix))

		resp := map[string]interface{}{
			"key":        plain,
			"key_id":     key.ID,
			"key_prefix": key.KeyPrefix,
			"name":       key.Name,
		}
		if key.ExpiresAt != nil {
			resp["expires_at"] = key.ExpiresAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusCreated, resp)

	case http.MethodDelete:
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}

		// Extract key ID from path: /api/gateway/api-keys/<id>
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 || parts[4] == "" {
			writeError(w, http.StatusBadRequest, "key ID required in path")
			return
		}
		keyID := parts[4]

		if err := gw.db.APIKeys().Revoke(r.Context(), keyID); err != nil {
			writeError(w, http.StatusNotFound, "key not found or already revoked")
			return
		}

		gw.audit(r, "api_key.revoke", "api_key", keyID, `{}`)

		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAPIKeysExpiring returns API keys expiring within 7 days.
func (gw *gateway) handleAPIKeysExpiring(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	keys, err := gw.db.APIKeys().ListExpiringSoon(r.Context(), claims.TenantID, 7)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list expiring keys")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})
}

// handleStripeWebhook processes Stripe webhook events.
// No JWT auth — signature is verified by the billing service.
func (gw *gateway) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if gw.billing == nil {
		writeError(w, http.StatusServiceUnavailable, "billing not configured")
		return
	}

	// Read body (Stripe recommends max 65536 bytes)
	payload, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	sigHeader := r.Header.Get("Stripe-Signature")
	event, err := gw.billing.ParseWebhook(payload, sigHeader)
	if err != nil {
		slog.Error("webhook signature verification failed", "error", err)
		writeError(w, http.StatusBadRequest, "invalid webhook signature")
		return
	}

	ctx := r.Context()
	slog.Info("stripe webhook received", "type", event.Type, "customer_id", event.CustomerID, "subscription_id", event.SubscriptionID, "status", event.Status, "plan", event.PlanName)

	// Find tenant by Stripe customer ID
	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		slog.Error("webhook failed to list tenants", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var tenant *tenantdb.Tenant
	for _, t := range tenants {
		if t.StripeCustomerID == event.CustomerID {
			tenant = t
			break
		}
	}

	if tenant == nil {
		slog.Warn("webhook no tenant found for stripe customer", "customer_id", event.CustomerID)
		// Return 200 to acknowledge — Stripe retries on non-2xx
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated":
		// Update plan based on price ID
		oldPlan := tenant.Plan
		if event.PlanName != "" && event.PlanName != "unknown" {
			if err := gw.db.Tenants().UpdatePlan(ctx, tenant.ID, event.PlanName); err != nil {
				slog.Error("webhook failed to update plan", "tenant_id", tenant.ID, "error", err)
			} else {
				slog.Info("webhook updated tenant plan", "tenant_id", tenant.ID, "plan", event.PlanName)
				gw.auditDirect(tenant.ID, "", "tenant.plan_change", "tenant", tenant.ID,
					fmt.Sprintf(`{"old":%q,"new":%q}`, oldPlan, event.PlanName), r.RemoteAddr)
			}
		}
		// Update subscription ID
		if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, event.CustomerID, event.SubscriptionID); err != nil {
			slog.Error("webhook failed to update stripe IDs", "tenant_id", tenant.ID, "error", err)
		}
		// Reactivate if previously suspended
		if event.Status == "active" && tenant.Status != "active" {
			if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "active"); err != nil {
				slog.Error("webhook failed to reactivate tenant", "tenant_id", tenant.ID, "error", err)
			}
		}

	case "customer.subscription.deleted":
		// Subscription canceled — mark tenant as churned
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "churned"); err != nil {
			slog.Error("webhook failed to mark tenant as churned", "tenant_id", tenant.ID, "error", err)
		} else {
			slog.Info("webhook marked tenant as churned", "tenant_id", tenant.ID)
			gw.auditDirect(tenant.ID, "", "tenant.churned", "tenant", tenant.ID, "", r.RemoteAddr)
		}

	case "invoice.payment_failed":
		// Payment failed — suspend tenant
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "suspended"); err != nil {
			slog.Error("webhook failed to suspend tenant", "tenant_id", tenant.ID, "error", err)
		} else {
			slog.Info("webhook suspended tenant due to payment failure", "tenant_id", tenant.ID)
			gw.auditDirect(tenant.ID, "", "tenant.suspended", "tenant", tenant.ID, "payment_failed", r.RemoteAddr)
		}
	}

	// Always return 200 to acknowledge receipt
	w.WriteHeader(http.StatusOK)
}

// handleBillingPortal returns a Stripe Customer Portal URL for self-service billing.
func (gw *gateway) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if gw.billing == nil {
		writeError(w, http.StatusServiceUnavailable, "billing not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	if tenant.StripeCustomerID == "" {
		writeError(w, http.StatusBadRequest, "no billing account found — contact support")
		return
	}

	var req struct {
		ReturnURL string `json:"return_url"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ReturnURL == "" {
		req.ReturnURL = "/"
	}

	url, err := gw.billing.CreatePortalSession(r.Context(), tenant.StripeCustomerID, req.ReturnURL)
	if err != nil {
		slog.Error("failed to create portal session", "tenant_id", tenant.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create billing portal session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (gw *gateway) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if gw.billing == nil {
		writeError(w, http.StatusServiceUnavailable, "billing not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}

	tenant, err := gw.db.Tenants().GetByID(r.Context(), claims.TenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	if tenant.StripeCustomerID == "" {
		writeError(w, http.StatusBadRequest, "no billing account found")
		return
	}

	var req struct {
		PriceID    string `json:"price_id"`
		SuccessURL string `json:"success_url"`
		CancelURL  string `json:"cancel_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.PriceID == "" {
		writeError(w, http.StatusBadRequest, "price_id required")
		return
	}
	if req.SuccessURL == "" {
		req.SuccessURL = "/"
	}
	if req.CancelURL == "" {
		req.CancelURL = "/"
	}

	// Determine trial days for the target plan
	planName := gw.billing.PlanForPriceID(req.PriceID)
	trialDays := billing.DefaultTrialDays(planName)

	svc, ok := gw.billing.(*billing.StripeService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "checkout not available")
		return
	}

	url, err := svc.CreateCheckoutSession(r.Context(), billing.CheckoutParams{
		CustomerID: tenant.StripeCustomerID,
		PriceID:    req.PriceID,
		SuccessURL: req.SuccessURL,
		CancelURL:  req.CancelURL,
		TrialDays:  trialDays,
	})
	if err != nil {
		slog.Error("failed to create checkout session", "tenant_id", tenant.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create checkout session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleBillingUsage returns current event usage for the tenant, including plan info and daily breakdown.
func (gw *gateway) handleBillingUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	// Look up tenant for plan info
	tenant, err := gw.db.Tenants().GetByID(ctx, claims.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up tenant")
		return
	}

	// Get today's count
	today := time.Now().UTC().Format("2006-01-02")
	todayCount, _ := gw.db.EventCounters().GetCount(ctx, claims.TenantID, today)

	// Sum the current month's counts
	year, month, _ := time.Now().UTC().Date()
	var monthTotal int64
	for day := 1; day <= 31; day++ {
		d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if d.Month() != month {
			break
		}
		dayStr := d.Format("2006-01-02")
		count, _ := gw.db.EventCounters().GetCount(ctx, claims.TenantID, dayStr)
		monthTotal += count
	}

	// Daily counts for last 30 days
	type dailyCount struct {
		Day   string `json:"day"`
		Count int64  `json:"count"`
	}
	dailyCounts := make([]dailyCount, 0, 30)
	for i := 29; i >= 0; i-- {
		d := time.Now().UTC().AddDate(0, 0, -i)
		dayStr := d.Format("2006-01-02")
		count, _ := gw.db.EventCounters().GetCount(ctx, claims.TenantID, dayStr)
		dailyCounts = append(dailyCounts, dailyCount{Day: dayStr, Count: count})
	}

	// Plan limits
	var eventLimit int64
	switch tenant.Plan {
	case "pro":
		eventLimit = 50_000_000
	case "starter":
		eventLimit = 10_000_000
	default:
		eventLimit = 1_000_000
	}

	rateLimitRPS, _ := ratelimit.PlanRateLimit(tenant.Plan)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":      claims.TenantID,
		"today":          todayCount,
		"month_total":    monthTotal,
		"period":         fmt.Sprintf("%d-%02d", year, month),
		"plan":           tenant.Plan,
		"event_limit":    eventLimit,
		"rate_limit_rps": rateLimitRPS,
		"daily_counts":   dailyCounts,
	})
}

// handleBillingInvoices returns Stripe invoice list for the tenant.
func (gw *gateway) handleBillingInvoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	tenant, err := gw.db.Tenants().GetByID(ctx, claims.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up tenant")
		return
	}

	if tenant.StripeCustomerID == "" || gw.billing == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": []interface{}{}})
		return
	}

	invoices, err := gw.billing.ListInvoices(ctx, tenant.StripeCustomerID)
	if err != nil {
		slog.Error("failed to list invoices", "tenant_id", claims.TenantID, "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": []interface{}{}})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": invoices})
}

// handleBillingUsageHistory returns monthly usage summaries for the tenant.
func (gw *gateway) handleBillingUsageHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	history, err := gw.db.MonthlyUsage().GetByTenant(ctx, claims.TenantID, 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get usage history")
		return
	}

	type monthEntry struct {
		Month string `json:"month"`
		Count int64  `json:"count"`
		Plan  string `json:"plan"`
	}
	entries := make([]monthEntry, 0, len(history))
	for _, h := range history {
		entries = append(entries, monthEntry{Month: h.Month, Count: h.Count, Plan: h.Plan})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"history": entries})
}

// usageMeteringLoop reports tenant event usage to Stripe every hour.
func (gw *gateway) usageMeteringLoop(ctx context.Context) {
	// Wait 5 minutes after startup before first run
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		gw.reportUsageToStripe(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// --- Notification Channel handlers ---

func (gw *gateway) handleChannels(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		channels, err := gw.db.NotificationChannels().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list channels")
			return
		}
		if channels == nil {
			channels = []*tenantdb.NotificationChannel{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"channels": channels})

	case http.MethodPost:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		var req struct {
			Name   string          `json:"name"`
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Name == "" || req.Type == "" {
			writeError(w, http.StatusBadRequest, "name and type are required")
			return
		}
		if req.Type != "slack" && req.Type != "webhook" {
			writeError(w, http.StatusBadRequest, "type must be slack or webhook")
			return
		}

		configStr := "{}"
		if req.Config != nil {
			configStr = string(req.Config)
		}
		// Validate config
		if _, err := notify.ParseChannelConfig(configStr); err != nil {
			writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
			return
		}

		ch := &tenantdb.NotificationChannel{
			TenantID: claims.TenantID,
			Name:     req.Name,
			Type:     req.Type,
			Config:   configStr,
		}
		if err := gw.db.NotificationChannels().Create(r.Context(), ch); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create channel")
			return
		}
		gw.audit(r, "channel.create", "channel", ch.ID, req.Name)
		writeJSON(w, http.StatusCreated, ch)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (gw *gateway) handleChannelByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	// Extract ID from path: /api/gateway/channels/<id>[/test]
	parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[4] == "" {
		writeError(w, http.StatusBadRequest, "channel ID required")
		return
	}
	channelID := parts[4]

	// Check for /test suffix
	isTest := len(parts) >= 6 && parts[5] == "test"

	if isTest && r.Method == http.MethodPost {
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), channelID)
		if err != nil {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if ch.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		cfg, err := notify.ParseChannelConfig(ch.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid channel config: "+err.Error())
			return
		}
		if err := gw.notifier.SendTest(r.Context(), ch.Type, cfg); err != nil {
			writeError(w, http.StatusBadGateway, "test failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), channelID)
		if err != nil {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if ch.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		if err := gw.db.NotificationChannels().Delete(r.Context(), channelID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete channel")
			return
		}
		gw.audit(r, "channel.delete", "channel", channelID, ch.Name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// --- Alert Rule handlers ---

var validMetrics = map[string]bool{
	"error_rate":  true,
	"p50_latency": true,
	"p95_latency": true,
	"p99_latency": true,
	"throughput":  true,
}

func (gw *gateway) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		rules, err := gw.db.AlertRules().ListByTenant(r.Context(), claims.TenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list rules")
			return
		}
		if rules == nil {
			rules = []*tenantdb.AlertRule{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})

	case http.MethodPost:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		var req struct {
			Name            string  `json:"name"`
			Metric          string  `json:"metric"`
			Operator        string  `json:"operator"`
			Threshold       float64 `json:"threshold"`
			WindowMinutes   int     `json:"window_minutes"`
			Service         string  `json:"service"`
			PathTemplate    string  `json:"path_template"`
			ChannelID       string  `json:"channel_id"`
			CooldownMinutes int     `json:"cooldown_minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if errMsg := validateAlertRule(req.Name, req.Metric, req.Operator, req.Threshold,
			req.WindowMinutes, req.CooldownMinutes, req.ChannelID); errMsg != "" {
			writeError(w, http.StatusBadRequest, errMsg)
			return
		}

		// Verify channel belongs to this tenant
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), req.ChannelID)
		if err != nil || ch.TenantID != claims.TenantID {
			writeError(w, http.StatusBadRequest, "channel not found")
			return
		}

		rule := &tenantdb.AlertRule{
			TenantID:        claims.TenantID,
			Name:            req.Name,
			Metric:          req.Metric,
			Operator:        req.Operator,
			Threshold:       req.Threshold,
			WindowMinutes:   req.WindowMinutes,
			Service:         req.Service,
			PathTemplate:    req.PathTemplate,
			ChannelID:       req.ChannelID,
			CooldownMinutes: req.CooldownMinutes,
		}
		if err := gw.db.AlertRules().Create(r.Context(), rule); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create rule")
			return
		}
		gw.audit(r, "alert_rule.create", "alert_rule", rule.ID, req.Name)
		writeJSON(w, http.StatusCreated, rule)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (gw *gateway) handleAlertRuleByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[4] == "" {
		writeError(w, http.StatusBadRequest, "rule ID required")
		return
	}
	ruleID := parts[4]

	switch r.Method {
	case http.MethodPut:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		existing, err := gw.db.AlertRules().GetByID(r.Context(), ruleID)
		if err != nil {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		if existing.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}

		var req struct {
			Name            string  `json:"name"`
			Metric          string  `json:"metric"`
			Operator        string  `json:"operator"`
			Threshold       float64 `json:"threshold"`
			WindowMinutes   int     `json:"window_minutes"`
			Service         string  `json:"service"`
			PathTemplate    string  `json:"path_template"`
			ChannelID       string  `json:"channel_id"`
			CooldownMinutes int     `json:"cooldown_minutes"`
			Status          string  `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if errMsg := validateAlertRule(req.Name, req.Metric, req.Operator, req.Threshold,
			req.WindowMinutes, req.CooldownMinutes, req.ChannelID); errMsg != "" {
			writeError(w, http.StatusBadRequest, errMsg)
			return
		}
		if req.Status != "" && req.Status != "active" && req.Status != "paused" {
			writeError(w, http.StatusBadRequest, "status must be active or paused")
			return
		}

		// Verify channel belongs to tenant
		ch, err := gw.db.NotificationChannels().GetByID(r.Context(), req.ChannelID)
		if err != nil || ch.TenantID != claims.TenantID {
			writeError(w, http.StatusBadRequest, "channel not found")
			return
		}

		existing.Name = req.Name
		existing.Metric = req.Metric
		existing.Operator = req.Operator
		existing.Threshold = req.Threshold
		existing.WindowMinutes = req.WindowMinutes
		existing.Service = req.Service
		existing.PathTemplate = req.PathTemplate
		existing.ChannelID = req.ChannelID
		existing.CooldownMinutes = req.CooldownMinutes
		if req.Status != "" {
			existing.Status = req.Status
		}

		if err := gw.db.AlertRules().Update(r.Context(), existing); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update rule")
			return
		}
		gw.audit(r, "alert_rule.update", "alert_rule", ruleID,
			fmt.Sprintf(`{"name":%q,"status":%q}`, existing.Name, existing.Status))
		writeJSON(w, http.StatusOK, existing)

	case http.MethodDelete:
		if !claims.HasRole(auth.RoleAdmin, auth.RoleEditor) {
			writeError(w, http.StatusForbidden, "admin or editor role required")
			return
		}

		existing, err := gw.db.AlertRules().GetByID(r.Context(), ruleID)
		if err != nil {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		if existing.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}

		if err := gw.db.AlertRules().Delete(r.Context(), ruleID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete rule")
			return
		}
		gw.audit(r, "alert_rule.delete", "alert_rule", ruleID, existing.Name)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func validateAlertRule(name, metric, operator string, threshold float64, windowMin, cooldownMin int, channelID string) string {
	if name == "" {
		return "name is required"
	}
	if !validMetrics[metric] {
		return "metric must be one of: error_rate, p50_latency, p95_latency, p99_latency, throughput"
	}
	if operator != "gt" && operator != "lt" && operator != "anomaly" {
		return "operator must be gt, lt, or anomaly"
	}
	if operator == "anomaly" {
		if threshold <= 0 {
			return "threshold (stddev multiplier) must be > 0 for anomaly rules"
		}
		if windowMin < 1 || windowMin > 30 {
			return "window_minutes (lookback days) must be 1-30 for anomaly rules"
		}
	} else {
		if threshold < 0 {
			return "threshold must be >= 0"
		}
		if windowMin < 1 || windowMin > 60 {
			return "window_minutes must be 1-60"
		}
	}
	if cooldownMin < 1 || cooldownMin > 1440 {
		return "cooldown_minutes must be 1-1440"
	}
	if channelID == "" {
		return "channel_id is required"
	}
	return ""
}

// --- Alert History handler ---

func (gw *gateway) handleAlertHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())

	ruleID := r.URL.Query().Get("rule_id")
	var entries []*tenantdb.AlertHistoryEntry
	var err error

	if ruleID != "" {
		// Verify rule belongs to tenant
		rule, rerr := gw.db.AlertRules().GetByID(r.Context(), ruleID)
		if rerr != nil || rule.TenantID != claims.TenantID {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		entries, err = gw.db.AlertHistory().ListByRule(r.Context(), ruleID, 50)
	} else {
		entries, err = gw.db.AlertHistory().ListByTenant(r.Context(), claims.TenantID, 50)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alert history")
		return
	}
	if entries == nil {
		entries = []*tenantdb.AlertHistoryEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"history": entries})
}

// --- Alert Evaluator ---

var metricToCubeMeasure = map[string]string{
	"error_rate":  "RequestMetricsMinute.errorRate",
	"p50_latency": "RequestMetricsMinute.p50Latency",
	"p95_latency": "RequestMetricsMinute.p95Latency",
	"p99_latency": "RequestMetricsMinute.p99Latency",
	"throughput":  "RequestMetricsMinute.requestCount",
}

func (gw *gateway) alertEvaluatorLoop(ctx context.Context) {
	// Wait 2 minutes after startup before first evaluation
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		gw.evaluateAlerts(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (gw *gateway) evaluateAlerts(ctx context.Context) {
	rules, err := gw.db.AlertRules().ListActive(ctx)
	if err != nil {
		slog.Error("alert evaluator failed to list active rules", "error", err)
		return
	}

	if len(rules) == 0 {
		return
	}

	// Group rules by tenant for JWT reuse
	byTenant := map[string][]*tenantdb.AlertRule{}
	for _, rule := range rules {
		byTenant[rule.TenantID] = append(byTenant[rule.TenantID], rule)
	}

	now := time.Now().UTC()

	for tenantID, tenantRules := range byTenant {
		// Generate a short-lived token for Cube.js queries
		token, err := gw.tokens.Generate(tenantID, "evaluator", "evaluator@system", "admin")
		if err != nil {
			slog.Error("alert evaluator failed to generate token", "tenant_id", tenantID, "error", err)
			continue
		}

		for _, rule := range tenantRules {
			// Check cooldown
			if rule.LastTriggeredAt != nil {
				cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
				if now.Sub(*rule.LastTriggeredAt) < cooldown {
					continue
				}
			}

			var value float64
			var triggered bool
			var message string
			var anomalyRes *anomalyResult

			if rule.Operator == "anomaly" {
				// Anomaly detection: compare current value against historical baseline
				var err error
				anomalyRes, err = gw.evaluateAnomalyRule(ctx, token, rule)
				if err != nil {
					slog.Error("alert evaluator anomaly error", "tenant_id", tenantID, "rule_id", rule.ID, "error", err)
					continue
				}
				if anomalyRes == nil {
					continue // not enough data or no anomaly
				}
				value = anomalyRes.currentValue
				triggered = anomalyRes.triggered
				message = fmt.Sprintf("%s: %s anomaly detected — value %.4f deviates from mean %.4f by %.1fσ (threshold %.1fσ)",
					rule.Name, rule.Metric, anomalyRes.currentValue,
					anomalyRes.mean, anomalyRes.deviationSigma, rule.Threshold)
			} else {
				var err error
				value, err = gw.queryCubeMetric(ctx, token, rule)
				if err != nil {
					slog.Error("alert evaluator error querying metric", "tenant_id", tenantID, "rule_id", rule.ID, "error", err)
					gatewayAlertEvalErrorsTotal.Inc()
					continue
				}

				if rule.Operator == "gt" && value > rule.Threshold {
					triggered = true
				} else if rule.Operator == "lt" && value < rule.Threshold {
					triggered = true
				}

				operatorText := ">"
				if rule.Operator == "lt" {
					operatorText = "<"
				}
				message = fmt.Sprintf("%s: %s is %.4f (%s %.4f) over %d min window",
					rule.Name, rule.Metric, value, operatorText, rule.Threshold, rule.WindowMinutes)
			}

			if !triggered {
				continue
			}

			slog.Warn("alert evaluator triggered", "tenant_id", tenantID, "rule_name", rule.Name, "metric", rule.Metric, "value", value)

			// Dispatch notification
			ch, err := gw.db.NotificationChannels().GetByID(ctx, rule.ChannelID)
			if err != nil {
				slog.Error("alert evaluator channel not found", "channel_id", rule.ChannelID, "rule_id", rule.ID, "error", err)
				continue
			}

			cfg, err := notify.ParseChannelConfig(ch.Config)
			if err != nil {
				slog.Error("alert evaluator invalid config for channel", "channel_id", ch.ID, "error", err)
				continue
			}

			payload := notify.AlertPayload{
				RuleName:      rule.Name,
				Metric:        rule.Metric,
				Operator:      rule.Operator,
				Threshold:     rule.Threshold,
				ActualValue:   value,
				WindowMinutes: rule.WindowMinutes,
				Service:       rule.Service,
				PathTemplate:  rule.PathTemplate,
				FiredAt:       now,
			}

			// Set anomaly-specific fields if applicable
			if rule.Operator == "anomaly" && anomalyRes != nil {
				payload.Mean = anomalyRes.mean
				payload.Stddev = anomalyRes.stddev
				payload.DeviationSigma = anomalyRes.deviationSigma
			}

			status := "fired"
			if err := gw.notifier.Send(ctx, ch.Type, cfg, payload); err != nil {
				slog.Error("alert evaluator failed to send notification", "rule_id", rule.ID, "error", err)
				gatewayAlertNotificationErrorsTotal.WithLabelValues(ch.Type).Inc()
				status = "error"
				message += " (notification failed: " + err.Error() + ")"
			}

			// Record in history
			entry := &tenantdb.AlertHistoryEntry{
				RuleID:       rule.ID,
				TenantID:     tenantID,
				Metric:       rule.Metric,
				Threshold:    rule.Threshold,
				ActualValue:  value,
				Service:      rule.Service,
				PathTemplate: rule.PathTemplate,
				Status:       status,
				Message:      message,
			}
			if err := gw.db.AlertHistory().Create(ctx, entry); err != nil {
				slog.Error("alert evaluator failed to record history", "rule_id", rule.ID, "error", err)
			}

			// Update last triggered
			if err := gw.db.AlertRules().UpdateLastTriggered(ctx, rule.ID, now); err != nil {
				slog.Error("alert evaluator failed to update last_triggered", "rule_id", rule.ID, "error", err)
			}
		}
	}
}

// anomalyResult holds the result of an anomaly evaluation.
type anomalyResult struct {
	currentValue   float64
	mean           float64
	stddev         float64
	deviationSigma float64
	triggered      bool
}

// evaluateAnomalyRule compares the current metric value against a historical
// baseline for the same hour-of-day and day-of-week. Returns nil if there
// isn't enough historical data (< 3 data points).
func (gw *gateway) evaluateAnomalyRule(ctx context.Context, token string, rule *tenantdb.AlertRule) (*anomalyResult, error) {
	measure, ok := metricToCubeMeasure[rule.Metric]
	if !ok {
		return nil, fmt.Errorf("unknown metric: %s", rule.Metric)
	}

	// WindowMinutes is repurposed as lookback days for anomaly rules
	lookbackDays := rule.WindowMinutes
	if lookbackDays < 1 {
		lookbackDays = 7
	}

	// Build filters for historical data query
	filters := []map[string]interface{}{}
	if rule.Service != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.service",
			"operator": "equals",
			"values":   []string{rule.Service},
		})
	}
	if rule.PathTemplate != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.pathTemplate",
			"operator": "equals",
			"values":   []string{rule.PathTemplate},
		})
	}

	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -lookbackDays)

	// Query historical data at hourly granularity
	query := map[string]interface{}{
		"measures": []string{measure},
		"timeDimensions": []map[string]interface{}{{
			"dimension":   "RequestMetricsMinute.bucketStart",
			"granularity": "hour",
			"dateRange":   []string{cutoff.Format("2006-01-02"), now.Format("2006-01-02")},
		}},
		"filters": filters,
		"order":   map[string]string{"RequestMetricsMinute.bucketStart": "asc"},
	}

	body, err := json.Marshal(map[string]interface{}{"query": query})
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", gw.cubeAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cube query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cube returned %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Data []map[string]interface{} `json:"data"`
		} `json:"results"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	var data []map[string]interface{}
	if len(result.Results) > 0 {
		data = result.Results[0].Data
	} else {
		data = result.Data
	}

	if len(data) == 0 {
		return nil, nil // No data at all
	}

	// Find the time key in the response
	timeKey := "RequestMetricsMinute.bucketStart"
	if _, ok := data[0]["RequestMetricsMinute.bucketStart.hour"]; ok {
		timeKey = "RequestMetricsMinute.bucketStart.hour"
	}

	// Current hour and day of week for filtering
	currentHour := now.Hour()
	currentDow := now.Weekday()

	// Filter historical data points matching same hour-of-day and day-of-week
	var historicalValues []float64
	var currentValue float64
	currentFound := false

	for _, row := range data {
		tsRaw, ok := row[timeKey]
		if !ok {
			continue
		}
		tsStr, ok := tsRaw.(string)
		if !ok {
			continue
		}

		// Parse timestamp
		var ts time.Time
		for _, layout := range []string{
			"2006-01-02T15:04:05.000",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
		} {
			ts, err = time.Parse(layout, tsStr)
			if err == nil {
				break
			}
		}
		if ts.IsZero() {
			continue
		}

		// Extract metric value
		val := extractFloat(row, measure)

		// Check if this is the current hour (most recent matching)
		if ts.Hour() == currentHour && ts.Weekday() == currentDow {
			// Check if this is today's data point
			if ts.Year() == now.Year() && ts.YearDay() == now.YearDay() {
				currentValue = val
				currentFound = true
			} else {
				// Historical data point matching same hour + DOW
				historicalValues = append(historicalValues, val)
			}
		}
	}

	// If we didn't find a current value, get it from the most recent data point
	if !currentFound {
		currentValue, err = gw.queryCubeMetric(ctx, token, rule)
		if err != nil {
			return nil, fmt.Errorf("query current value: %w", err)
		}
		currentFound = true
	}

	// Need at least 3 historical data points for meaningful statistics
	if len(historicalValues) < 3 {
		slog.Info("alert evaluator anomaly rule skipped insufficient data", "rule_id", rule.ID, "data_points", len(historicalValues))
		return nil, nil
	}

	mean, err := stats.Mean(historicalValues)
	if err != nil {
		return nil, fmt.Errorf("compute mean: %w", err)
	}

	stddev, err := stats.StandardDeviationSample(historicalValues)
	if err != nil {
		return nil, fmt.Errorf("compute stddev: %w", err)
	}

	// Avoid division by zero — if stddev is 0, any deviation is infinite
	var deviationSigma float64
	if stddev > 0 {
		deviationSigma = math.Abs(currentValue-mean) / stddev
	} else if currentValue != mean {
		deviationSigma = math.Inf(1) // Any deviation from a flat baseline is infinite sigma
	}

	triggered := deviationSigma > rule.Threshold

	return &anomalyResult{
		currentValue:   currentValue,
		mean:           mean,
		stddev:         stddev,
		deviationSigma: deviationSigma,
		triggered:      triggered,
	}, nil
}

// extractFloat extracts a float64 value from a Cube.js data row.
func extractFloat(row map[string]interface{}, key string) float64 {
	val, ok := row[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f
	default:
		return 0
	}
}

func (gw *gateway) queryCubeMetric(ctx context.Context, token string, rule *tenantdb.AlertRule) (float64, error) {
	measure, ok := metricToCubeMeasure[rule.Metric]
	if !ok {
		return 0, fmt.Errorf("unknown metric: %s", rule.Metric)
	}

	filters := []map[string]interface{}{}
	if rule.Service != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.service",
			"operator": "equals",
			"values":   []string{rule.Service},
		})
	}
	if rule.PathTemplate != "" {
		filters = append(filters, map[string]interface{}{
			"member":   "RequestMetricsMinute.pathTemplate",
			"operator": "equals",
			"values":   []string{rule.PathTemplate},
		})
	}

	cutoff := time.Now().UTC().Add(-time.Duration(rule.WindowMinutes) * time.Minute)
	filters = append(filters, map[string]interface{}{
		"member":   "RequestMetricsMinute.bucketStart",
		"operator": "gte",
		"values":   []string{cutoff.Format("2006-01-02T15:04:05")},
	})

	query := map[string]interface{}{
		"measures": []string{measure},
		"filters":  filters,
	}

	body, err := json.Marshal(map[string]interface{}{"query": query})
	if err != nil {
		return 0, fmt.Errorf("marshal query: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", gw.cubeAPIURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cube query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("cube returned %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Data []map[string]interface{} `json:"data"`
		} `json:"results"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	var data []map[string]interface{}
	if len(result.Results) > 0 {
		data = result.Results[0].Data
	} else {
		data = result.Data
	}

	if len(data) == 0 {
		return 0, nil // No data for this window
	}

	val, ok := data[0][measure]
	if !ok {
		return 0, nil
	}

	switch v := val.(type) {
	case float64:
		return v, nil
	case string:
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f, nil
	default:
		return 0, fmt.Errorf("unexpected value type for %s: %T", measure, val)
	}
}

// reportUsageToStripe reads event counters and reports to Stripe for all active tenants.
func (gw *gateway) reportUsageToStripe(ctx context.Context) {
	if gw.billing == nil {
		return
	}

	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		slog.Error("usage metering failed to list tenants", "error", err)
		return
	}

	today := time.Now().UTC().Format("2006-01-02")

	for _, tenant := range tenants {
		if tenant.Status != "active" || tenant.StripeSubscriptionID == "" {
			continue
		}

		count, err := gw.db.EventCounters().GetCount(ctx, tenant.ID, today)
		if err != nil {
			slog.Error("usage metering failed to get count", "tenant_id", tenant.ID, "error", err)
			continue
		}

		if count == 0 {
			continue
		}

		if err := gw.billing.ReportUsage(ctx, tenant.StripeSubscriptionID, count, time.Now().UTC()); err != nil {
			slog.Error("usage metering failed to report usage", "tenant_id", tenant.ID, "error", err)
		} else {
			slog.Info("usage metering reported events", "tenant_id", tenant.ID, "count", count)
		}
	}

	// Monthly snapshot: on the 1st of each month, snapshot previous month's total
	now := time.Now().UTC()
	if now.Day() == 1 {
		prevMonth := now.AddDate(0, -1, 0)
		prevYear, prevMon, _ := prevMonth.Date()
		monthStr := fmt.Sprintf("%d-%02d", prevYear, prevMon)

		for _, tenant := range tenants {
			if tenant.Status != "active" {
				continue
			}
			var total int64
			for day := 1; day <= 31; day++ {
				d := time.Date(prevYear, prevMon, day, 0, 0, 0, 0, time.UTC)
				if d.Month() != prevMon {
					break
				}
				dayStr := d.Format("2006-01-02")
				c, _ := gw.db.EventCounters().GetCount(ctx, tenant.ID, dayStr)
				total += c
			}
			if total > 0 {
				if err := gw.db.MonthlyUsage().Snapshot(ctx, tenant.ID, monthStr, total, tenant.Plan); err != nil {
					slog.Error("monthly snapshot failed", "tenant_id", tenant.ID, "month", monthStr, "error", err)
				} else {
					slog.Info("monthly usage snapshot", "tenant_id", tenant.ID, "month", monthStr, "count", total)
				}
			}
		}
	}
}

// --- Dead Letter Queue API ---

// dlqEntry mirrors the ingestion DLQ entry format.
type dlqEntry struct {
	Timestamp string          `json:"timestamp"`
	TenantID  string          `json:"tenant_id,omitempty"`
	FactType  string          `json:"fact_type"`
	Error     string          `json:"error"`
	RawJSON   json.RawMessage `json:"raw_json"`
}

// handleDLQ lists recent DLQ entries for the authenticated tenant.
// GET /api/gateway/dlq?limit=50
func (gw *gateway) handleDLQ(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if gw.store == nil {
		writeError(w, http.StatusServiceUnavailable, "DLQ storage not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenantID := claims.TenantID

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	// List DLQ files for this tenant
	prefix := tenantID + "/dlq/request_facts/"
	if tenantID == "" {
		prefix = "dlq/request_facts/"
	}

	files, err := gw.store.List(r.Context(), prefix)
	if err != nil {
		slog.Error("dlq list error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list DLQ files")
		return
	}

	// Sort files descending (most recent first)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	// Read entries from files until we have enough
	var entries []dlqEntry
	for _, file := range files {
		if len(entries) >= limit {
			break
		}

		reader, err := gw.store.Get(r.Context(), file)
		if err != nil {
			slog.Error("dlq read error", "file", file, "error", err)
			continue
		}

		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1MB lines
		var fileEntries []dlqEntry
		for scanner.Scan() {
			var entry dlqEntry
			if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
				continue
			}
			fileEntries = append(fileEntries, entry)
		}
		reader.Close()

		// Add entries in reverse order (most recent first)
		for i := len(fileEntries) - 1; i >= 0; i-- {
			entries = append(entries, fileEntries[i])
			if len(entries) >= limit {
				break
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"total":   len(entries),
	})
}

// handleDLQReplay re-submits DLQ entries to the ingestion endpoint.
// POST /api/gateway/dlq/replay  body: {"entries": [{"raw_json": ...}, ...]}
// Security: validates each entry belongs to the authenticated tenant before replay.
func (gw *gateway) handleDLQReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenantID := claims.TenantID

	var req struct {
		Entries []struct {
			RawJSON json.RawMessage `json:"raw_json"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "no entries to replay")
		return
	}

	// Validate tenant scoping: each entry must belong to the authenticated tenant.
	// This prevents cross-tenant data injection via DLQ replay.
	var buf bytes.Buffer
	skipped := 0
	for i, e := range req.Entries {
		// Parse just the tenant_id field from each entry to validate ownership
		var entryMeta struct {
			TenantID string `json:"tenant_id"`
		}
		if err := json.Unmarshal(e.RawJSON, &entryMeta); err != nil {
			skipped++
			continue
		}
		// If entry has a tenant_id, it must match the authenticated tenant
		if entryMeta.TenantID != "" && entryMeta.TenantID != tenantID {
			skipped++
			slog.Warn("DLQ replay: skipped cross-tenant entry",
				"authenticated_tenant", tenantID,
				"entry_tenant", entryMeta.TenantID)
			continue
		}

		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		_ = i
		buf.Write(e.RawJSON)
	}

	if buf.Len() == 0 {
		writeError(w, http.StatusBadRequest, "no valid entries to replay (all skipped due to tenant mismatch)")
		return
	}

	// Forward to ingestion batch endpoint
	ingestionURL := os.Getenv("INGESTION_URL")
	if ingestionURL == "" {
		ingestionURL = "http://localhost:8090"
	}

	fwdReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		ingestionURL+"/api/v1/facts/batch", &buf)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build replay request")
		return
	}
	fwdReq.Header.Set("Content-Type", "application/json")

	// Propagate request ID for cross-service tracing
	logging.PropagateRequestID(r.Context(), fwdReq)

	// Forward the Authorization header
	if authHdr := r.Header.Get("Authorization"); authHdr != "" {
		fwdReq.Header.Set("Authorization", authHdr)
	}
	// Also try X-API-Key
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		fwdReq.Header.Set("X-API-Key", apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(fwdReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ingestion service unavailable")
		return
	}
	defer resp.Body.Close()

	// Relay the ingestion response with skipped count
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	if skipped > 0 {
		slog.Info("DLQ replay completed with skipped entries",
			"tenant_id", tenantID, "skipped", skipped,
			"replayed", len(req.Entries)-skipped)
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// traceSpan is a single span from a trace sample file.
type traceSpan struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	EventTime    string            `json:"event_time"`
	Service      string            `json:"service"`
	Method       string            `json:"method"`
	PathTemplate string            `json:"path_template"`
	StatusCode   int               `json:"status_code"`
	LatencyMs    int               `json:"latency_ms"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// traceSummary is a lightweight summary of a trace for listing.
type traceSummary struct {
	TraceID      string `json:"trace_id"`
	RootService  string `json:"root_service"`
	RootPath     string `json:"root_path"`
	TotalLatency int    `json:"total_latency_ms"`
	SpanCount    int    `json:"span_count"`
	EventTime    string `json:"event_time"`
}

// handleTracesRecent lists recent trace IDs by scanning the most recent trace_samples files.
// GET /api/gateway/traces/recent?limit=20
func (gw *gateway) handleTracesRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if gw.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenantID := claims.TenantID

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	// List trace sample files for this tenant (last 24 hours)
	prefix := "raw/"
	if tenantID != "" {
		prefix += tenantID + "/"
	}
	prefix += "trace_samples/"

	files, err := gw.store.List(r.Context(), prefix)
	if err != nil {
		slog.Error("trace list error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list trace files")
		return
	}

	// Sort descending (most recent first)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	// Read spans and group by trace_id
	traceMap := make(map[string]*traceSummary)
	for _, file := range files {
		if len(traceMap) >= limit*2 { // read enough files to fill limit
			break
		}

		reader, err := gw.store.Get(r.Context(), file)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			var span traceSpan
			if err := json.Unmarshal(scanner.Bytes(), &span); err != nil {
				continue
			}
			if summary, exists := traceMap[span.TraceID]; exists {
				summary.SpanCount++
				if span.LatencyMs > summary.TotalLatency {
					summary.TotalLatency = span.LatencyMs
				}
			} else {
				traceMap[span.TraceID] = &traceSummary{
					TraceID:      span.TraceID,
					RootService:  span.Service,
					RootPath:     span.PathTemplate,
					TotalLatency: span.LatencyMs,
					SpanCount:    1,
					EventTime:    span.EventTime,
				}
			}
		}
		reader.Close()
	}

	// Convert to sorted slice
	traces := make([]traceSummary, 0, len(traceMap))
	for _, s := range traceMap {
		traces = append(traces, *s)
	}
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].EventTime > traces[j].EventTime
	})
	if len(traces) > limit {
		traces = traces[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"traces": traces,
		"total":  len(traces),
	})
}

// handleTraceByID returns all spans for a specific trace.
// GET /api/gateway/traces?trace_id=...
func (gw *gateway) handleTraceByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if gw.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not configured")
		return
	}

	traceID := r.URL.Query().Get("trace_id")
	if traceID == "" {
		writeError(w, http.StatusBadRequest, "trace_id query parameter required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	tenantID := claims.TenantID

	prefix := "raw/"
	if tenantID != "" {
		prefix += tenantID + "/"
	}
	prefix += "trace_samples/"

	files, err := gw.store.List(r.Context(), prefix)
	if err != nil {
		slog.Error("trace list error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list trace files")
		return
	}

	// Sort descending to search recent files first
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var spans []traceSpan
	for _, file := range files {
		reader, err := gw.store.Get(r.Context(), file)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			var span traceSpan
			if err := json.Unmarshal(scanner.Bytes(), &span); err != nil {
				continue
			}
			if span.TraceID == traceID {
				spans = append(spans, span)
			}
		}
		reader.Close()

		// If we found spans and scanned enough files, stop
		if len(spans) > 0 && len(spans) > 50 {
			break
		}
	}

	// Sort spans by event_time ascending
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].EventTime < spans[j].EventTime
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"trace_id": traceID,
		"spans":    spans,
		"total":    len(spans),
	})
}

// handleGDPRAccess returns all personal data held for the authenticated user (GDPR Article 15).
func (gw *gateway) handleGDPRAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	user, err := gw.db.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	tenant, _ := gw.db.Tenants().GetByID(ctx, claims.TenantID)
	consents, _ := gw.db.ConsentRecords().ListByUser(ctx, claims.UserID)

	result := map[string]interface{}{
		"user": map[string]interface{}{
			"id":             user.ID,
			"email":          user.Email,
			"role":           user.Role,
			"email_verified": user.EmailVerified,
			"status":         user.Status,
			"created_at":     user.CreatedAt.Format(time.RFC3339),
		},
		"consent_records": consents,
	}
	if tenant != nil {
		result["tenant"] = map[string]interface{}{
			"id":         tenant.ID,
			"name":       tenant.Name,
			"plan":       tenant.Plan,
			"status":     tenant.Status,
			"created_at": tenant.CreatedAt.Format(time.RFC3339),
		}
	}

	gw.auditDirect(claims.TenantID, claims.UserID, "gdpr.access", "user", claims.UserID, "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, result)
}

// handleGDPRPortability exports personal data as a downloadable JSON file (GDPR Article 20).
func (gw *gateway) handleGDPRPortability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	user, err := gw.db.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	tenant, _ := gw.db.Tenants().GetByID(ctx, claims.TenantID)
	consents, _ := gw.db.ConsentRecords().ListByUser(ctx, claims.UserID)

	export := map[string]interface{}{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"user": map[string]interface{}{
			"id":             user.ID,
			"email":          user.Email,
			"role":           user.Role,
			"email_verified": user.EmailVerified,
			"status":         user.Status,
			"created_at":     user.CreatedAt.Format(time.RFC3339),
		},
		"consent_records": consents,
	}
	if tenant != nil {
		export["tenant"] = map[string]interface{}{
			"id":         tenant.ID,
			"name":       tenant.Name,
			"plan":       tenant.Plan,
			"status":     tenant.Status,
			"created_at": tenant.CreatedAt.Format(time.RFC3339),
		}
	}

	gw.auditDirect(claims.TenantID, claims.UserID, "gdpr.portability", "user", claims.UserID, "", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"gravix-data-export.json\"")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(export)
}

// handleConsent manages consent records (GET = list, POST = record new consent).
func (gw *gateway) handleConsent(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		records, err := gw.db.ConsentRecords().ListByUser(ctx, claims.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list consent records")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"consent_records": records,
		})

	case http.MethodPost:
		var req struct {
			Type     string `json:"type"`
			Version  string `json:"version"`
			Accepted bool   `json:"accepted"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Type == "" || req.Version == "" {
			writeError(w, http.StatusBadRequest, "type and version are required")
			return
		}
		validTypes := map[string]bool{"tos": true, "privacy": true, "cookies": true}
		if !validTypes[req.Type] {
			writeError(w, http.StatusBadRequest, "type must be tos, privacy, or cookies")
			return
		}

		record := &tenantdb.ConsentRecord{
			TenantID:  claims.TenantID,
			UserID:    claims.UserID,
			Type:      req.Type,
			Version:   req.Version,
			Accepted:  req.Accepted,
			IPAddress: ratelimit.ExtractIP(r),
			CreatedAt: time.Now().UTC(),
		}
		if err := gw.db.ConsentRecords().Create(ctx, record); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record consent")
			return
		}
		gw.auditDirect(claims.TenantID, claims.UserID, "consent.record", "consent", record.ID,
			fmt.Sprintf(`{"type":%q,"version":%q,"accepted":%v}`, req.Type, req.Version, req.Accepted), r.RemoteAddr)
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"id":      record.ID,
			"type":    record.Type,
			"version": record.Version,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

// handleAccountDeletion requests account deletion with 30-day grace period.
func (gw *gateway) handleAccountDeletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "DELETE required")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	ctx := r.Context()

	// Check if deletion already pending
	existing, _ := gw.db.DeletionRequests().GetByTenantID(ctx, claims.TenantID)
	if existing != nil && existing.Status == "pending" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":           existing.ID,
			"status":       existing.Status,
			"requested_at": existing.RequestedAt.Format(time.RFC3339),
			"expires_at":   existing.ExpiresAt.Format(time.RFC3339),
			"message":      "deletion already requested",
		})
		return
	}

	dr := &tenantdb.DeletionRequest{
		TenantID:    claims.TenantID,
		RequestedBy: claims.UserID,
		Status:      "pending",
		RequestedAt: time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(30 * 24 * time.Hour),
	}
	if err := gw.db.DeletionRequests().Create(ctx, dr); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create deletion request")
		return
	}

	gw.auditDirect(claims.TenantID, claims.UserID, "account.delete_request", "tenant", claims.TenantID, "", r.RemoteAddr)
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"id":           dr.ID,
		"status":       "pending",
		"requested_at": dr.RequestedAt.Format(time.RFC3339),
		"expires_at":   dr.ExpiresAt.Format(time.RFC3339),
		"message":      "account scheduled for deletion in 30 days",
	})
}

// handleCancelDeletion cancels a pending account deletion request.
func (gw *gateway) handleCancelDeletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	ctx := r.Context()

	dr, err := gw.db.DeletionRequests().GetByTenantID(ctx, claims.TenantID)
	if err != nil || dr == nil || dr.Status != "pending" {
		writeError(w, http.StatusNotFound, "no pending deletion request found")
		return
	}

	if err := gw.db.DeletionRequests().Cancel(ctx, dr.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel deletion")
		return
	}

	gw.auditDirect(claims.TenantID, claims.UserID, "account.cancel_deletion", "tenant", claims.TenantID, "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "deletion request cancelled",
	})
}
