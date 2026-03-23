package com.gravix.sdk;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.locks.ReentrantLock;
import java.util.function.Consumer;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Thread-safe Gravix ingestion client with automatic batching.
 *
 * <p>Facts are buffered in memory and flushed either when the batch reaches
 * {@code batchSize} entries or when the {@code flushInterval} elapses —
 * whichever comes first. Service events are sent synchronously.
 *
 * <p>Usage:
 * <pre>{@code
 * GravixClient client = GravixClient.builder()
 *         .baseUrl("http://localhost:8090")
 *         .apiKey("my-key")
 *         .defaultService("order-service")
 *         .build();
 *
 * client.sendFacts(List.of(
 *     RequestFact.builder()
 *         .service("order-service")
 *         .method("GET")
 *         .pathTemplate("/orders/{id}")
 *         .statusCode(200)
 *         .latencyMs(42)
 *         .build()
 * ));
 *
 * client.close();
 * }</pre>
 */
public final class GravixClient implements AutoCloseable {

    private static final Logger LOG = Logger.getLogger(GravixClient.class.getName());

    private final String baseUrl;
    private final String apiKey;
    private final String defaultService;
    private final int batchSize;
    private final boolean autoSanitize;
    private final Consumer<Exception> onError;

    private final HttpClient http;
    private final ObjectMapper mapper;
    private final ScheduledExecutorService scheduler;

    private final ReentrantLock lock = new ReentrantLock();
    private final List<RequestFact> buffer = new ArrayList<>();
    private volatile boolean closed = false;

    private GravixClient(Builder b) {
        this.baseUrl = b.baseUrl.replaceAll("/+$", "");
        this.apiKey = b.apiKey;
        this.defaultService = b.defaultService;
        this.batchSize = b.batchSize;
        this.autoSanitize = b.autoSanitize;
        this.onError = b.onError;

        this.http = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(5))
                .build();

        this.mapper = new ObjectMapper()
                .registerModule(new JavaTimeModule())
                .disable(SerializationFeature.WRITE_DATES_AS_TIMESTAMPS);

