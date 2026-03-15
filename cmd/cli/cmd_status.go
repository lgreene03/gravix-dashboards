package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runStatus(args []string) {
	fs := flag.NewFlagSet("gravix status", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "Ingestion endpoint (env: GRAVIX_ENDPOINT)")
	fs.Parse(args)

	ep := resolveStr(*endpoint, getEndpoint())
	ep = strings.TrimRight(ep, "/")

	client := &http.Client{Timeout: 5 * time.Second}

	fmt.Printf("Gravix Ingestion Service — %s\n\n", ep)

	checks := []struct {
		name string
		path string
	}{
		{"Liveness  (/live)", "/live"},
		{"Readiness (/ready)", "/ready"},
	}

	allOK := true
	for _, c := range checks {
		status, body := probe(client, ep+c.path)
		icon := "✓"
		if status != 200 {
			icon = "✗"
			allOK = false
		}
		fmt.Printf("  %s %-24s  %d  %s\n", icon, c.name, status, body)
	}

	// Metrics endpoint — just check reachability, don't print full body.
	metricsStatus, _ := probe(client, ep+"/metrics")
	icon := "✓"
	if metricsStatus != 200 {
		icon = "✗"
		allOK = false
	}
	fmt.Printf("  %s %-24s  %d\n", icon, "Metrics   (/metrics)", metricsStatus)

	fmt.Println()
	if allOK {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed.")
		os.Exit(1)
	}
}

func probe(client *http.Client, url string) (int, string) {
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Sprintf("error: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return resp.StatusCode, strings.TrimSpace(string(body))
}
