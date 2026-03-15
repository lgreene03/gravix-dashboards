/**
 * Path template sanitization.
 *
 * Replaces raw UUIDs, numeric IDs (4+ digits), and hex tokens in URL paths
 * with `{id}` placeholders to prevent high-cardinality metric explosion.
 */

/** Matches standard UUID format (any version). */
const UUID_RE =
  /[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}/g;

/** Matches numeric IDs with 4 or more digits in path segments. */
const RAW_ID_RE = /\/[0-9]{4,}(\/|$)/g;

/** Matches hex strings of 16+ characters in path segments. */
const HEX_TOKEN_RE = /\/[a-fA-F0-9]{16,}(\/|$)/g;

function replaceSegment(match: string): string {
  return match.endsWith("/") ? "/{id}/" : "/{id}";
}

/**
 * Replace raw IDs in a URL path with `{id}` placeholders.
 *
 * @example
 * sanitizePath("/users/550e8400-e29b-41d4-a716-446655440000/orders")
 * // => "/users/{id}/orders"
 *
 * sanitizePath("/api/v1/products/12345")
 * // => "/api/v1/products/{id}"
 */
export function sanitizePath(path: string): string {
  let result = path.replace(UUID_RE, "{id}");
  result = result.replace(RAW_ID_RE, replaceSegment);
  result = result.replace(HEX_TOKEN_RE, replaceSegment);
  return result;
}
