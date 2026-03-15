package gravix_test

import (
	"context"
	"fmt"

	gravix "github.com/gravix-io/gravix-go"
)

func ExampleNew() {
	// Create a client with automatic batching and path sanitization.
	client := gravix.New(
		"http://localhost:8090",
		"your-api-key",
		gravix.WithService("my-api"),
		gravix.WithBatchSize(50),
	)
	defer client.Close()

	// Record a request fact (batched automatically).
	client.RecordFact(context.Background(), gravix.RequestFact{
		Method:       "GET",
		PathTemplate: "/api/v1/users/{id}",
		StatusCode:   200,
		LatencyMs:    42,
	})

	fmt.Println("fact recorded")
	// Output: fact recorded
}

func ExampleClient_RecordEvent() {
	client := gravix.New("http://localhost:8090", "your-api-key")
	defer client.Close()

	// Record a deploy event (sent synchronously).
	err := client.RecordEvent(context.Background(), gravix.ServiceEvent{
		Service:   "my-api",
		EventType: "deploy_completed",
		Message:   "Deployed v1.2.3",
		Properties: map[string]string{
			"version": "1.2.3",
			"branch":  "main",
		},
	})
	if err != nil {
		fmt.Printf("error: %v\n", err)
	}
}

func ExampleSanitizePath() {
	// UUIDs are replaced with {id}.
	fmt.Println(gravix.SanitizePath("/users/550e8400-e29b-41d4-a716-446655440000/orders"))

	// Numeric IDs (4+ digits) are replaced with {id}.
	fmt.Println(gravix.SanitizePath("/api/v1/products/12345"))

	// Already-templated paths are left unchanged.
	fmt.Println(gravix.SanitizePath("/api/v1/users/{id}"))

	// Output:
	// /users/{id}/orders
	// /api/v1/products/{id}
	// /api/v1/users/{id}
}
