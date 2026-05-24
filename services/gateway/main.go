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
	"sync"

	"crypto/sha256"
	"encoding/hex"

	"github.com/lgreene/gravix-dashboards/pkg/logging"
	"github.com/lgreene/gravix-dashboards/pkg/pagination"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/billing"
	"github.com/lgreene/gravix-dashboards/pkg/captcha"
	"github.com/lgreene/gravix-dashboards/pkg/email"
	"github.com/lgreene/gravix-dashboards/pkg/notify"
	"github.com/lgreene/gravix-dashboards/pkg/password"
	"github.com/lgreene/gravix-dashboards/pkg/ratelimit"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	gatewayBillingCheckoutTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_billing_checkout_total",
			Help: "Total number of billing checkout sessions created.",
		},
		[]string{"plan", "interval"},
	)
	gatewayBillingWebhookTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_billing_webhook_total",
			Help: "Total number of Stripe webhook events processed.",
		},
		[]string{"event_type", "status"},
	)
	gatewayBillingPortalRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_billing_portal_requests_total",
			Help: "Total number of billing portal session requests.",
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
	prometheus.MustRegister(gatewayBillingCheckoutTotal)
	prometheus.MustRegister(gatewayBillingWebhookTotal)
	prometheus.MustRegister(gatewayBillingPortalRequestsTotal)
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

	tokens := auth.NewTokenService(*jwtSecret, 1*time.Hour)

	cubeURL := os.Getenv("CUBE_API_URL")
	if cubeURL == "" {
		cubeURL = "http://localhost:4000/cubejs-api/v1/load"
	}
	cubeSecret := os.Getenv("CUBE_API_SECRET")

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

	// TOTP encryption key: separate from JWT secret for security isolation
	var totpKey []byte
	if tk := os.Getenv("TOTP_ENCRYPTION_KEY"); tk != "" {
		totpKey = []byte(tk)
		slog.Info("using dedicated TOTP encryption key")
	} else {
		slog.Warn("TOTP_ENCRYPTION_KEY not set, falling back to JWT_SECRET (deprecated — set TOTP_ENCRYPTION_KEY for production)")
	}

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
		cubeAPISecret:   cubeSecret,
		jwtSecret:       *jwtSecret,
		totpKey:         totpKey,
		baseURL:         email.BaseURLFromEnv(),
		activeExports:   make(map[string]bool),
	}

	// Master context for background goroutines — cancelled on shutdown
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	// Start alert evaluator background loop
	go gw.alertEvaluatorLoop(bgCtx)

	// Start onboarding email drip loop
	go gw.onboardingEmailLoop(bgCtx)

	// Initialize Stripe billing if configured
	stripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if stripeKey != "" {
		plans := billing.DefaultPlansWithAnnual(
			os.Getenv("STRIPE_PRICE_FREE"),
			os.Getenv("STRIPE_PRICE_TEAM"),
			os.Getenv("STRIPE_PRICE_BUSINESS"),
			os.Getenv("STRIPE_PRICE_SCALE"),
			os.Getenv("STRIPE_PRICE_ENTERPRISE"),
			billing.AnnualPriceIDs{
				Team:       os.Getenv("STRIPE_PRICE_TEAM_ANNUAL"),
				Business:   os.Getenv("STRIPE_PRICE_BUSINESS_ANNUAL"),
				Scale:      os.Getenv("STRIPE_PRICE_SCALE_ANNUAL"),
				Enterprise: os.Getenv("STRIPE_PRICE_ENTERPRISE_ANNUAL"),
			},
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
	mux.HandleFunc("/api/gateway/api-keys/", gw.requireAuth(gw.rateLimitMiddleware(gw.handleAPIKeyByID)))
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
	mux.HandleFunc("/api/gateway/feedback", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleFeedback))))

	// Platform features (Phase 6)
	mux.HandleFunc("/api/gateway/dashboards", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleDashboards))))
	mux.HandleFunc("/api/gateway/dashboards/", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleDashboardByID))))
	mux.HandleFunc("/api/gateway/branding", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleBranding))))
	mux.HandleFunc("/api/gateway/branding/public", gw.ipRateLimitMiddleware(gw.handleBrandingPublic))
	mux.HandleFunc("/api/gateway/exports/scheduled", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleScheduledExports))))
	mux.HandleFunc("/api/gateway/exports/scheduled/", gw.requireAuth(gw.rateLimitMiddleware(bodyLimit(gw.handleScheduledExportByID))))
	mux.HandleFunc("/api/v1/metrics", gw.ipRateLimitMiddleware(gw.handlePublicMetrics))
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
		slog.Warn("CORS_ALLOWED_ORIGINS not set, defaulting to '*' — restrict this in production")
	}
	// Warn loudly if CORS is wildcard in non-development environments
	gravixEnv := os.Getenv("GRAVIX_ENV")
	if (allowedOrigins == "*" || allowedOrigins == "") && gravixEnv != "development" && gravixEnv != "dev" {
		slog.Warn("CORS_ALLOWED_ORIGINS is set to wildcard — restrict for production use",
			"current_value", allowedOrigins,
			"GRAVIX_ENV", gravixEnv,
		)
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
	cubeAPISecret   string // sent as Bearer token to Cube.js (CUBE_API_SECRET)
	jwtSecret       string
	baseURL         string // for email links (e.g., https://app.gravix.io)
	totpKey         []byte   // separate encryption key for TOTP secrets
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

	pg := pagination.FromRequest(r)

	entries, total, err := gw.db.AuditLog().ListByTenant(r.Context(), claims.TenantID, pg.Limit, pg.Offset())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit log")
		return
	}
	if entries == nil {
		entries = []*tenantdb.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       entries,
		"pagination": pagination.NewResponse(pg, total),
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

// handleAPIKeyByID handles requests with an ID suffix: DELETE /api/gateway/api-keys/{id}
func (gw *gateway) handleAPIKeyByID(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())

	// Extract key ID from path: /api/gateway/api-keys/<id>
	parts := strings.Split(r.URL.Path, "/")
	keyID := ""
	if len(parts) >= 5 {
		keyID = parts[4]
	}

	// Avoid matching /api/gateway/api-keys/expiring (handled by dedicated route)
	if keyID == "expiring" || keyID == "" {
		writeError(w, http.StatusBadRequest, "key ID required in path")
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
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

// onboardingEmailLoop sends drip emails to tenants based on account age.
// Runs every hour, checks tenant creation time, and sends:
//   - 3 days: "try alerting" nudge (if no alert rules created)
//   - 7 days: "upgrade plan" nudge (if still on free plan)
func (gw *gateway) onboardingEmailLoop(ctx context.Context) {
	// Track sent emails in-memory to avoid duplicates within process lifetime.
	// On restart, audit log entries prevent business-critical duplicate actions,
	// but a duplicate nudge email is harmless.
	sent := make(map[string]bool) // key: "tenantID:emailType"

	// Wait 10 minutes after startup before first run
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Minute):
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		gw.sendOnboardingEmails(ctx, sent)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (gw *gateway) sendOnboardingEmails(ctx context.Context, sent map[string]bool) {
	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		slog.Error("onboarding email loop: failed to list tenants", "error", err)
		return
	}

	now := time.Now().UTC()
	onboarding := email.NewOnboardingService(gw.emailSender, gw.baseURL)

	for _, t := range tenants {
		if t.Status != "active" {
			continue
		}
		age := now.Sub(t.CreatedAt)

		// 3-day nudge: try alerting
		if age >= 3*24*time.Hour && age < 30*24*time.Hour {
			key := t.ID + ":try_alerting"
			if !sent[key] {
				// Check if tenant has any alert rules
				rules, err := gw.db.AlertRules().ListByTenant(ctx, t.ID)
				if err != nil {
					slog.Error("onboarding: failed to list alert rules", "tenant_id", t.ID, "error", err)
					continue
				}
				if len(rules) == 0 {
					// Find admin user for this tenant
					users, err := gw.db.Users().ListByTenant(ctx, t.ID)
					if err != nil || len(users) == 0 {
						continue
					}
					onboarding.SendTryAlerting(ctx, users[0].Email, users[0].Email, t.Name)
					gw.auditDirect(t.ID, "", "onboarding.try_alerting_sent", "tenant", t.ID, "", "system")
					sent[key] = true
				} else {
					sent[key] = true // has rules, skip permanently
				}
			}
		}

		// 7-day nudge: upgrade from free
		if age >= 7*24*time.Hour && age < 30*24*time.Hour {
			key := t.ID + ":upgrade_nudge"
			if !sent[key] {
				if t.Plan == "free" {
					users, err := gw.db.Users().ListByTenant(ctx, t.ID)
					if err != nil || len(users) == 0 {
						continue
					}
					onboarding.SendUpgradeNudge(ctx, users[0].Email, users[0].Email, t.Name)
					gw.auditDirect(t.ID, "", "onboarding.upgrade_nudge_sent", "tenant", t.ID, "", "system")
					sent[key] = true
				} else {
					sent[key] = true // not on free, skip permanently
				}
			}
		}
	}
}

func (gw *gateway) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())

	var req struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Page    string `json:"page"`
		Rating  int    `json:"rating"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}
	if len(req.Message) > 2000 {
		http.Error(w, `{"error":"message too long (max 2000 chars)"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "general"
	}

	detail := fmt.Sprintf("type=%s page=%s rating=%d message=%s", req.Type, req.Page, req.Rating, req.Message)
	gw.audit(r, "feedback.submitted", "feedback", "", detail)

	slog.Info("feedback received",
		"tenant_id", claims.TenantID,
		"user_id", claims.UserID,
		"type", req.Type,
		"page", req.Page,
		"rating", req.Rating,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Thank you for your feedback!"})
}
