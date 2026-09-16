// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gravix is a CLI tool for the Gravix observability platform.
//
// Usage:
//
//	gravix send fact   --service=auth --method=GET --path=/users/{id} --status=200 --latency=42
//	gravix send event  --service=auth --type=deploy_completed --message="v1.2.3"
//	gravix status      [--endpoint=http://localhost:8090]
//	gravix tail dlq    [--follow]
//	gravix recompute   --from=2026-09-01 --to=2026-09-08
//	gravix evolve      add-percentile --quantile=0.999 --from=2026-08-12 --to=2026-09-11
//	gravix plugin new  --name=gravix-notifier-demo --kind=notifier
//	gravix migrate export-cloud --tenant-id=ten_abc --since=2026-01-01
//	gravix migrate import-cloud --tenant-dir-name=ten_abc --api-key=$GRAVIX_API_KEY
//
// Environment variables:
//
//	GRAVIX_API_KEY   – API key (required for send commands)
//	GRAVIX_ENDPOINT  – Ingestion endpoint (default: http://localhost:8090)
package main

import (
	"context"
	"fmt"
	"os"
)

const defaultEndpoint = "http://localhost:8090"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "send":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: gravix send <fact|event> [flags]\n")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "fact":
			runSendFact(os.Args[3:])
		case "event":
			runSendEvent(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "Unknown send subcommand: %s\nUsage: gravix send <fact|event> [flags]\n", os.Args[2])
			os.Exit(1)
		}
	case "status":
		runStatus(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "tail":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: gravix tail <dlq> [flags]\n")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "dlq":
			runTailDLQ(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "Unknown tail subcommand: %s\nUsage: gravix tail <dlq> [flags]\n", os.Args[2])
			os.Exit(1)
		}
	case "replay":
		runReplay(os.Args[2:])
	case "recompute":
		runRecompute(os.Args[2:])
	case "import":
		runImport(os.Args[2:])
	case "migrate":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: gravix migrate <export-cloud|import-cloud> [flags]\n")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "export-cloud":
			runMigrateExportCloud(os.Args[3:])
		case "import-cloud":
			runMigrateImportCloud(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "Unknown migrate subcommand: %s\nUsage: gravix migrate <export-cloud|import-cloud> [flags]\n", os.Args[2])
			os.Exit(1)
		}
	case "export":
		os.Exit(exportMain(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	case "plugin":
		os.Exit(pluginMain(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	case "evolve":
		runEvolve(os.Args[2:])
	case "explain":
		runExplain(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	case "version", "--version":
		fmt.Println("gravix v0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Gravix CLI – send metrics and events to the Gravix observability platform.

Usage:
  gravix send fact    Send a single request fact
  gravix send event   Send a service lifecycle event
  gravix status       Check ingestion service health
  gravix doctor       Diagnose setup failures and print the fix for each
  gravix tail dlq     Tail the dead-letter queue
  gravix replay       Replay DLQ entries back to ingestion
  gravix recompute    Rebuild derived metrics from raw facts
  gravix evolve       Add a percentile or dimension and backfill history
  gravix explain      Show where a number came from
  gravix import       Import history from Prometheus or Datadog
  gravix export       Export facts, metrics or events out of Gravix
  gravix migrate      Move a tenant between Gravix Cloud and a self-hosted install
  gravix plugin       Scaffold, list and validate plugins
  gravix version      Print version
  gravix help         Show this help

Environment:
  GRAVIX_API_KEY      API key for authentication (required for send commands)
  GRAVIX_ENDPOINT     Ingestion service URL (default: http://localhost:8090)

Run 'gravix send fact --help' or 'gravix send event --help' for command-specific flags.
`)
}

func getEndpoint() string {
	if v := os.Getenv("GRAVIX_ENDPOINT"); v != "" {
		return v
	}
	return defaultEndpoint
}

func getAPIKey() string {
	return os.Getenv("GRAVIX_API_KEY")
}

func requireAPIKey() string {
	key := getAPIKey()
	if key == "" {
		fmt.Fprintf(os.Stderr, "Error: GRAVIX_API_KEY environment variable is required.\n")
		os.Exit(1)
	}
	return key
}
