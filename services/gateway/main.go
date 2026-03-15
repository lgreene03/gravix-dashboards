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
//	GET  /api/gateway/billing/usage    — Get current usage stats (JWT required)
//	GET  /live                         — Health check
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
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

	"math"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/billing"
	"github.com/lgreene/gravix-dashboards/pkg/notify"
	"github.com/lgreene/gravix-dashboards/pkg/storage"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
	"github.com/montanaflynn/stats"
	"golang.org/x/crypto/bcrypt"
)

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{"error": msg, "code": code})
}

func main() {
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

	if *tenantDBPath == "" {
		log.Fatal("FATAL: TENANT_DB_PATH is required")
	}
	if *jwtSecret == "" {
		log.Fatal("FATAL: JWT_SECRET is required")
	}

	db, err := tenantdb.Open(*tenantDBPath)
	if err != nil {
		log.Fatalf("Failed to open tenant database: %v", err)
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
		log.Printf("WARNING: DLQ store init failed: %v (DLQ endpoints will be unavailable)", err)
	} else {
		log.Printf("DLQ store initialized: local dir %s", rawDir)
	}

	gw := &gateway{
		db:         db,
		tokens:     tokens,
		notifier:   notify.NewDispatcher(),
		store:      objStore,
		cubeAPIURL: cubeURL,
		jwtSecret:  *jwtSecret,
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
			os.Getenv("STRIPE_PRICE_STARTER"),
			os.Getenv("STRIPE_PRICE_PRO"),
		)
		gw.billing = billing.NewStripeService(
			stripeKey,
			os.Getenv("STRIPE_WEBHOOK_SECRET"),
			plans,
		)
		log.Println("Stripe billing enabled")

		// Start background usage metering (hourly)
		go gw.usageMeteringLoop(bgCtx)
	} else {
		log.Println("Stripe billing disabled (STRIPE_SECRET_KEY not set)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/gateway/login", gw.handleLogin)
	mux.HandleFunc("/api/gateway/register", gw.handleRegister)
	mux.HandleFunc("/api/gateway/me", gw.requireAuth(gw.handleMe))
	mux.HandleFunc("/api/gateway/api-keys", gw.requireAuth(gw.handleAPIKeys))
	mux.HandleFunc("/api/gateway/webhooks/stripe", gw.handleStripeWebhook)
	mux.HandleFunc("/api/gateway/billing/portal", gw.requireAuth(gw.handleBillingPortal))
	mux.HandleFunc("/api/gateway/billing/usage", gw.requireAuth(gw.handleBillingUsage))
	mux.HandleFunc("/api/gateway/channels", gw.requireAuth(gw.handleChannels))
	mux.HandleFunc("/api/gateway/channels/", gw.requireAuth(gw.handleChannelByID))
	mux.HandleFunc("/api/gateway/alert-rules", gw.requireAuth(gw.handleAlertRules))
	mux.HandleFunc("/api/gateway/alert-rules/", gw.requireAuth(gw.handleAlertRuleByID))
	mux.HandleFunc("/api/gateway/alert-history", gw.requireAuth(gw.handleAlertHistory))
	mux.HandleFunc("/api/gateway/dlq", gw.requireAuth(gw.handleDLQ))
	mux.HandleFunc("/api/gateway/dlq/replay", gw.requireAuth(gw.handleDLQReplay))
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// Lightweight DB probe — confirm SQLite is responsive
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := gw.db.Tenants().List(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})

	// CORS middleware for dashboard access
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf(":%d", *port)
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
		log.Printf("Received %v, draining connections (10s)...", sig)
		bgCancel() // stop background goroutines
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("Gateway service starting on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Println("Gateway stopped gracefully.")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type gateway struct {
	db         tenantdb.DB
	tokens     *auth.TokenService
	billing    billing.Service // nil if Stripe is not configured
	notifier   *notify.Dispatcher
	store      storage.ObjectStore // for DLQ reads
	cubeAPIURL string
	jwtSecret  string
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

	// Get tenant info for the token
	tenant, err := gw.db.Tenants().GetByID(r.Context(), user.TenantID)
	if err != nil || tenant.Status != "active" {
		writeError(w, http.StatusForbidden, "tenant is not active")
		return
	}

	token, err := gw.tokens.Generate(user.TenantID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     token,
		"tenant_id": user.TenantID,
		"user_id":   user.ID,
		"email":     user.Email,
		"role":      user.Role,
		"plan":      tenant.Plan,
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
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Name == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "name, email, and password are required")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
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
			log.Printf("Warning: failed to create Stripe customer for tenant %s: %v", tenant.ID, err)
		} else {
			subID, err := gw.billing.CreateSubscription(ctx, customerID, gw.billing.FreePriceID())
			if err != nil {
				log.Printf("Warning: failed to create Stripe subscription for tenant %s: %v", tenant.ID, err)
			}
			if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, customerID, subID); err != nil {
				log.Printf("Warning: failed to store Stripe IDs for tenant %s: %v", tenant.ID, err)
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

	// Create initial API key
	plainKey, apiKey, err := gw.db.APIKeys().Create(ctx, tenant.ID, "default")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create API key")
		return
	}

	// Generate JWT for immediate login
	token, err := gw.tokens.Generate(tenant.ID, user.ID, user.Email, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":      token,
		"tenant_id":  tenant.ID,
		"user_id":    user.ID,
		"email":      user.Email,
		"role":       user.Role,
		"plan":       tenant.Plan,
		"api_key":    plainKey,
		"key_prefix": apiKey.KeyPrefix,
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":     claims.UserID,
		"email":       claims.Email,
		"role":        claims.Role,
		"tenant_id":   claims.TenantID,
		"tenant_name": tenant.Name,
		"plan":        tenant.Plan,
	})
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

		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}

		plain, key, err := gw.db.APIKeys().Create(r.Context(), claims.TenantID, req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create key")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"key":        plain,
			"key_id":     key.ID,
			"key_prefix": key.KeyPrefix,
			"name":       key.Name,
		})

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
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		log.Printf("Webhook signature verification failed: %v", err)
		writeError(w, http.StatusBadRequest, "invalid webhook signature")
		return
	}

	ctx := r.Context()
	log.Printf("Stripe webhook: type=%s customer=%s subscription=%s status=%s plan=%s",
		event.Type, event.CustomerID, event.SubscriptionID, event.Status, event.PlanName)

	// Find tenant by Stripe customer ID
	tenants, err := gw.db.Tenants().List(ctx)
	if err != nil {
		log.Printf("Webhook error: failed to list tenants: %v", err)
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
		log.Printf("Webhook: no tenant found for Stripe customer %s", event.CustomerID)
		// Return 200 to acknowledge — Stripe retries on non-2xx
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated":
		// Update plan based on price ID
		if event.PlanName != "" && event.PlanName != "unknown" {
			if err := gw.db.Tenants().UpdatePlan(ctx, tenant.ID, event.PlanName); err != nil {
				log.Printf("Webhook error: failed to update plan for tenant %s: %v", tenant.ID, err)
			} else {
				log.Printf("Webhook: updated tenant %s plan to %s", tenant.ID, event.PlanName)
			}
		}
		// Update subscription ID
		if err := gw.db.Tenants().UpdateStripe(ctx, tenant.ID, event.CustomerID, event.SubscriptionID); err != nil {
			log.Printf("Webhook error: failed to update stripe IDs for tenant %s: %v", tenant.ID, err)
		}
		// Reactivate if previously suspended
		if event.Status == "active" && tenant.Status != "active" {
			if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "active"); err != nil {
				log.Printf("Webhook error: failed to reactivate tenant %s: %v", tenant.ID, err)
			}
		}

	case "customer.subscription.deleted":
		// Subscription canceled — mark tenant as churned
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "churned"); err != nil {
			log.Printf("Webhook error: failed to mark tenant %s as churned: %v", tenant.ID, err)
		} else {
			log.Printf("Webhook: marked tenant %s as churned", tenant.ID)
		}

	case "invoice.payment_failed":
		// Payment failed — suspend tenant
		if err := gw.db.Tenants().UpdateStatus(ctx, tenant.ID, "suspended"); err != nil {
			log.Printf("Webhook error: failed to suspend tenant %s: %v", tenant.ID, err)
		} else {
			log.Printf("Webhook: suspended tenant %s due to payment failure", tenant.ID)
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
		log.Printf("Failed to create portal session for tenant %s: %v", tenant.ID, err)
		writeError(w, http.StatusInternalServerError, "failed to create billing portal session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleBillingUsage returns current event usage for the tenant.
func (gw *gateway) handleBillingUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	ctx := r.Context()

	// Get today's count and this month's total
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":   claims.TenantID,
		"today":       todayCount,
		"month_total": monthTotal,
		"period":      fmt.Sprintf("%d-%02d", year, month),
	})
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
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
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
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
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
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
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
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
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
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
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
		writeJSON(w, http.StatusOK, existing)

	case http.MethodDelete:
		if claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
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
		log.Printf("Alert evaluator: failed to list active rules: %v", err)
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
			log.Printf("Alert evaluator: failed to generate token for tenant %s: %v", tenantID, err)
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
					log.Printf("Alert evaluator: tenant=%s rule=%s anomaly error: %v",
						tenantID, rule.ID, err)
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
					log.Printf("Alert evaluator: tenant=%s rule=%s error querying metric: %v",
						tenantID, rule.ID, err)
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

			log.Printf("Alert evaluator: TRIGGERED tenant=%s rule=%q metric=%s value=%.4f",
				tenantID, rule.Name, rule.Metric, value)

			// Dispatch notification
			ch, err := gw.db.NotificationChannels().GetByID(ctx, rule.ChannelID)
			if err != nil {
				log.Printf("Alert evaluator: channel %s not found for rule %s: %v",
					rule.ChannelID, rule.ID, err)
				continue
			}

			cfg, err := notify.ParseChannelConfig(ch.Config)
			if err != nil {
				log.Printf("Alert evaluator: invalid config for channel %s: %v", ch.ID, err)
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
				log.Printf("Alert evaluator: failed to send notification for rule %s: %v", rule.ID, err)
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
				log.Printf("Alert evaluator: failed to record history for rule %s: %v", rule.ID, err)
			}

			// Update last triggered
			if err := gw.db.AlertRules().UpdateLastTriggered(ctx, rule.ID, now); err != nil {
				log.Printf("Alert evaluator: failed to update last_triggered for rule %s: %v", rule.ID, err)
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
		log.Printf("Alert evaluator: anomaly rule %s skipped — only %d historical data points (need ≥3)",
			rule.ID, len(historicalValues))
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
		log.Printf("Usage metering: failed to list tenants: %v", err)
		return
	}

	today := time.Now().UTC().Format("2006-01-02")

	for _, tenant := range tenants {
		if tenant.Status != "active" || tenant.StripeSubscriptionID == "" {
			continue
		}

		count, err := gw.db.EventCounters().GetCount(ctx, tenant.ID, today)
		if err != nil {
			log.Printf("Usage metering: failed to get count for tenant %s: %v", tenant.ID, err)
			continue
		}

		if count == 0 {
			continue
		}

		if err := gw.billing.ReportUsage(ctx, tenant.StripeSubscriptionID, count, time.Now().UTC()); err != nil {
			log.Printf("Usage metering: failed to report usage for tenant %s: %v", tenant.ID, err)
		} else {
			log.Printf("Usage metering: reported %d events for tenant %s", count, tenant.ID)
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
		log.Printf("DLQ list error: %v", err)
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
			log.Printf("DLQ read error for %s: %v", file, err)
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
func (gw *gateway) handleDLQReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

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

	// Build JSONL body from raw entries
	var buf bytes.Buffer
	for i, e := range req.Entries {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(e.RawJSON)
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
	fwdReq.Header.Set("Content-Type", "application/x-ndjson")

	// Forward the Authorization header
	if auth := r.Header.Get("Authorization"); auth != "" {
		fwdReq.Header.Set("Authorization", auth)
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

	// Relay the ingestion response
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}
