package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

func main() {
	url := "http://localhost:8090/api/v1/facts"
	apiKey := "secret-token-123"

	for i := 0; i < 20; i++ {
		// Generate UUIDv7
		id, err := uuid.NewV7()
		if err != nil {
			log.Fatalf("failed to generate uuidv7: %v", err)
		}

		fact := map[string]interface{}{
			"event_id":      id.String(),
			"service":       "load-gen-s3",
			"method":        "GET",
			"path_template": "/test/api",
			"status_code":   201,
			"latency_ms":    10 + (i * 5),
			"event_time":    time.Now().UTC().Format(time.RFC3339),
		}

		body, _ := json.Marshal(fact)
		req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", apiKey)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed to send request: %v", err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("Sent request %d: status %d\n", i, resp.StatusCode)
	}
}
