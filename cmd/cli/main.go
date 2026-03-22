// Command gravix is a CLI tool for the Gravix observability platform.
//
// Usage:
//
//	gravix send fact   --service=auth --method=GET --path=/users/{id} --status=200 --latency=42
//	gravix send event  --service=auth --type=deploy_completed --message="v1.2.3"
//	gravix status      [--endpoint=http://localhost:8090]
//	gravix tail dlq    [--follow]
//
// Environment variables:
//
//	GRAVIX_API_KEY   – API key (required for send commands)
//	GRAVIX_ENDPOINT  – Ingestion endpoint (default: http://localhost:8090)
package main

import (
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
  gravix tail dlq     Tail the dead-letter queue
  gravix replay       Replay DLQ entries back to ingestion
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
