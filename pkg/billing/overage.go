package billing

// OverageResult describes the overage for a tenant's usage.
type OverageResult struct {
	Plan       string
	EventLimit int64
	EventCount int64
	Overage    int64 // events above the limit (0 if within)
	OverageGB  float64 // overage in GB (assuming 1KB per event)
}

// CalculateOverage computes the overage for a tenant given their plan and current usage.
// Returns zero overage for unlimited plans (enterprise) or usage within limits.
func CalculateOverage(plan string, eventCount int64) OverageResult {
	limit := PlanEventLimit(plan)
	result := OverageResult{
		Plan:       plan,
		EventLimit: limit,
		EventCount: eventCount,
	}

	if limit == 0 {
		// Unlimited plan (enterprise)
		return result
	}

	if eventCount > limit {
		result.Overage = eventCount - limit
		// Approximate 1KB per event for GB calculation
		result.OverageGB = float64(result.Overage) / 1_000_000
	}
	return result
}

// OverageCostCents returns the cost in cents for overage events.
// Pricing: $1 per 100K events overage ($0.00001 per event = 0.001 cents per event).
func OverageCostCents(overage int64) int64 {
	if overage <= 0 {
		return 0
	}
	// $1 per 100K = 100 cents per 100K = 0.001 cents per event
	// Round up to nearest cent
	cents := (overage + 99_999) / 100_000
	return cents
}
