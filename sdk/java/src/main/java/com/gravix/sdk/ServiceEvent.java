package com.gravix.sdk;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.time.Instant;
import java.util.Collections;
import java.util.Map;

/**
 * A lifecycle event (deploy, restart, scale, etc.) associated with a service.
 *
 * <p>Events are sent synchronously and are not batched.
 * Use the {@link Builder} to construct instances.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class ServiceEvent {

    @JsonProperty("event_id")
    private final String eventId;

    @JsonProperty("event_time")
    private final Instant eventTime;

    @JsonProperty("service")
    private final String service;

    @JsonProperty("event_type")
    private final String eventType;

    @JsonProperty("description")
    private final String description;

    @JsonProperty("metadata")
    private final Map<String, String> metadata;

    /** Jackson deserialization constructor. */
    @JsonCreator
    static ServiceEvent jsonCreate(
            @JsonProperty("event_id") String eventId,
            @JsonProperty("event_time") Instant eventTime,
            @JsonProperty("service") String service,
            @JsonProperty("event_type") String eventType,
            @JsonProperty("description") String description,
            @JsonProperty("metadata") java.util.Map<String, String> metadata) {
        return new Builder()
                .eventId(eventId).eventTime(eventTime)
                .service(service).eventType(eventType)
                .description(description).metadata(metadata)
                .build();
    }

    private ServiceEvent(Builder b) {
        this.eventId = b.eventId;
        this.eventTime = b.eventTime;
        this.service = b.service;
        this.eventType = b.eventType;
        this.description = b.description;
        this.metadata = b.metadata != null ? Collections.unmodifiableMap(b.metadata) : null;
    }

    public String getEventId() { return eventId; }
    public Instant getEventTime() { return eventTime; }
    public String getService() { return service; }
    public String getEventType() { return eventType; }
    public String getDescription() { return description; }
    public Map<String, String> getMetadata() { return metadata; }

    public ServiceEvent withEventId(String eventId) { return toBuilder().eventId(eventId).build(); }
    public ServiceEvent withEventTime(Instant eventTime) { return toBuilder().eventTime(eventTime).build(); }
    public ServiceEvent withService(String service) { return toBuilder().service(service).build(); }

    public Builder toBuilder() {
        return new Builder()
                .eventId(eventId)
                .eventTime(eventTime)
                .service(service)
                .eventType(eventType)
                .description(description)
                .metadata(metadata);
    }

    public static Builder builder() { return new Builder(); }

    public static final class Builder {
        private String eventId;
        private Instant eventTime;
        private String service;
        private String eventType;
        private String description;
        private Map<String, String> metadata;

        public Builder eventId(String v) { this.eventId = v; return this; }
        public Builder eventTime(Instant v) { this.eventTime = v; return this; }
        public Builder service(String v) { this.service = v; return this; }
        public Builder eventType(String v) { this.eventType = v; return this; }
        public Builder description(String v) { this.description = v; return this; }
        public Builder metadata(Map<String, String> v) { this.metadata = v; return this; }

        public ServiceEvent build() {
            if (service == null || service.isEmpty()) throw new IllegalStateException("service is required");
            if (eventType == null || eventType.isEmpty()) throw new IllegalStateException("eventType is required");
            return new ServiceEvent(this);
        }
    }
}
