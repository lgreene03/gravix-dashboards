// Command trial_expiry runs as a daily cron job to downgrade tenants
// whose free trial has expired back to the free plan.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

func main() {
	dbPath := flag.String("db", envOr("DB_PATH", "./data/gravix.db"), "SQLite database path")
	dryRun := flag.Bool("dry-run", false, "log expired trials without downgrading")
	flag.Parse()

	slog.Info("trial_expiry starting", "db", *dbPath, "dry_run", *dryRun)

	db, err := tenantdb.Open(*dbPath)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	count, err := processExpiredTrials(ctx, db, *dryRun)
	if err != nil {
		slog.Error("process expired trials", "error", err)
		os.Exit(1)
	}
	slog.Info("trial_expiry complete", "downgraded", count)
}

func processExpiredTrials(ctx context.Context, db tenantdb.DB, dryRun bool) (int, error) {
	tenants, err := db.Tenants().List(ctx)
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	downgraded := 0

	for _, t := range tenants {
		if t.TrialEndsAt == nil {
			continue
		}
		if t.TrialEndsAt.After(now) {
			continue // trial still active
		}
		if t.Plan == "free" {
			continue // already downgraded
		}

		slog.Info("trial expired",
			"tenant_id", t.ID,
			"name", t.Name,
			"plan", t.Plan,
			"trial_ended", t.TrialEndsAt.Format(time.RFC3339),
		)

		if dryRun {
			downgraded++
			continue
		}

		// Downgrade to free
		if err := db.Tenants().UpdatePlan(ctx, t.ID, "free"); err != nil {
			slog.Error("downgrade tenant", "tenant_id", t.ID, "error", err)
			continue
		}
		// Clear trial dates
		if err := db.Tenants().UpdateTrial(ctx, t.ID, nil, nil); err != nil {
			slog.Error("clear trial dates", "tenant_id", t.ID, "error", err)
		}
		downgraded++
	}
	return downgraded, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
