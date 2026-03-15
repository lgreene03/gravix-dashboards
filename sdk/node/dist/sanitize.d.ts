/**
 * Path template sanitization.
 *
 * Replaces raw UUIDs, numeric IDs (4+ digits), and hex tokens in URL paths
 * with `{id}` placeholders to prevent high-cardinality metric explosion.
 */
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
export declare function sanitizePath(path: string): string;
