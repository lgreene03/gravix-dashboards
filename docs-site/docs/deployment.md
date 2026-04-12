---
title: Deployment
sidebar_position: 2
---

# Deployment Quickstart

This guide walks through deploying Gravix locally with Docker Compose and in production with Kubernetes and Helm.

## Docker Compose (Local / Staging)

### 1. Clone and configure

```bash
git clone https://github.com/lgreene/gravix-dashboards.git
cd gravix-dashboards
cp .env.example .env
```

Open `.env` and set the required values:

| Variable | Required | Description |
|----------|----------|-------------|
| `API_KEY` | Yes | Ingestion API key (min 16 characters) |
| `JWT_SECRET` | Yes | Gateway JWT signing secret (min 32 characters) |
| `MINIO_ROOT_PASSWORD` | Yes | MinIO root password |
| `S3_ACCESS_KEY` | Yes | S3/MinIO access key |
| `S3_SECRET_KEY` | Yes | S3/MinIO secret key |

All other variables have sensible defaults for local development. See `.env.example` for the full list.

### 2. Start all services

```bash
docker compose up -d --build
```

This starts 14 containers: ingestion, gateway, dashboard, Trino, Cube.js, MinIO, Prometheus, Grafana, three rollup jobs, a purge job, a load generator, and init containers for MinIO and Trino schema setup.

### 3. Verify the deployment

Wait about 60 seconds for all services to become healthy, then confirm each is running:

| Service | URL | What to check |
|---------|-----|---------------|
| Ingestion API | http://localhost:8090/live | Returns 200 |
| Gateway API | http://localhost:8091/live | Returns 200 |
| Dashboard | http://localhost:8000/index.html | Renders the dashboard UI |
| Grafana | http://localhost:3000 | Login page loads |
| Prometheus | http://localhost:9090 | Query UI loads |
| Trino UI | http://localhost:8081 | Trino web interface |
| Cube Playground | http://localhost:4000 | Cube.js developer UI |
| MinIO Console | http://localhost:9001 | MinIO login page |

You can also check container health in one command:

```bash
docker compose ps
```

All services should show `healthy` or `running`.

### 4. Verify data flow

The built-in load generator sends synthetic request facts at 5 QPS. To confirm data is flowing end to end:

1. Check that JSONL files are being written:

```bash
ls data/raw/request_facts/
```

2. Wait five minutes for the request-metrics rollup to run, then check for Parquet output:

```bash
ls data/warehouse/request_metrics_minute/
```

3. Open the dashboard at http://localhost:8000/index.html and confirm latency and error rate charts are populated.

### Service port reference

| Service | Host Port | Container Port | Network Access |
|---------|-----------|----------------|----------------|
| Ingestion | 8090 | 8080 | Public |
| Gateway | 8091 | 8091 | Public |
| Dashboard | 8000 | 8000 | Public |
| Cube.js | 4000 | 4000 | Public |
| Trino | 8081 | 8080 | Localhost only |
| Prometheus | 9090 | 9090 | Localhost only |
| Grafana | 3000 | 3000 | Localhost only |
| MinIO API | 9000 | 9000 | Localhost only |
| MinIO Console | 9001 | 9001 | Localhost only |

Services bound to localhost only (`127.0.0.1`) are not accessible from other machines on the network.

## Kubernetes / Helm (Production)

Gravix ships a Helm chart in `deploy/gravix/`.

### 1. Build and push images

Build the three application images and push them to your container registry:

```bash
docker build -t <registry>/gravix-ingestion:0.1.0 -f services/ingestion/Dockerfile .
docker build -t <registry>/gravix-load-generator:0.1.0 -f services/load_generator/Dockerfile .
docker build -t <registry>/gravix-rollup:0.1.0 -f services/rollup/Dockerfile .

docker push <registry>/gravix-ingestion:0.1.0
docker push <registry>/gravix-load-generator:0.1.0
docker push <registry>/gravix-rollup:0.1.0
```

### 2. Configure values

Copy the default values file and customize it:

```bash
cp deploy/gravix/values.yaml my-values.yaml
```

At minimum, set these in your values file or via `--set`:

```yaml
global:
  apiKey: "<your-api-key>"
  jwtSecret: "<your-jwt-secret>"
  cubeApiSecret: "<your-cube-secret>"
  imageRegistry: "<your-registry>"
  storage:
    accessKey: "<s3-access-key>"
    secretKey: "<s3-secret-key>"
```

The chart includes environment-specific overrides in `values-dev.yaml` and `values-prod.yaml`.

### 3. Install the chart

```bash
helm install gravix ./deploy/gravix -f my-values.yaml -n gravix --create-namespace
```

### 4. Verify

```bash
kubectl get pods -n gravix
kubectl get svc -n gravix
```

Check the ingestion and gateway liveness endpoints:

```bash
kubectl port-forward svc/gravix-ingestion 8090:8080 -n gravix &
curl http://localhost:8090/live
```

### Production features in the Helm chart

The chart includes templates for:

- **Ingress** with TLS termination (`ingress.yaml`)
- **Horizontal Pod Autoscalers** for ingestion and Cube (`hpa.yaml`, `cube-hpa.yaml`)
- **Pod Disruption Budgets** (`pdb.yaml`)
- **Network Policies** (`networkpolicy.yaml`)
- **Prometheus ServiceMonitor** (`servicemonitor.yaml`)
- **Resource Quotas** (`resourcequota.yaml`)
- **External Secrets Operator** integration (`external-secret.yaml`)
- **Cert-Manager** certificate provisioning (`cert-manager.yaml`)
- **Database backup CronJob** (`db-backup-job.yaml`)
- **Data retention CronJob** (`retention-job.yaml`)
- **Synthetic monitoring** (`synthetic-monitor.yaml`)

See the [Self-Hosting Guide](/self-hosting) for production hardening recommendations.
