# Gravix Deploy Event Action

A GitHub Action that sends deploy lifecycle events to the [Gravix](https://github.com/gravix-io/gravix) observability platform. Automatically record when deploys start, complete, or fail — directly from your CI/CD pipeline.

## Usage

```yaml
- name: Send deploy event
  uses: gravix-io/gravix/action@main
  with:
    api-key: ${{ secrets.GRAVIX_API_KEY }}
    endpoint: ${{ secrets.GRAVIX_ENDPOINT }}
    service: my-api
    event-type: deploy_completed
    message: "Deployed v1.2.3 to production"
    properties: |
      version=1.2.3
      commit=${{ github.sha }}
      environment=production
```

## Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `api-key` | Yes | — | Gravix API key for authentication |
| `endpoint` | No | `http://localhost:8090` | Gravix ingestion service URL |
| `service` | Yes | — | Service name (e.g., `auth-service`) |
| `event-type` | No | `deploy_completed` | Event type in snake_case |
| `message` | No | `""` | Human-readable event message |
| `entity-id` | No | `""` | Related entity identifier |
| `properties` | No | `""` | Key=value pairs, one per line |

## Common Event Types

| Type | When to use |
|------|-------------|
| `deploy_started` | Before your deploy step |
| `deploy_completed` | After a successful deploy |
| `deploy_failed` | On deploy failure (`if: failure()`) |
| `scale_up` / `scale_down` | Scaling events |
| `restart` | Service restarts |
| `health_check_failed` | When a health check fails |

## Full Example

Track the complete deploy lifecycle — start, success, and failure:

```yaml
name: Deploy

on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      # Record deploy start
      - name: Gravix – deploy started
        uses: gravix-io/gravix/action@main
        with:
          api-key: ${{ secrets.GRAVIX_API_KEY }}
          endpoint: ${{ secrets.GRAVIX_ENDPOINT }}
          service: my-api
          event-type: deploy_started
          message: "Deploying ${{ github.sha }}"
          entity-id: "deployment/my-api-${{ github.run_id }}"
          properties: |
            commit=${{ github.sha }}
            branch=${{ github.ref_name }}
            actor=${{ github.actor }}

      # Your deploy steps
      - name: Deploy to production
        run: ./deploy.sh

      # Record success
      - name: Gravix – deploy completed
        if: success()
        uses: gravix-io/gravix/action@main
        with:
          api-key: ${{ secrets.GRAVIX_API_KEY }}
          endpoint: ${{ secrets.GRAVIX_ENDPOINT }}
          service: my-api
          event-type: deploy_completed
          message: "Successfully deployed ${{ github.sha }}"
          entity-id: "deployment/my-api-${{ github.run_id }}"

      # Record failure
      - name: Gravix – deploy failed
        if: failure()
        uses: gravix-io/gravix/action@main
        with:
          api-key: ${{ secrets.GRAVIX_API_KEY }}
          endpoint: ${{ secrets.GRAVIX_ENDPOINT }}
          service: my-api
          event-type: deploy_failed
          message: "Deploy failed for ${{ github.sha }}"
          entity-id: "deployment/my-api-${{ github.run_id }}"
```

## Setup

1. Add your Gravix API key as a repository secret: `GRAVIX_API_KEY`
2. Add your Gravix endpoint as a secret: `GRAVIX_ENDPOINT`
3. Reference the action in your workflow

## How It Works

The action builds and runs the `gravix` CLI tool inside a Docker container. It maps the action inputs to `gravix send event` flags and sends the event to your Gravix ingestion endpoint. The CLI handles retry with exponential backoff on transient errors (429, 5xx).