        // Daemon scheduler so it does not block JVM shutdown
        this.scheduler = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "gravix-flusher");
            t.setDaemon(true);
            return t;
        });
        scheduler.scheduleAtFixedRate(
                this::flushBackground,
                b.flushIntervalMs, b.flushIntervalMs, TimeUnit.MILLISECONDS
        );
    }

    // -------------------------------------------------------------------------
    // Public API
    // -------------------------------------------------------------------------

    /**
     * Enqueue facts for batched delivery. Auto-flushes when {@code batchSize} is reached.
     */
    public void sendFacts(List<RequestFact> facts) {
        if (facts == null || facts.isEmpty()) return;
        List<RequestFact> enriched = new ArrayList<>(facts.size());
        for (RequestFact f : facts) {
            enriched.add(enrich(f));
        }

        lock.lock();
        try {
            buffer.addAll(enriched);
            if (buffer.size() >= batchSize) {
                flushLocked();
            }
        } finally {
            lock.unlock();
        }
    }

    /**
     * Send service events synchronously (not batched).
     */
    public void sendEvents(List<ServiceEvent> events) {
        if (events == null || events.isEmpty()) return;
        for (ServiceEvent e : events) {
            ServiceEvent enriched = enrichEvent(e);
            try {
                String body = mapper.writeValueAsString(enriched);
                post("/api/v1/events", body);
            } catch (Exception ex) {
                handleError(ex);
            }
        }
    }

    /**
     * Flush all buffered facts and release resources.
     */
    @Override
    public void close() {
        closed = true;
        scheduler.shutdown();
        flush();
    }

    /**
     * Force-flush all buffered facts immediately.
     */
    public void flush() {
        lock.lock();
        try {
            flushLocked();
        } finally {
            lock.unlock();
        }
    }

    // -------------------------------------------------------------------------
    // Internal helpers
    // -------------------------------------------------------------------------

    /** Called by the scheduled executor. */
    private void flushBackground() {
        try {
            flush();
        } catch (Exception e) {
            handleError(e);
        }
    }

    /** Must be called with {@link #lock} held. */
    private void flushLocked() {
        if (buffer.isEmpty()) return;
        List<RequestFact> batch = new ArrayList<>(buffer);
        buffer.clear();

        // Build JSONL payload
        StringBuilder sb = new StringBuilder();
        try {
            for (RequestFact f : batch) {
                sb.append(mapper.writeValueAsString(f)).append('\n');
            }
            post("/api/v1/facts/batch", sb.toString());
        } catch (Exception e) {
            handleError(e);
        }
    }

    private RequestFact enrich(RequestFact f) {
        RequestFact.Builder b = f.toBuilder();
        if (f.getEventId() == null || f.getEventId().isEmpty()) {
            b.eventId(UUID.randomUUID().toString());
        }
        if (f.getEventTime() == null) {
            b.eventTime(Instant.now());
        }
        if ((f.getService() == null || f.getService().isEmpty()) && defaultService != null) {
            b.service(defaultService);
        }
        if (autoSanitize && f.getPathTemplate() != null) {
            b.pathTemplate(Sanitizer.sanitizePath(f.getPathTemplate()));
        }
        return b.build();
    }

    private ServiceEvent enrichEvent(ServiceEvent e) {
        ServiceEvent.Builder b = e.toBuilder();
        if (e.getEventId() == null || e.getEventId().isEmpty()) {
            b.eventId(UUID.randomUUID().toString());
        }
        if (e.getEventTime() == null) {
            b.eventTime(Instant.now());
        }
        if ((e.getService() == null || e.getService().isEmpty()) && defaultService != null) {
            b.service(defaultService);
        }
        return b.build();
    }

    private void post(String path, String body) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(baseUrl + path))
                .header("Content-Type", "application/json")
                .header("X-API-Key", apiKey)
                .timeout(Duration.ofSeconds(10))
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build();

        HttpResponse<String> response = http.send(request, HttpResponse.BodyHandlers.ofString());
        int status = response.statusCode();
        if (status < 200 || status >= 300) {
            throw new IOException("Gravix API error " + status + ": " + truncate(response.body(), 200));
        }
    }

    private void handleError(Exception e) {
        LOG.log(Level.WARNING, "gravix: flush error: " + e.getMessage(), e);
        if (onError != null) {
            try { onError.accept(e); } catch (Exception ignored) {}
        }
    }

    private static String truncate(String s, int max) {
        return s != null && s.length() > max ? s.substring(0, max) + "..." : s;
    }

    // -------------------------------------------------------------------------
    // Builder
    // -------------------------------------------------------------------------

    public static Builder builder() { return new Builder(); }

    public static final class Builder {
        private String baseUrl = "http://localhost:8090";
        private String apiKey = "";
        private String defaultService;
        private int batchSize = 100;
        private long flushIntervalMs = 5_000;
        private boolean autoSanitize = true;
        private Consumer<Exception> onError;

        /** Base URL of the Gravix ingestion service (no trailing slash). */
        public Builder baseUrl(String v) { this.baseUrl = v; return this; }

        /** API key sent as {@code X-API-Key} header. */
        public Builder apiKey(String v) { this.apiKey = v; return this; }

        /** Default service name applied when facts/events omit it. */
        public Builder defaultService(String v) { this.defaultService = v; return this; }

        /** Maximum facts buffered before an automatic flush. Default: 100. */
        public Builder batchSize(int v) { this.batchSize = v; return this; }

        /** Interval between periodic flushes in milliseconds. Default: 5000. */
        public Builder flushIntervalMs(long v) { this.flushIntervalMs = v; return this; }

        /** If true, path templates are sanitized automatically. Default: true. */
        public Builder autoSanitize(boolean v) { this.autoSanitize = v; return this; }

        /** Callback invoked on background flush errors. */
        public Builder onError(Consumer<Exception> v) { this.onError = v; return this; }

        public GravixClient build() {
            if (baseUrl == null || baseUrl.isEmpty()) throw new IllegalStateException("baseUrl is required");
            if (batchSize <= 0) throw new IllegalStateException("batchSize must be > 0");
            if (flushIntervalMs <= 0) throw new IllegalStateException("flushIntervalMs must be > 0");
            return new GravixClient(this);
        }
    }
}
