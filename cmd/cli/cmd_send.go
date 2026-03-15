package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	gravix "github.com/gravix-io/gravix-go"
)

func runSendFact(args []string) {
	fs := flag.NewFlagSet("gravix send fact", flag.ExitOnError)
	service := fs.String("service", "", "Service name (required)")
	method := fs.String("method", "", "HTTP method: GET, POST, PUT, DELETE, etc. (required)")
	path := fs.String("path", "", "Path template, e.g. /api/v1/users/{id} (required)")
	status := fs.Int("status", 0, "HTTP status code 100-599 (required)")
	latency := fs.Int("latency", 0, "Latency in milliseconds (required)")
	endpoint := fs.String("endpoint", "", "Ingestion endpoint (env: GRAVIX_ENDPOINT)")
	apiKey := fs.String("api-key", "", "API key (env: GRAVIX_API_KEY)")
	userAgent := fs.String("user-agent", "", "User agent family (optional)")

	fs.Parse(args)

	// Resolve config from flags or env.
	ep := resolveStr(*endpoint, getEndpoint())
	key := resolveStr(*apiKey, requireAPIKey())

	// Validate required fields.
	var missing []string
	if *service == "" {
		missing = append(missing, "--service")
	}
	if *method == "" {
		missing = append(missing, "--method")
	}
	if *path == "" {
		missing = append(missing, "--path")
	}
	if *status == 0 {
		missing = append(missing, "--status")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "Error: missing required flags: %s\n", strings.Join(missing, ", "))
		fs.PrintDefaults()
		os.Exit(1)
	}

	client := gravix.New(ep, key,
		gravix.WithService(*service),
		gravix.WithAutoSanitize(true),
		gravix.WithMaxRetries(2),
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.SendFact(ctx, gravix.RequestFact{
		Method:          strings.ToUpper(*method),
		PathTemplate:    *path,
		StatusCode:      *status,
		LatencyMs:       *latency,
		UserAgentFamily: *userAgent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Fact sent successfully.")
}

func runSendEvent(args []string) {
	fs := flag.NewFlagSet("gravix send event", flag.ExitOnError)
	service := fs.String("service", "", "Service name (required)")
	eventType := fs.String("type", "", "Event type, e.g. deploy_completed (required)")
	message := fs.String("message", "", "Human-readable message (optional)")
	entityID := fs.String("entity-id", "", "Related entity identifier (optional)")
	endpoint := fs.String("endpoint", "", "Ingestion endpoint (env: GRAVIX_ENDPOINT)")
	apiKey := fs.String("api-key", "", "API key (env: GRAVIX_API_KEY)")

	// Properties as repeated key=value flags.
	var props propertyFlags
	fs.Var(&props, "prop", "Property key=value pair (repeatable)")

	fs.Parse(args)

	ep := resolveStr(*endpoint, getEndpoint())
	key := resolveStr(*apiKey, requireAPIKey())

	var missing []string
	if *service == "" {
		missing = append(missing, "--service")
	}
	if *eventType == "" {
		missing = append(missing, "--type")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "Error: missing required flags: %s\n", strings.Join(missing, ", "))
		fs.PrintDefaults()
		os.Exit(1)
	}

	client := gravix.New(ep, key,
		gravix.WithService(*service),
		gravix.WithMaxRetries(2),
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	event := gravix.ServiceEvent{
		Service:   *service,
		EventType: *eventType,
		Message:   *message,
		EntityID:  *entityID,
	}
	if len(props.pairs) > 0 {
		event.Properties = props.pairs
	}

	err := client.RecordEvent(ctx, event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Event sent successfully.")
}

// resolveStr returns the flag value if non-empty, otherwise the fallback.
func resolveStr(flag, fallback string) string {
	if flag != "" {
		return flag
	}
	return fallback
}

// propertyFlags implements flag.Value for repeatable -prop key=value flags.
type propertyFlags struct {
	pairs map[string]string
}

func (p *propertyFlags) String() string {
	return fmt.Sprintf("%v", p.pairs)
}

func (p *propertyFlags) Set(val string) error {
	if p.pairs == nil {
		p.pairs = make(map[string]string)
	}
	parts := strings.SplitN(val, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("property must be in key=value format, got: %s", val)
	}
	p.pairs[parts[0]] = parts[1]
	return nil
}
