#!/usr/bin/env bash
set -euo pipefail

#
# demo.sh — Boot Gravix locally and simulate an example customer.
#
# Simulates "Acme Retail", an e-commerce company running 4 microservices.
# Sends realistic traffic through the full pipeline:
#
#   Load → Ingestion → MinIO → Rollup → Trino → Cube → Dashboard
#
# Usage:
#   ./scripts/demo.sh              # Full demo: boot + traffic + verify
#   ./scripts/demo.sh --traffic    # Traffic only (services already running)
#   ./scripts/demo.sh --teardown   # Stop everything and clean up
#
# After running, visit:
#   Dashboard:    http://localhost:8000/index.html
#   Cube:         http://localhost:4000
#   Prometheus:   http://localhost:9090
#   Grafana:      http://localhost:3000  (admin/admin)
#   MinIO:        http://localhost:9001  (admin/local-dev-secret-key-change-me)
#

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${PROJECT_DIR}"

# ── Configuration ──
INGESTION_URL="http://localhost:8090/api/v1/facts"
EVENTS_URL="http://localhost:8090/api/v1/events"
API_KEY="${API_KEY:-local-dev-api-key-gravix}"
TRAFFIC_DURATION=120  # seconds of traffic to generate
BATCH_SIZE=10         # facts per batch

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}▶${NC} $1"; }
ok()    { echo -e "${GREEN}✓${NC} $1"; }
warn()  { echo -e "${YELLOW}⚠${NC} $1"; }
fail()  { echo -e "${RED}✗${NC} $1"; }

# ── Acme Retail: Service Configuration ──
#
# Simulates a typical e-commerce company with 4 microservices:
#   - storefront: Public-facing product browsing and search
#   - checkout:   Cart and payment processing
#   - inventory:  Stock management and warehouse sync
#   - accounts:   User authentication and profile management
#

generate_uuid7() {
  if command -v python3 &>/dev/null && [ -f "${SCRIPT_DIR}/uuid7.py" ]; then
    python3 "${SCRIPT_DIR}/uuid7.py" 2>/dev/null && return
  fi
  # Fallback: construct a v7-like UUID from timestamp + random
  # This produces a valid UUIDv7 structure (timestamp in high bits, version=7)
  if command -v python3 &>/dev/null; then
    python3 -c "
import time, random, struct
ts_ms = int(time.time() * 1000)
rand_a = random.getrandbits(12)
rand_b = random.getrandbits(62)
hi = (ts_ms << 16) | (0x7 << 12) | rand_a
lo = (0b10 << 62) | rand_b
u = (hi << 64) | lo
h = format(u, '032x')
print(f'{h[:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:]}')
" 2>/dev/null && return
  fi
  # Last resort: uuidgen (v4) — will fail validation but shows the attempt
  uuidgen | tr '[:upper:]' '[:lower:]'
}

now_iso() {
  date -u +"%Y-%m-%dT%H:%M:%S.000Z"
}

# ── Send a single RequestFact ──
send_fact() {
  local service="$1"
  local method="$2"
  local path="$3"
  local status="$4"
  local latency="$5"
  local ua="$6"

  local event_id
  event_id=$(generate_uuid7)
  local event_time
  event_time=$(now_iso)

  curl -sf -o /dev/null -w "%{http_code}" \
    -X POST "${INGESTION_URL}" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "{
      \"event_id\": \"${event_id}\",
      \"event_time\": \"${event_time}\",
      \"service\": \"${service}\",
      \"method\": \"${method}\",
      \"path_template\": \"${path}\",
      \"status_code\": ${status},
      \"latency_ms\": ${latency},
      \"user_agent_family\": \"${ua}\"
    }" 2>/dev/null || echo "000"
}

# ── Send a ServiceEvent ──
send_event() {
  local service="$1"
  local event_type="$2"
  local props="$3"

  local event_id
  event_id=$(generate_uuid7)
  local event_time
  event_time=$(now_iso)

  curl -sf -o /dev/null -w "%{http_code}" \
    -X POST "${EVENTS_URL}" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "{
      \"event_id\": \"${event_id}\",
      \"event_time\": \"${event_time}\",
      \"service\": \"${service}\",
      \"event_type\": \"${event_type}\",
      \"properties\": ${props}
    }" 2>/dev/null || echo "000"
}

# ── Weighted random selection ──
pick_weighted() {
  # Input: "value:weight value:weight ..."
  local total=0
  local -a items=()
  local -a weights=()
  for entry in $1; do
    local val="${entry%%:*}"
    local w="${entry##*:}"
    items+=("$val")
    weights+=("$w")
    total=$((total + w))
  done
  local r=$((RANDOM % total))
  local cumulative=0
  for i in "${!items[@]}"; do
    cumulative=$((cumulative + weights[i]))
    if [ "$r" -lt "$cumulative" ]; then
      echo "${items[$i]}"
      return
    fi
  done
  echo "${items[0]}"
}

