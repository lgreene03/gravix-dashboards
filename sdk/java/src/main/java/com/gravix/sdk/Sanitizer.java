package com.gravix.sdk;

import java.util.regex.Pattern;

/**
 * Path template sanitization — port of the Go/Python sanitize logic.
 *
 * <p>Replaces raw UUIDs and numeric IDs (4+ digits) in URL path segments
 * with {@code {id}} placeholders, and strips query parameters.
 *
 * <p>Examples:
 * <pre>
 *   /users/550e8400-e29b-41d4-a716-446655440000  →  /users/{id}
 *   /orders/12345/items                           →  /orders/{id}/items
 *   /v1/things?filter=foo                         →  /v1/things
 * </pre>
 */
public final class Sanitizer {

    // UUID v1–v7 pattern
    private static final Pattern UUID_RE = Pattern.compile(
            "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
    );

    // Numeric IDs: 4+ digit path segments (anchored to full segment)
    private static final Pattern NUMERIC_ID_RE = Pattern.compile("^\\d{4,}$");

    // Prevent instantiation
    private Sanitizer() {}

    /**
     * Sanitize a URL path, returning a path template safe for aggregation.
     *
     * @param path raw URL path (may include query string)
     * @return sanitized path template
     */
    public static String sanitizePath(String path) {
        if (path == null || path.isEmpty()) {
            return path;
        }

        // Strip query parameters
        int qIdx = path.indexOf('?');
        if (qIdx >= 0) {
            path = path.substring(0, qIdx);
        }

        // Replace UUIDs first (more specific pattern)
        path = UUID_RE.matcher(path).replaceAll("{id}");

        // Replace numeric-only path segments (4+ digits)
        String[] parts = path.split("/", -1);
        for (int i = 0; i < parts.length; i++) {
            if (NUMERIC_ID_RE.matcher(parts[i]).matches()) {
                parts[i] = "{id}";
            }
        }
        path = String.join("/", parts);

        // Collapse consecutive {id}/{id} placeholders
        while (path.contains("/{id}/{id}")) {
            path = path.replace("/{id}/{id}", "/{id}");
        }

        return path;
    }
}
