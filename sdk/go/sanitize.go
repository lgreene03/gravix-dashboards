package gravix

import "regexp"

// Pre-compiled regexes matching the server-side validation in schemas/request_fact.go.
var (
	// Matches standard UUID format (any version).
	uuidRegex = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)

	// Matches numeric IDs with 4 or more digits in path segments.
	rawIDRegex = regexp.MustCompile(`/[0-9]{4,}(/|$)`)

	// Matches hex strings of 16+ characters (common for hashes, tokens).
	hexTokenRegex = regexp.MustCompile(`/[a-fA-F0-9]{16,}(/|$)`)
)

// SanitizePath replaces raw UUIDs, numeric IDs (4+ digits), and hex tokens
// in URL path segments with the {id} placeholder. This prevents
// high-cardinality path explosion in metrics.
//
// Examples:
//
//	"/users/550e8400-e29b-41d4-a716-446655440000/orders" → "/users/{id}/orders"
//	"/api/v1/products/12345" → "/api/v1/products/{id}"
//	"/api/v1/products/12345/" → "/api/v1/products/{id}/"
func SanitizePath(path string) string {
	// Replace UUIDs first (most specific).
	result := uuidRegex.ReplaceAllString(path, "{id}")

	// Replace numeric IDs (4+ digits) in path segments.
	// The regex matches /DIGITS/ or /DIGITS$, so we need to preserve the trailing delimiter.
	result = rawIDRegex.ReplaceAllStringFunc(result, func(match string) string {
		if match[len(match)-1] == '/' {
			return "/{id}/"
		}
		return "/{id}"
	})

	// Replace long hex tokens.
	result = hexTokenRegex.ReplaceAllStringFunc(result, func(match string) string {
		if match[len(match)-1] == '/' {
			return "/{id}/"
		}
		return "/{id}"
	})

	return result
}