# ── Generate realistic latency (ms) ──
random_latency() {
  local base="$1"  # base latency for this endpoint
  # 80% normal, 15% elevated, 5% spike
  local r=$((RANDOM % 100))
  if [ "$r" -lt 80 ]; then
    echo $((base + RANDOM % (base / 2 + 1)))
  elif [ "$r" -lt 95 ]; then
    echo $((base * 2 + RANDOM % base))
  else
    echo $((base * 5 + RANDOM % (base * 3)))
  fi
}

# ── Generate realistic status code ──
random_status() {
  local error_rate="$1"  # percentage of errors (0-100)
  local r=$((RANDOM % 100))
  if [ "$r" -lt "$((100 - error_rate))" ]; then
    echo 200
  elif [ "$r" -lt "$((100 - error_rate / 3))" ]; then
    # Client errors
    pick_weighted "400:3 401:2 403:1 404:4 422:2"
  else
    # Server errors
    pick_weighted "500:5 502:2 503:2 504:1"
  fi
}

# ── Traffic Generator ──
generate_acme_traffic() {
  local duration="$1"
  local end_time=$((SECONDS + duration))
  local total_sent=0
  local total_errors=0
  local batch=0

  echo ""
  info "Generating Acme Retail traffic for ${duration}s..."
  echo ""
  echo "  Services:"
  echo "    storefront  — Product catalog, search, browsing"
  echo "    checkout     — Cart operations, payment processing"
  echo "    inventory    — Stock levels, warehouse sync"
  echo "    accounts     — Login, registration, profiles"
  echo ""

  while [ "$SECONDS" -lt "$end_time" ]; do
    batch=$((batch + 1))

    for _ in $(seq 1 "${BATCH_SIZE}"); do
      # Pick a service with weighted distribution (storefront gets most traffic)
      local service
      service=$(pick_weighted "storefront:50 checkout:20 inventory:15 accounts:15")

      local method path base_latency error_rate ua

      case "${service}" in
        storefront)
          local endpoint
          endpoint=$(pick_weighted "browse:40 search:25 detail:30 reviews:5")
          case "${endpoint}" in
            browse)  method="GET";  path="/api/v1/products";          base_latency=30;  error_rate=2 ;;
            search)  method="GET";  path="/api/v1/products/search";   base_latency=80;  error_rate=3 ;;
            detail)  method="GET";  path="/api/v1/products/{id}";     base_latency=25;  error_rate=1 ;;
            reviews) method="GET";  path="/api/v1/products/{id}/reviews"; base_latency=50; error_rate=2 ;;
          esac
          ua=$(pick_weighted "Chrome:45 Safari:25 Firefox:15 Edge:10 Mobile:5")
          ;;
        checkout)
          local endpoint
          endpoint=$(pick_weighted "view_cart:30 add_item:25 remove_item:10 place_order:20 payment:15")
          case "${endpoint}" in
            view_cart)    method="GET";    path="/api/v1/cart";             base_latency=20;  error_rate=1 ;;
            add_item)     method="POST";   path="/api/v1/cart/items";       base_latency=35;  error_rate=3 ;;
            remove_item)  method="DELETE"; path="/api/v1/cart/items/{id}";  base_latency=25;  error_rate=2 ;;
            place_order)  method="POST";   path="/api/v1/orders";           base_latency=150; error_rate=5 ;;
            payment)      method="POST";   path="/api/v1/payments";         base_latency=200; error_rate=4 ;;
          esac
          ua=$(pick_weighted "Chrome:50 Safari:30 Mobile:15 Edge:5")
          ;;
        inventory)
          local endpoint
          endpoint=$(pick_weighted "check_stock:50 update_stock:30 warehouse_sync:20")
          case "${endpoint}" in
            check_stock)    method="GET";  path="/api/v1/inventory/{id}";   base_latency=15;  error_rate=1 ;;
            update_stock)   method="PUT";  path="/api/v1/inventory/{id}";   base_latency=40;  error_rate=3 ;;
            warehouse_sync) method="POST"; path="/api/v1/inventory/sync";   base_latency=300; error_rate=8 ;;
          esac
          ua="InternalService"
          ;;
        accounts)
          local endpoint
          endpoint=$(pick_weighted "login:35 register:10 profile:30 password:5 sessions:20")
          case "${endpoint}" in
            login)    method="POST"; path="/api/v1/auth/login";        base_latency=60;  error_rate=8 ;;
            register) method="POST"; path="/api/v1/auth/register";     base_latency=100; error_rate=5 ;;
            profile)  method="GET";  path="/api/v1/users/{id}";        base_latency=20;  error_rate=2 ;;
            password) method="PUT";  path="/api/v1/users/{id}/password"; base_latency=80; error_rate=3 ;;
            sessions) method="GET";  path="/api/v1/auth/sessions";     base_latency=15;  error_rate=1 ;;
          esac
          ua=$(pick_weighted "Chrome:40 Safari:25 Firefox:15 Mobile:15 Postman:5")
          ;;
      esac

      local status latency
      status=$(random_status "${error_rate}")
      latency=$(random_latency "${base_latency}")

      local result
      result=$(send_fact "${service}" "${method}" "${path}" "${status}" "${latency}" "${ua}")
      total_sent=$((total_sent + 1))
      if [ "${result}" != "201" ] && [ "${result}" != "200" ]; then
        total_errors=$((total_errors + 1))
      fi
    done

    # Print progress every 5 batches
    if [ $((batch % 5)) -eq 0 ]; then
      local remaining=$((end_time - SECONDS))
      [ "$remaining" -lt 0 ] && remaining=0
      printf "  Batch %3d | Sent: %5d | Errors: %3d | Remaining: %3ds\r" \
        "${batch}" "${total_sent}" "${total_errors}" "${remaining}"
    fi

    # ~10 requests/second pace
    sleep 1
  done

  echo ""
  echo ""
  ok "Traffic generation complete: ${total_sent} facts sent (${total_errors} ingestion errors)"
}

