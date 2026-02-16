# Engineering Plan: Zero-Cost Cloud Prep

**Author:** Senior Engineering Lead
**Theme:** "Decouple and Observe"
**Goal:** Prepare Gravix for high-scale production while remaining 100% local/free during development.

---

## 1. Storage Abstraction Layer (Cloud-Neutral)

Currently, our code writes directly to `/Users/lgreene/gravix-dashboards/data`. This is a hard dependency on the local filesystem.

### Plan

- **Interface**: Define an `ObjectStore` interface in the Ingestion service and Rollup job.

  ```go
  type ObjectStore interface {
      Put(ctx context.Context, key string, reader io.Reader) error
      Get(ctx context.Context, key string) (io.ReadCloser, error)
      Delete(ctx context.Context, key string) error
      List(ctx context.Context, prefix string) ([]string, error)
  }
  ```

- **Local Implementation**: A wrapper around `os` and `filepath`.
- **S3 Implementation (Future)**: A wrapper around the AWS SDK.
- **Verification (Zero Cost)**: Use **MinIO** in `docker-compose.yml`. MinIO provides an S3-compatible API locally. We can test "S3" uploads without an AWS account.

---

## 2. Infrastructure-as-Code (Local K8s)

`docker-compose` is great for dev, but it doesn't translate to production scaling.

### Plan

- **Helm Charts**: Create Helm charts for all services (Ingestion, Trino, Cube).
- **Local Verification**: Deploy to **Minikube** or **Kind** (Kubernetes in Docker).
- **Value**: Validates our networking, ConfigMaps, and Secret management without hiring a DevOps engineer yet.

---

## 3. Observability Foundation (Self-Hosted)

Monitoring a monitoring system is crucial. We need to export internal metrics to ensure the Ingestion service isn't dropping data.

### Plan

- **Metrics**: Integrate `prometheus/client_golang` into the Ingestion service.
  - Track: `ingestion_requests_total`, `ingestion_latency_seconds`, `batch_rotation_errors_total`.
- **Infrastructure**: Add **Prometheus** and **Grafana** to `docker-compose.yml`.
- **Dashboard**: Create a "Gravix Internals" Grafana dashboard.

---

## 4. Strict Contract Management (Protobuf)

Loose JSON is fine for MVP but breaks in production during rolling deployments.

### Plan

- **Protobuf**: Define our `RequestFact` and `ServiceEvent` in `.proto` files.
- **Benefits**:
  - Strict typing.
  - Forward/Backward compatibility.
  - Performance: Binary format is faster than JSON (Horizon 3 optimization).
- **Ingestion**: The service should accept both JSON (for legacy/curl) and Protobuf.

---

## 5. Configuration Management (Viper/Env)

Move away from hardcoded defaults.

### Plan

- Use **Viper** (Go) to handle configurations.
- Priority Order: `Command Line Flag > Environment Variable > Config File > Default`.
- This sets us up for Kubernetes **ConfigMaps** perfectly.

---

## 🚀 Priority Checklist (Phase 2)

- [ ] **Infrastructure**: Add MinIO, Prometheus, and Grafana to Docker Compose.
- [ ] **Ingestion**: Implement `ObjectStore` abstraction and MinIO driver.
- [ ] **Ingestion**: Add `/metrics` endpoint with basic health stats.
- [ ] **Schemas**: Transition logic to support Protobuf-backed validation.
- [ ] **Deployment**: Draft initial Helm charts for local K8s testing.
