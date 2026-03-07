// Gateway service provides tenant management, user login, and API key management
// for multi-tenant Gravix deployments.
//
// Endpoints:
//
//	POST /api/gateway/login       — Authenticate user, return JWT
//	GET  /api/gateway/me          — Get current user info (JWT required)
//	POST /api/gateway/api-keys    — Create API key (JWT required, admin only)
//	GET  /api/gateway/api-keys    — List API keys (JWT required)
//	DELETE /api/gateway/api-keys/:id — Revoke API key (JWT required, admin only)
//	GET  /live                    — Health check
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/auth"
	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
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

	gw := &gateway{db: db, tokens: tokens}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/gateway/login", gw.handleLogin)
	mux.HandleFunc("/api/gateway/me", gw.requireAuth(gw.handleMe))
	mux.HandleFunc("/api/gateway/api-keys", gw.requireAuth(gw.handleAPIKeys))
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("up"))
	})

	// CORS middleware for dashboard access
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Gateway service starting on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type gateway struct {
	db     tenantdb.DB
	tokens *auth.TokenService
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
