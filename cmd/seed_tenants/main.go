// Command seed_tenants creates demo tenants and API keys in the tenant database.
//
// Usage:
//
//	go run ./cmd/seed_tenants/ -db ./data/gravix.db
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

type seedTenant struct {
	Name  string
	Email string
	Plan  string
}

var defaultTenants = []seedTenant{
	{Name: "Acme Corp", Email: "admin@acme.example.com", Plan: "starter"},
	{Name: "Beta Inc", Email: "admin@beta.example.com", Plan: "free"},
	{Name: "Gamma Ltd", Email: "admin@gamma.example.com", Plan: "pro"},
}

func main() {
	dbPath := flag.String("db", "./data/gravix.db", "Path to tenant SQLite database")
	flag.Parse()

	db, err := tenantdb.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	for _, st := range defaultTenants {
		// Check if tenant already exists
		if existing, _ := db.Tenants().GetByEmail(ctx, st.Email); existing != nil {
			fmt.Printf("  Tenant %q already exists (id=%s)\n", st.Name, existing.ID)
			continue
		}

		tenant := &tenantdb.Tenant{
			Name:   st.Name,
			Email:  st.Email,
			Plan:   st.Plan,
			Status: "active",
		}
		if err := db.Tenants().Create(ctx, tenant); err != nil {
			log.Fatalf("Failed to create tenant %q: %v", st.Name, err)
		}

		// Create an API key for each tenant
		plainKey, key, err := db.APIKeys().Create(ctx, tenant.ID, "default", nil)
		if err != nil {
			log.Fatalf("Failed to create API key for %q: %v", st.Name, err)
		}

		// Create an admin user for each tenant
		user := &tenantdb.User{
			TenantID:     tenant.ID,
			Email:        st.Email,
			PasswordHash: "changeme", // will be bcrypt-hashed by Create()
			Role:         "admin",
		}
		if err := db.Users().Create(ctx, user); err != nil {
			log.Fatalf("Failed to create user for %q: %v", st.Name, err)
		}

		fmt.Printf("  Created tenant %q (plan=%s)\n", st.Name, st.Plan)
		fmt.Printf("    Tenant ID:  %s\n", tenant.ID)
		fmt.Printf("    API Key:    %s\n", plainKey)
		fmt.Printf("    Key Prefix: %s\n", key.KeyPrefix)
		fmt.Printf("    User:       %s (password: changeme)\n", st.Email)
		fmt.Println()
	}

	// Print summary
	tenants, _ := db.Tenants().List(ctx)
	fmt.Printf("Total tenants in database: %d\n", len(tenants))

	fmt.Println("\nTo use multi-tenant mode, set:")
	fmt.Fprintf(os.Stderr, "  export TENANT_DB_PATH=%s\n", *dbPath)
}
