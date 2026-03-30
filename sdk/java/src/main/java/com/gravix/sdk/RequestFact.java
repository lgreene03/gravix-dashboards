package com.gravix.sdk;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.time.Instant;

/**
 * An immutable HTTP request observation sent to the Gravix ingestion API.
 *
 * <p>Fields match the Go schema defined in {@code schemas/request_fact.go}.
 * Use the {@link Builder} to construct instances.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class RequestFact {

    @JsonProperty("event_id")
    private final String eventId;

    @JsonProperty("event_time")
    private final Instant eventTime;

    @JsonProperty("service")
    private final String service;

    @JsonProperty("method")
    private final String method;

    @JsonProperty("path_template")
    private final String pathTemplate;

    @JsonProperty("status_code")
    private final int statusCode;

    @JsonProperty("latency_ms")
    private final long latencyMs;

    @JsonProperty("user_agent_family")
    private final String userAgentFamily;

    private RequestFact(Builder b) {
        this.eventId = b.eventId;
        this.eventTime = b.eventTime;
        this.service = b.service;
        this.method = b.method;
        this.pathTemplate = b.pathTemplate;
        this.statusCode = b.statusCode;
        this.latencyMs = b.latencyMs;
        this.userAgentFamily = b.userAgentFamily;
    }

    /** Jackson deserialization constructor. */
    @JsonCreator
    static RequestFact jsonCreate(
            @JsonProperty("event_id") String eventId,
            @JsonProperty("event_time") Instant eventTime,
            @JsonProperty("service") String service,
            @JsonProperty("method") String method,
            @JsonProperty("path_template") String pathTemplate,
            @JsonProperty("status_code") int statusCode,
            @JsonProperty("latency_ms") long latencyMs,
            @JsonProperty("user_agent_family") String userAgentFamily) {
        return new Builder()
                .eventId(eventId).eventTime(eventTime)
                .service(service).method(method).pathTemplate(pathTemplate)
                .statusCode(statusCode).latencyMs(latencyMs)
                .userAgentFamily(userAgentFamily)
                .build();
    }

    // --- Accessors ---

    public String getEventId() { return eventId; }
    public Instant getEventTime() { return eventTime; }
    public String getService() { return service; }
    public String getMethod() { return method; }
    public String getPathTemplate() { return pathTemplate; }
    public int getStatusCode() { return statusCode; }
    public long getLatencyMs() { return latencyMs; }
    public String getUserAgentFamily() { return userAgentFamily; }

    /** Returns a copy of this fact with the given field values overridden. */
    public RequestFact withEventId(String eventId) {
        return toBuilder().eventId(eventId).build();
    }

    public RequestFact withEventTime(Instant eventTime) {
        return toBuilder().eventTime(eventTime).build();
    }

    public RequestFact withService(String service) {
        return toBuilder().service(service).build();
    }

    public RequestFact withPathTemplate(String pathTemplate) {
        return toBuilder().pathTemplate(pathTemplate).build();
    }

    public Builder toBuilder() {
        return new Builder()
                .eventId(eventId)
                .eventTime(eventTime)
                .service(service)
                .method(method)
                .pathTemplate(pathTemplate)
                .statusCode(statusCode)
                .latencyMs(latencyMs)
                .userAgentFamily(userAgentFamily);
    }

    public static Builder builder() { return new Builder(); }

    public static final class Builder {
        private String eventId;
        private Instant eventTime;
        private String service;
        private String method;
        private String pathTemplate;
        private int statusCode;
        private long latencyMs;
        private String userAgentFamily;

        public Builder eventId(String v) { this.eventId = v; return this; }
        public Builder eventTime(Instant v) { this.eventTime = v; return this; }
        public Builder service(String v) { this.service = v; return this; }
        public Builder method(String v) { this.method = v; return this; }
        public Builder pathTemplate(String v) { this.pathTemplate = v; return this; }
        public Builder statusCode(int v) { this.statusCode = v; return this; }
        public Builder latencyMs(long v) { this.latencyMs = v; return this; }
        public Builder userAgentFamily(String v) { this.userAgentFamily = v; return this; }

        /**
         * Build the fact.
         *
         * <p>{@code service} may be omitted here when the {@link GravixClient} is
         * configured with a {@code defaultService} that will be applied before
         * the fact is sent (e.g. when constructing facts inside
         * {@link GravixMiddleware}).
         */
        public RequestFact build() {
            if (method == null || method.isEmpty()) throw new IllegalStateException("method is required");
            if (pathTemplate == null || pathTemplate.isEmpty()) throw new IllegalStateException("pathTemplate is required");
            if (statusCode < 100 || statusCode > 599) throw new IllegalStateException("statusCode must be 100–599");
            if (latencyMs < 0) throw new IllegalStateException("latencyMs must be >= 0");
            return new RequestFact(this);
        }
    }
}
