package com.gravix.sdk;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.time.Instant;

import static org.junit.jupiter.api.Assertions.*;

class RequestFactTest {

    private ObjectMapper mapper;

    @BeforeEach
    void setUp() {
        mapper = new ObjectMapper()
                .registerModule(new JavaTimeModule())
                .disable(SerializationFeature.WRITE_DATES_AS_TIMESTAMPS);
    }

    // --- Builder validation ---

    @Test
    void buildsMinimalFact() {
        RequestFact f = RequestFact.builder()
                .service("svc")
                .method("GET")
                .pathTemplate("/api/v1/health")
                .statusCode(200)
                .latencyMs(10)
                .build();

        assertEquals("svc", f.getService());
        assertEquals("GET", f.getMethod());
        assertEquals("/api/v1/health", f.getPathTemplate());
        assertEquals(200, f.getStatusCode());
        assertEquals(10, f.getLatencyMs());
    }

    @Test
    void buildsFullFact() {
        Instant now = Instant.parse("2026-03-23T10:00:00Z");
        RequestFact f = RequestFact.builder()
                .eventId("evt-1")
                .eventTime(now)
                .service("order-service")
                .method("POST")
                .pathTemplate("/orders/{id}")
                .statusCode(201)
                .latencyMs(42)
                .userAgentFamily("Mozilla")
                .build();

        assertEquals("evt-1", f.getEventId());
        assertEquals(now, f.getEventTime());
        assertEquals("Mozilla", f.getUserAgentFamily());
    }

    @Test
    void rejectsInvalidStatusCode() {
        assertThrows(IllegalStateException.class, () ->
                RequestFact.builder().service("s").method("GET").pathTemplate("/").statusCode(99).latencyMs(0).build()
        );
        assertThrows(IllegalStateException.class, () ->
                RequestFact.builder().service("s").method("GET").pathTemplate("/").statusCode(600).latencyMs(0).build()
        );
    }

    @Test
    void rejectsNegativeLatency() {
        assertThrows(IllegalStateException.class, () ->
                RequestFact.builder().service("s").method("GET").pathTemplate("/").statusCode(200).latencyMs(-1).build()
        );
    }

    @Test
    void rejectsMissingRequiredFields() {
        // service is optional at build time (may be filled by GravixClient.defaultService)
        assertThrows(IllegalStateException.class, () ->
                RequestFact.builder().service("s").pathTemplate("/").statusCode(200).latencyMs(0).build()
        );
        assertThrows(IllegalStateException.class, () ->
                RequestFact.builder().service("s").method("GET").statusCode(200).latencyMs(0).build()
        );
    }

    @Test
    void allowsOmittedService() {
        // service may be absent; GravixClient will enrich it from defaultService
        RequestFact f = RequestFact.builder()
                .method("GET").pathTemplate("/health").statusCode(200).latencyMs(0).build();
        assertNull(f.getService());
    }

    // --- Jackson serialization ---

    @Test
    void serializesToSnakeCaseJson() throws Exception {
        Instant ts = Instant.parse("2026-03-23T10:00:00Z");
        RequestFact f = RequestFact.builder()
                .eventId("id-1")
                .eventTime(ts)
                .service("svc")
                .method("DELETE")
                .pathTemplate("/users/{id}")
                .statusCode(204)
                .latencyMs(7)
                .userAgentFamily("curl")
                .build();

        String json = mapper.writeValueAsString(f);
        JsonNode node = mapper.readTree(json);

        assertEquals("id-1", node.get("event_id").asText());
        assertEquals("2026-03-23T10:00:00Z", node.get("event_time").asText());
        assertEquals("svc", node.get("service").asText());
        assertEquals("DELETE", node.get("method").asText());
        assertEquals("/users/{id}", node.get("path_template").asText());
        assertEquals(204, node.get("status_code").asInt());
        assertEquals(7, node.get("latency_ms").asLong());
        assertEquals("curl", node.get("user_agent_family").asText());
    }

    @Test
    void omitsNullFieldsFromJson() throws Exception {
        RequestFact f = RequestFact.builder()
                .service("svc")
                .method("GET")
                .pathTemplate("/health")
                .statusCode(200)
                .latencyMs(1)
                .build();

        String json = mapper.writeValueAsString(f);
        JsonNode node = mapper.readTree(json);

        assertFalse(node.has("event_id"), "event_id should be omitted when null");
        assertFalse(node.has("event_time"), "event_time should be omitted when null");
        assertFalse(node.has("user_agent_family"), "user_agent_family should be omitted when null");
    }

    @Test
    void roundTripsViaJson() throws Exception {
        Instant ts = Instant.parse("2026-03-23T12:34:56Z");
        RequestFact original = RequestFact.builder()
                .eventId("round-trip")
                .eventTime(ts)
                .service("payment-service")
                .method("POST")
                .pathTemplate("/payments/{id}/capture")
                .statusCode(200)
                .latencyMs(123)
                .userAgentFamily("okhttp")
                .build();

        String json = mapper.writeValueAsString(original);
        RequestFact parsed = mapper.readValue(json, RequestFact.class);

        assertEquals(original.getEventId(), parsed.getEventId());
        assertEquals(original.getEventTime(), parsed.getEventTime());
        assertEquals(original.getService(), parsed.getService());
        assertEquals(original.getMethod(), parsed.getMethod());
        assertEquals(original.getPathTemplate(), parsed.getPathTemplate());
        assertEquals(original.getStatusCode(), parsed.getStatusCode());
        assertEquals(original.getLatencyMs(), parsed.getLatencyMs());
        assertEquals(original.getUserAgentFamily(), parsed.getUserAgentFamily());
    }

    // --- toBuilder / withX ---

    @Test
    void toBuilderProducesEqualFields() {
        RequestFact original = RequestFact.builder()
                .service("svc").method("GET").pathTemplate("/").statusCode(200).latencyMs(0).build();
        RequestFact copy = original.toBuilder().build();

        assertEquals(original.getService(), copy.getService());
        assertEquals(original.getMethod(), copy.getMethod());
    }

    @Test
    void withServiceOverridesService() {
        RequestFact f = RequestFact.builder()
                .service("old").method("GET").pathTemplate("/").statusCode(200).latencyMs(0).build();
        RequestFact updated = f.withService("new");

        assertEquals("new", updated.getService());
        assertEquals("old", f.getService()); // original unchanged
    }
}
