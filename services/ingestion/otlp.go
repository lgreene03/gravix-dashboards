package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// OTLPTraceRequest represents a simplified OTLP trace export request (JSON format).
type OTLPTraceRequest struct {
	ResourceSpans []ResourceSpan `json:"resourceSpans"`
}

// ResourceSpan contains spans from a single resource.
type ResourceSpan struct {
	Resource   OTLPResource `json:"resource"`
	ScopeSpans []ScopeSpan  `json:"scopeSpans"`
}

// OTLPResource describes the entity producing telemetry.
type OTLPResource struct {
	Attributes []OTLPAttribute `json:"attributes"`
}

// ScopeSpan contains spans from a single instrumentation scope.
type ScopeSpan struct {
	Spans []OTLPSpan `json:"spans"`
}

// OTLPSpan represents an individual span.
type OTLPSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []OTLPAttribute `json:"attributes"`
	Status            OTLPStatus      `json:"status"`
}

// OTLPAttribute is a key-value pair.
type OTLPAttribute struct {
	Key   string        `json:"key"`
	Value OTLPAttrValue `json:"value"`
}

// OTLPAttrValue holds the attribute value (only stringValue and intValue supported).
type OTLPAttrValue struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    string `json:"intValue,omitempty"`
}

// OTLPStatus represents span status.
type OTLPStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const otlpMaxBodyBytes = 10 << 20 // 10 MB max for OTLP trace payloads

// handleOTLPTraces returns a handler for POST /v1/traces that accepts OTLP JSON
// traces and converts HTTP spans to RequestFacts written to the durable sink.
func handleOTLPTraces(sink *DurableSink) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, otlpMaxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 10MB)")
			return
		}
		defer r.Body.Close()

		var traceReq OTLPTraceRequest
		if err := json.Unmarshal(body, &traceReq); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}

		tenantID := getTenantID(r)
		converted := 0
		skipped := 0

		for _, rs := range traceReq.ResourceSpans {
			serviceName := extractResourceAttr(rs.Resource, "service.name")
			if serviceName == "" {
				serviceName = "unknown"
			}

			for _, ss := range rs.ScopeSpans {
				for _, span := range ss.Spans {
					fact, ok := spanToFact(span, serviceName)
					if !ok {
						skipped++
						continue
					}

					factBytes, err := json.Marshal(fact)
					if err != nil {
						slog.Warn("failed to marshal fact from OTLP span", "error", err)
						skipped++
						continue
					}

					topic := topicForTenant(tenantID, "request_facts")
					if err := sink.Write(topic, factBytes); err != nil {
						slog.Error("sink write error for OTLP span", "error", err)
						skipped++
						continue
					}
					converted++
				}
			}
		}

		slog.Info("OTLP traces processed",
			"tenant", tenantID,
			"converted", converted,
			"skipped", skipped)

		ingestionRequestsTotal.WithLabelValues("/v1/traces", "200", tenantID).Inc()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"partialSuccess": map[string]interface{}{
				"rejectedSpans": skipped,
			},
		})
	}
}

// spanToFact converts an OTLP span to a RequestFact-like map.
// Returns false if the span is not an HTTP span and should be skipped.
func spanToFact(span OTLPSpan, serviceName string) (map[string]interface{}, bool) {
	method := getSpanAttr(span, "http.method")
	if method == "" {
		method = getSpanAttr(span, "http.request.method")
	}
	if method == "" {
		// Not an HTTP span — skip
		return nil, false
	}

	route := getSpanAttr(span, "http.route")
	if route == "" {
		route = getSpanAttr(span, "http.target")
	}
	if route == "" {
		route = span.Name
	}

	statusCode := getSpanAttrInt(span, "http.status_code")
	if statusCode == 0 {
		statusCode = getSpanAttrInt(span, "http.response.status_code")
	}
	if statusCode == 0 {
		statusCode = 200
	}

	// Calculate latency from start/end times
	var latencyMs float64
	startNano := parseNano(span.StartTimeUnixNano)
	endNano := parseNano(span.EndTimeUnixNano)
	if startNano > 0 && endNano > startNano {
		latencyMs = float64(endNano-startNano) / 1e6
	}

	eventTime := time.Unix(0, startNano).UTC().Format(time.RFC3339Nano)

	userAgent := getSpanAttr(span, "http.user_agent")
	if userAgent == "" {
		userAgent = getSpanAttr(span, "user_agent.original")
	}
	uaFamily := "other"
	if userAgent != "" {
		uaFamily = simplifyUserAgent(userAgent)
	}

	fact := map[string]interface{}{
		"event_id":          span.SpanID,
		"event_time":        eventTime,
		"service":           serviceName,
		"method":            strings.ToUpper(method),
		"path_template":     route,
		"status_code":       statusCode,
		"latency_ms":        latencyMs,
		"user_agent_family": uaFamily,
	}

	return fact, true
}

// extractResourceAttr extracts an attribute value from a resource.
func extractResourceAttr(res OTLPResource, key string) string {
	for _, attr := range res.Attributes {
		if attr.Key == key {
			return attr.Value.StringValue
		}
	}
	return ""
}

// getSpanAttr returns the string value of a span attribute.
func getSpanAttr(span OTLPSpan, key string) string {
	for _, attr := range span.Attributes {
		if attr.Key == key {
			return attr.Value.StringValue
		}
	}
	return ""
}

// getSpanAttrInt returns the int value of a span attribute.
func getSpanAttrInt(span OTLPSpan, key string) int {
	for _, attr := range span.Attributes {
		if attr.Key == key {
			if attr.Value.IntValue != "" {
				v := 0
				fmt.Sscanf(attr.Value.IntValue, "%d", &v)
				return v
			}
			if attr.Value.StringValue != "" {
				v := 0
				fmt.Sscanf(attr.Value.StringValue, "%d", &v)
				return v
			}
		}
	}
	return 0
}

// parseNano parses a nanosecond timestamp string.
func parseNano(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

// simplifyUserAgent extracts a browser/client family name from a user agent string.
func simplifyUserAgent(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "chrome"):
		return "chrome"
	case strings.Contains(ua, "firefox"):
		return "firefox"
	case strings.Contains(ua, "safari"):
		return "safari"
	case strings.Contains(ua, "curl"):
		return "curl"
	case strings.Contains(ua, "python"):
		return "python"
	case strings.Contains(ua, "go-http"):
		return "go"
	case strings.Contains(ua, "java"):
		return "java"
	case strings.Contains(ua, "node"):
		return "node"
	default:
		return "other"
	}
}
