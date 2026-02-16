# Gravix Service Health Dashboard

Gravix is a **real-time service health monitoring system** designed for high-scale, low-latency observability. It ingests raw request events, aggregates them into metrics (Latency, Error Rate), and visualizes them on a live dashboard.

![Dashboard Screenshot](docs/images/dashboard.png)

## 🚀 Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.24+ (optional, for local dev)

### Running the System

```bash
# 1. Start all services (Ingestion, Trino, Cube, Load Generator)
docker-compose up -d --build

# 2. View the Dashboard
open http://localhost:8000/index.html

# 3. View Trino UI (Query Engine)
open http://localhost:8081

# 4. View Cube Playground (Semantic Layer)
open http://localhost:4000
```

## 🏗 Architecture

```mermaid
graph LR
    A[Load Generator] -- "POST /api/v1/facts" --> B[Ingestion Service]
    B -- "Write JSONL" --> C[Local Disk / S3]
    C -- "ETL Cron" --> D[Rollup Job (Go)]
    D -- "Write Parquet" --> E[Data Warehouse]
    F[Trino] -- "Query" --> E
    G[Cube.js] -- "Aggregates" --> F
    H[Dashboard] -- "API" --> G
```

## 📚 Documentation

- [**System Truth**](docs/00-system-truth.md): Core architectural decisions and invariants.
- [**API Reference**](docs/07-api-reference.md): How to send data to Gravix.
- [**Operations Runbook**](docs/06-operations.md): Maintenance, troubleshooting, and recovery.
- [**MVP Scope**](docs/05-mvp-scope.md): Original project requirements and goals.

## 🛠 Project Structure

- `cmd/load_generator`: Synthetic traffic generator.
- `services/ingestion`: Go-based ingestion service (HTTP -> JSONL).
- `transforms/request_metrics_minute`: Go ETL job (JSONL -> Parquet).
- `cube/`: Semantic layer configuration.
- `dashboards/`: Static HTML/JS frontend.
- `storage/trino`: Trino configuration and schema.