# ── Send service events (deploys, restarts, etc.) ──
send_acme_events() {
  info "Sending Acme Retail service events..."

  local events_sent=0

  # Simulate a deploy cycle
  for svc in storefront checkout inventory accounts; do
    local version="2.$((RANDOM % 5)).$((RANDOM % 20))"

    send_event "${svc}" "deploy_started" \
      "{\"version\": \"${version}\", \"environment\": \"production\"}" >/dev/null
    events_sent=$((events_sent + 1))

    sleep 0.2

    send_event "${svc}" "deploy_completed" \
      "{\"version\": \"${version}\", \"environment\": \"production\", \"duration_seconds\": \"$((30 + RANDOM % 90))\"}" >/dev/null
    events_sent=$((events_sent + 1))
  done

  # A health check failure on inventory (realistic incident)
  send_event "inventory" "health_check_failed" \
    "{\"reason\": \"connection_timeout\", \"endpoint\": \"warehouse_db\"}" >/dev/null
  events_sent=$((events_sent + 1))

  # A scale-up on storefront (traffic spike)
  send_event "storefront" "scale_up" \
    "{\"from_replicas\": \"3\", \"to_replicas\": \"6\", \"reason\": \"cpu_threshold\"}" >/dev/null
  events_sent=$((events_sent + 1))

  ok "${events_sent} service events sent"
}

# ── Wait for a service to be healthy ──
wait_for_service() {
  local name="$1"
  local url="$2"
  local max_wait="${3:-60}"
  local elapsed=0

  printf "  Waiting for %-15s " "${name}..."
  while [ "$elapsed" -lt "$max_wait" ]; do
    if curl -sf -o /dev/null "${url}" 2>/dev/null; then
      echo -e "${GREEN}ready${NC} (${elapsed}s)"
      return 0
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  echo -e "${RED}timeout${NC} (${max_wait}s)"
  return 1
}

# ── Boot ──
boot_services() {
  echo ""
  echo "==========================================="
  echo "  Gravix Local Demo — Acme Retail"
  echo "==========================================="
  echo ""

  # Check for .env
  if [ ! -f .env ]; then
    warn "No .env file found. Creating from .env.example..."
    cp .env.example .env
    sed -i.bak 's/change-me-to-a-real-secret/local-dev-secret-key-change-me/g' .env
    rm -f .env.bak
    ok ".env created"
  fi

  # Check Docker
  if ! command -v docker &>/dev/null; then
    fail "Docker not found. Install from https://docs.docker.com/get-docker/"
    exit 1
  fi
  if ! docker info &>/dev/null 2>&1; then
    fail "Docker daemon not running. Start Docker Desktop."
    exit 1
  fi

  info "Building and starting services..."
  docker-compose up -d --build 2>&1 | tail -5
  echo ""

  info "Waiting for services to become healthy..."
  wait_for_service "ingestion" "http://localhost:8090/live" 60
  wait_for_service "minio" "http://localhost:9001" 30
  wait_for_service "trino" "http://localhost:8081/v1/info" 90
  wait_for_service "cube" "http://localhost:4000/readyz" 90
  wait_for_service "dashboard" "http://localhost:8000/healthz" 30
  wait_for_service "prometheus" "http://localhost:9090/-/healthy" 30

  echo ""
  ok "All services healthy"
}

