package com.gravix.sdk;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;

class SanitizerTest {

    // --- UUID replacement ---

    @Test
    void replacesUuidInPathSegment() {
        String raw = "/users/550e8400-e29b-41d4-a716-446655440000";
        assertEquals("/users/{id}", Sanitizer.sanitizePath(raw));
    }

    @Test
    void replacesMultipleUuids() {
        String raw = "/orgs/00000000-0000-0000-0000-000000000001/members/00000000-0000-0000-0000-000000000002";
        assertEquals("/orgs/{id}/members/{id}", Sanitizer.sanitizePath(raw));
    }

    @Test
    void handlesUpperCaseUuid() {
        String raw = "/items/550E8400-E29B-41D4-A716-446655440000/details";
        assertEquals("/items/{id}/details", Sanitizer.sanitizePath(raw));
    }

    // --- Numeric ID replacement ---

    @Test
    void replacesNumericIdFourDigits() {
        assertEquals("/orders/{id}/items", Sanitizer.sanitizePath("/orders/1234/items"));
    }

    @Test
    void replacesNumericIdManyDigits() {
        assertEquals("/orders/{id}", Sanitizer.sanitizePath("/orders/9876543210"));
    }

    @Test
    void doesNotReplaceThreeDigitNumber() {
        // 3 digits should NOT be replaced
        assertEquals("/v1/api/123", Sanitizer.sanitizePath("/v1/api/123"));
    }

    @Test
    void doesNotReplaceShortNumbers() {
        assertEquals("/status/200", Sanitizer.sanitizePath("/status/200"));
    }

    @Test
    void replacesNumericIdInMiddleSegment() {
        assertEquals("/api/v1/{id}/detail", Sanitizer.sanitizePath("/api/v1/99999/detail"));
    }

    // --- Query parameter stripping ---

    @Test
    void stripsQueryString() {
        assertEquals("/search", Sanitizer.sanitizePath("/search?q=foo&limit=10"));
    }

    @Test
    void stripsQueryStringBeforeSanitization() {
        assertEquals("/users/{id}", Sanitizer.sanitizePath("/users/550e8400-e29b-41d4-a716-446655440000?expand=true"));
    }

    @Test
    void pathWithNoQueryStringUnchanged() {
        assertEquals("/api/v1/health", Sanitizer.sanitizePath("/api/v1/health"));
    }

    // --- Mixed cases ---

    @Test
    void mixedUuidAndNumericId() {
        String raw = "/tenant/550e8400-e29b-41d4-a716-446655440000/order/56789";
        assertEquals("/tenant/{id}/order/{id}", Sanitizer.sanitizePath(raw));
    }

    @Test
    void collapseConsecutivePlaceholders() {
        // Two UUIDs back-to-back in path
        String raw = "/a/550e8400-e29b-41d4-a716-446655440000/550e8400-e29b-41d4-a716-446655440001";
        assertEquals("/a/{id}", Sanitizer.sanitizePath(raw));
    }

    // --- Edge cases ---

    @Test
    void emptyPathReturnsEmpty() {
        assertEquals("", Sanitizer.sanitizePath(""));
    }

    @Test
    void nullPathReturnsNull() {
        assertNull(Sanitizer.sanitizePath(null));
    }

    @Test
    void rootPathUnchanged() {
        assertEquals("/", Sanitizer.sanitizePath("/"));
    }

    @Test
    void pathWithNoIdSegments() {
        assertEquals("/api/v1/health", Sanitizer.sanitizePath("/api/v1/health"));
    }
}
