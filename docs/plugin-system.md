# Plugin System Design

## Overview

Gravix supports extensibility through a plugin interface that allows custom transforms, notification channels, and storage backends to be added without modifying core code. Plugins are loaded at startup and registered with the appropriate subsystem.

## Plugin Types

### 1. Transform Plugins

Transform plugins process raw facts or events into derived data. They implement the `etl.Transform` interface:

```go
type Transform interface {
    Process(ctx context.Context, store storage.ObjectStore, cfg Config, day time.Time, files []InputFile) (*ProcessResult, error)
}
```

**Use cases:**
- Custom metric aggregations (e.g., percentile bucketing by user agent family)
- Business-specific rollups (e.g., revenue per endpoint)
- Data quality checks (e.g., anomaly detection on ingested facts)

**Registration:**
```go
// In your plugin's init():
etl.RegisterTransform("my-custom-rollup", myTransformFunc)
```

### 2. Notification Channel Plugins

Notification plugins extend the alerting system to deliver alerts to custom destinations beyond Slack and webhooks.

```go
type ChannelPlugin interface {
    Type() string
    Send(ctx context.Context, payload AlertPayload, config json.RawMessage) error
    ValidateConfig(config json.RawMessage) error
}
```

**Use cases:**
- Microsoft Teams notifications
- SMS via Twilio
- Custom incident management systems
- Email digests

**Registration:**
```go
notify.RegisterChannel("teams", &TeamsPlugin{})
```

### 3. Storage Backend Plugins

Storage plugins add support for additional object stores beyond local filesystem and S3.

```go
// Implement the existing storage.ObjectStore interface:
type ObjectStore interface {
    List(ctx context.Context, prefix string) ([]string, error)
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Put(ctx context.Context, key string, r io.Reader) error
    Delete(ctx context.Context, key string) error
}
```

**Use cases:**
- Google Cloud Storage
- Azure Blob Storage
- HDFS for on-premise deployments

### 4. Auth Provider Plugins

Auth plugins add custom authentication and SSO providers.

```go
type AuthProvider interface {
    Name() string
    InitiateLogin(w http.ResponseWriter, r *http.Request, state string)
    HandleCallback(r *http.Request) (*AuthResult, error)
    ValidateConfig(config json.RawMessage) error
}
```

**Use cases:**
- Okta, Auth0, Azure AD (beyond standard OIDC)
- LDAP/Active Directory
- Custom corporate SSO

## Plugin Loading

### Build-time Plugins (Recommended)

Plugins are Go packages imported in a custom `main.go`:

```go
package main

import (
    "github.com/lgreene/gravix-dashboards/services/gateway"
    _ "github.com/myorg/gravix-plugin-teams"     // auto-registers on import
    _ "github.com/myorg/gravix-plugin-gcs"        // auto-registers on import
)

func main() {
    gateway.Run()
}
```

### Configuration

Plugin-specific configuration lives in environment variables or the tenant database:

```yaml
# Helm values for plugin configuration
gateway:
  plugins:
    teams:
      enabled: true
      webhook_url: "https://outlook.office.com/webhook/..."
    gcs:
      enabled: true
      bucket: "gravix-prod"
      credentials_file: "/secrets/gcp-sa.json"
```

## Plugin Development Guide

### 1. Create a new Go module

```bash
mkdir gravix-plugin-teams && cd gravix-plugin-teams
go mod init github.com/myorg/gravix-plugin-teams
go get github.com/lgreene/gravix-dashboards
```

### 2. Implement the interface

```go
package teams

import (
    "context"
    "encoding/json"
    "net/http"

    "github.com/lgreene/gravix-dashboards/pkg/notify"
)

type TeamsPlugin struct{}

func (p *TeamsPlugin) Type() string { return "teams" }

func (p *TeamsPlugin) Send(ctx context.Context, payload notify.AlertPayload, config json.RawMessage) error {
    var cfg struct {
        WebhookURL string `json:"webhook_url"`
    }
    json.Unmarshal(config, &cfg)
    // Send adaptive card to Teams webhook...
    return nil
}

func (p *TeamsPlugin) ValidateConfig(config json.RawMessage) error {
    var cfg struct {
        WebhookURL string `json:"webhook_url"`
    }
    return json.Unmarshal(config, &cfg)
}

func init() {
    notify.RegisterChannel("teams", &TeamsPlugin{})
}
```

### 3. Test

```go
func TestTeamsSend(t *testing.T) {
    p := &TeamsPlugin{}
    err := p.Send(context.Background(), notify.AlertPayload{
        RuleName: "High Error Rate",
        Metric:   "error_rate",
        Value:    0.15,
    }, json.RawMessage(`{"webhook_url":"https://test.example.com"}`))
    if err != nil {
        t.Fatal(err)
    }
}
```

## Security Considerations

- Plugins run in-process with full access to the gateway's resources
- Plugin configurations containing secrets should use Kubernetes Secrets or external secret managers
- Notification plugins must not log alert payload contents (may contain service names/paths)
- Storage plugins must respect the ObjectStore interface contract including context cancellation

## Future Directions

- **Plugin registry**: Searchable catalog of community plugins
- **Runtime loading**: Support for plugins loaded from shared libraries (`.so` files) without recompilation
- **Plugin versioning**: Compatibility matrix between Gravix versions and plugin versions
- **Sandboxing**: WASM-based plugin execution for untrusted plugins