# ── Verify pipeline ──
verify_pipeline() {
  echo ""
  info "Verifying data pipeline..."

  # Check raw data landed
  local raw_count
  raw_count=$(find data/raw -name "*.jsonl" 2>/dev/null | wc -l | tr -d ' ')
  if [ "${raw_count}" -gt 0 ]; then
    ok "Raw data: ${raw_count} JSONL file(s) in data/raw/"
  else
    # Check buffer (may not have rotated yet)
    local buffer_count
    buffer_count=$(find data/buffer -name "*.jsonl" 2>/dev/null | wc -l | tr -d ' ')
    if [ "${buffer_count}" -gt 0 ]; then
      ok "Buffered data: ${buffer_count} JSONL file(s) (waiting for rotation to raw/)"
    else
      warn "No data files found yet — ingestion may still be buffering"
    fi
  fi

  # Check warehouse
  local parquet_count
  parquet_count=$(find data/warehouse -name "*.parquet" 2>/dev/null | wc -l | tr -d ' ')
  if [ "${parquet_count}" -gt 0 ]; then
    ok "Warehouse: ${parquet_count} Parquet file(s) in data/warehouse/"
  else
    warn "No Parquet files yet — rollup runs every 5 minutes, check back shortly"
  fi

  # Check ingestion metrics
  local metrics
  metrics=$(curl -sf http://localhost:8090/metrics 2>/dev/null | grep -c "ingestion_requests_total" || echo "0")
  if [ "${metrics}" -gt 0 ]; then
    ok "Prometheus metrics exposed on :8090/metrics"
  else
    warn "No ingestion metrics found"
  fi
}

# ── Teardown ──
teardown() {
  echo ""
  info "Stopping all Gravix services..."
  docker-compose down 2>&1 | tail -3
  ok "Services stopped"

  echo ""
  read -r -p "Remove data directories? (raw, warehouse, minio, buffer) [y/N] " confirm
  if [[ "${confirm}" =~ ^[Yy]$ ]]; then
    rm -rf data/raw data/warehouse data/minio data/buffer
    ok "Data directories cleaned"
  else
    info "Data directories preserved"
  fi
}

# ── Main ──
case "${1:-}" in
  --traffic)
    generate_acme_traffic "${TRAFFIC_DURATION}"
    send_acme_events
    verify_pipeline
    ;;
  --teardown)
    teardown
    ;;
  --help|-h)
    echo "Usage: $0 [--traffic | --teardown | --help]"
    echo ""
    echo "  (no args)    Full demo: boot services, generate traffic, verify pipeline"
    echo "  --traffic    Generate traffic only (services must be running)"
    echo "  --teardown   Stop services and optionally clean data"
    echo ""
    echo "After running, visit http://localhost:8000/index.html for the dashboard."
    exit 0
    ;;
  "")
    boot_services
    echo ""

    # Stop the built-in load generator so our demo traffic is the only data
    info "Pausing built-in load generator..."
    docker-compose stop load-generator 2>/dev/null || true
    ok "Load generator paused (only Acme Retail traffic will flow)"

    echo ""
    echo "==========================================="
    echo "  Acme Retail — Example Customer"
    echo "==========================================="
    echo ""
    echo "  Company:  Acme Retail (e-commerce)"
    echo "  Services: storefront, checkout, inventory, accounts"
    echo "  Traffic:  ~${BATCH_SIZE} req/s for ${TRAFFIC_DURATION}s"
    echo ""

    send_acme_events
    generate_acme_traffic "${TRAFFIC_DURATION}"

    echo ""
    info "Waiting 10s for data to flush..."
    sleep 10

    verify_pipeline

    echo ""
    echo "==========================================="
    echo "  Demo Complete"
    echo "==========================================="
    echo ""
    echo "  Dashboard:   http://localhost:8000/index.html"
    echo "  Cube:        http://localhost:4000"
    echo "  Prometheus:  http://localhost:9090"
    echo "  Grafana:     http://localhost:3000  (admin/admin)"
    echo "  MinIO:       http://localhost:9001"
    echo ""
    echo "  The rollup job runs every 5 minutes."
    echo "  Wait a few minutes, then refresh the dashboard to see aggregated metrics."
    echo ""
    echo "  To send more traffic:   ./scripts/demo.sh --traffic"
    echo "  To shut everything down: ./scripts/demo.sh --teardown"
    echo ""
    ;;
  *)
    echo "Unknown option: $1"
    echo "Run '$0 --help' for usage."
    exit 1
    ;;
esac
