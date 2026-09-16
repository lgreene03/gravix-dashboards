---
title: Writing a plugin
sidebar_position: 6
---

# Writing a plugin

A Gravix plugin is a program that reads JSON-RPC 2.0 requests on stdin and writes responses on
stdout. That is the whole contract. It can be written in any language, it does not link against
Gravix, and it does not break when Gravix upgrades.

## Before you install one: the trust boundary

**Installing a third-party plugin is installing third-party code with unrestricted network access**,
exactly like adding any other dependency. A notifier has to reach Slack, so the host does not
firewall it. The plugin runs as a subprocess of Gravix, in Gravix's working directory, with whatever
the operating system grants that user.

What Gravix *does* guarantee is containment of failure, not containment of intent:

| Property | Rule |
|---|---|
| Timeout | 30s per call by default, then the process is killed |
| Crash | Restarted with exponential backoff up to 5 minutes; the host carries on |
| Failure budget | 5 consecutive failures disable the plugin and raise an alert; it never blocks the pipeline |
| Memory | 256 MB RSS by default; exceeding it kills and restarts the process |
| Secrets | Config fields declared `secret` are never logged, never in an error, never in a `gravix doctor` bundle |

Read a plugin's source before you install it, the same way you would any dependency.

## Why subprocesses rather than Go plugins

Go's `plugin` package loads a shared object into the host process. It requires the plugin to be
built with the identical toolchain and identical dependency versions, it is ELF-only, and a crash in
the plugin takes the host with it. Every Gravix release would break every plugin.

A subprocess costs one process and a JSON round trip per call. A notifier sends a handful of messages
per minute and an exporter runs on a schedule, so that cost buys crash isolation, language freedom
and an ABI that survives upgrades. **No plugin is ever called on the ingestion hot path** — adapters
run at batch boundaries, never between a request and its durable write.

## The three kinds

| Kind | Interface | Called when |
|---|---|---|
| `notifier` | `Notify(alert)` | An alert fires |
| `exporter` | `Export(batch)` | A batch of rows is ready |
| `adapter` | `Convert(payload, contentType)` | A foreign payload needs turning into facts |

An adapter's facts are checked against the cardinality budget **on the Gravix side**. A plugin
promising to respect `docs/04-non-goals.md` §5 is not evidence that it did, and installing something
does not waive it.

## A complete worked example

This is a notifier that writes each alert to a file. It is deliberately small enough to read in one
sitting and it runs as-is.

```go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// The manifest Gravix asks for at startup. abi_version must match the Gravix
// you are running against, or it refuses to load the plugin rather than
// calling it and failing in a confusing way.
var manifest = map[string]any{
	"name":        "file-notifier",
	"version":     "1.0.0",
	"abi_version": "1",
	"kind":        "notifier",
	"description": "appends each alert to a file",
	"license":     "Apache-2.0",
	"config_schema": map[string]any{
		"path": map[string]any{
			"type":        "string",
			"required":    true,
			"description": "file to append alerts to",
		},
	},
}

func main() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return // Gravix closed the pipe; we are done.
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}

		var resp response
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "gravix.describe":
			resp.Result = manifest

		case "gravix.notify":
			var alert struct {
				AlertID  string `json:"alert_id"`
				RuleName string `json:"rule_name"`
			}
			if err := json.Unmarshal(req.Params, &alert); err != nil {
				resp.Error = &rpcErr{Code: -32602, Message: "invalid alert"}
				break
			}
			// Idempotent on alert_id: Gravix may retry, and a duplicate page
			// is worse than a late one. Appending the id lets a reader dedupe.
			f, err := os.OpenFile("alerts.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				resp.Error = &rpcErr{Code: -32000, Message: err.Error()}
				break
			}
			fmt.Fprintf(f, "%s\t%s\n", alert.AlertID, alert.RuleName)
			f.Close()
			resp.Result = map[string]any{"delivered": true}

		default:
			resp.Error = &rpcErr{Code: -32601, Message: "method not found"}
		}

		body, _ := json.Marshal(resp)
		out.Write(body)
		out.WriteByte('\n')
		out.Flush() // One response per line, flushed — Gravix reads line by line.
	}
}
```

Build it and point Gravix at the binary:

```bash
go build -o file-notifier ./cmd/file-notifier
```

### Four things that will bite you

1. **Flush after every response.** Gravix reads one line at a time. A buffered response that never
   flushes looks exactly like a hung plugin, and gets killed at the timeout.
2. **One JSON object per line.** A pretty-printed response spanning lines will not parse.
3. **Never write anything else to stdout.** A stray `fmt.Println` corrupts the RPC stream. Debug
   output goes to stderr, which Gravix forwards.
4. **Return errors as JSON-RPC errors, not by exiting.** Exiting counts as a crash and spends your
   failure budget; a returned error is a clean failure Gravix can report.

## Declaring configuration

Every field in `config_schema` is typed. Use `secret` for anything that must not be logged:

```json
"config_schema": {
  "webhook_url": {"type": "string", "required": true},
  "token":       {"type": "secret", "required": true}
}
```

A `secret` field may not declare a default — a default in a manifest ends up in every copy of the
plugin's documentation.

## The ABI version

`abi_version` is `1`. It changes only when an existing method's request or response shape changes;
adding a method does not change it. A plugin declaring a different version is refused at startup with
a message naming both versions, so you know which build to fetch:

```
plugin "file-notifier" declares ABI 2, this Gravix supports 1
```
