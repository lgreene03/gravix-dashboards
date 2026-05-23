package com.gravix.sdk;

import javax.servlet.Filter;
import javax.servlet.FilterChain;
import javax.servlet.FilterConfig;
import javax.servlet.ServletException;
import javax.servlet.ServletRequest;
import javax.servlet.ServletResponse;
import javax.servlet.http.HttpServletRequest;
import javax.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.List;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Servlet {@link Filter} that records {@link RequestFact} observations for every
 * HTTP request passing through the filter chain.
 *
 * <p>Register this filter in your servlet container (e.g. Spring Boot, Tomcat,
 * Jetty) and supply a configured {@link GravixClient}. The filter:
 * <ol>
 *   <li>Captures method, URI, and response status.</li>
 *   <li>Measures end-to-end latency in milliseconds.</li>
 *   <li>Sanitizes the path template (UUIDs and numeric IDs replaced with {@code {id}}).</li>
 *   <li>Enqueues a {@link RequestFact} via the client's internal batcher.</li>
 * </ol>
 *
 * <p>Spring Boot example:
 * <pre>{@code
 * @Bean
 * public FilterRegistrationBean<GravixMiddleware> gravixFilter(GravixClient client) {
 *     FilterRegistrationBean<GravixMiddleware> reg = new FilterRegistrationBean<>();
 *     reg.setFilter(new GravixMiddleware(client, "my-service"));
 *     reg.addUrlPatterns("/*");
 *     reg.setOrder(Ordered.HIGHEST_PRECEDENCE);
 *     return reg;
 * }
 * }</pre>
 */
public final class GravixMiddleware implements Filter {

    private static final Logger LOG = Logger.getLogger(GravixMiddleware.class.getName());

    private final GravixClient client;
    private final String service;

    /**
     * @param client  configured GravixClient (caller retains ownership)
     * @param service service name stamped on every fact; may be empty to rely on
     *                the client's {@code defaultService}
     */
    public GravixMiddleware(GravixClient client, String service) {
        if (client == null) throw new IllegalArgumentException("client must not be null");
        this.client = client;
        this.service = service != null ? service : "";
    }

    /** Convenience constructor that relies on the client's {@code defaultService}. */
    public GravixMiddleware(GravixClient client) {
        this(client, "");
    }

    @Override
    public void init(FilterConfig filterConfig) {
        // nothing to initialise
    }

    @Override
    public void doFilter(ServletRequest req, ServletResponse res, FilterChain chain)
            throws IOException, ServletException {

        if (!(req instanceof HttpServletRequest)) {
            chain.doFilter(req, res);
            return;
        }

        HttpServletRequest httpReq = (HttpServletRequest) req;
        HttpServletResponse httpRes = (HttpServletResponse) res;

        long start = System.currentTimeMillis();
        try {
            chain.doFilter(req, res);
        } finally {
            long latencyMs = System.currentTimeMillis() - start;
            try {
                String rawPath = httpReq.getRequestURI();
                String pathTemplate = Sanitizer.sanitizePath(rawPath);

                String svc = service.isEmpty() ? null : service;

                RequestFact.Builder fb = RequestFact.builder()
                        .method(httpReq.getMethod())
                        .pathTemplate(pathTemplate)
                        .statusCode(httpRes.getStatus())
                        .latencyMs(latencyMs);

                if (svc != null) fb.service(svc);

                // userAgentFamily: use the raw User-Agent header (GravixClient will not re-sanitize)
                String ua = httpReq.getHeader("User-Agent");
                if (ua != null && !ua.isEmpty()) fb.userAgentFamily(ua);

                client.sendFacts(List.of(fb.build()));
            } catch (Exception e) {
                LOG.log(Level.WARNING, "gravix middleware: failed to record fact", e);
            }
        }
    }

    @Override
    public void destroy() {
        // filter does not own the client lifecycle
    }
}
