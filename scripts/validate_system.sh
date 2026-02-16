#!/bin/bash
set -e

echo "=== Gravix System Validation ==="

# 1. Check Services
echo "[1/7] Checking Docker Services..."
if ! docker-compose ps | grep "Up"; then
    echo "Services not running. Starting..."
    docker-compose up -d ingestion trino cube
    sleep 5
fi

# 2. Health Checks
echo "[2/7] Checking Health Endpoints..."
curl -f -s http://localhost:8090/health > /dev/null && echo "  - Ingestion: OK" || echo "  - Ingestion: FAIL"
# Trino UI is 8081, API is complex, assumes container running is OK for now
# Cube API 4000
curl -f -s http://localhost:4000/readyz > /dev/null && echo "  - Cube: OK" || echo "  - Cube: FAIL (might be starting)"

# 3. Clean & Send Data
echo "[3/7] Cleaning & Sending Sample Data..."
rm -rf data/buffer/* data/raw/* data/warehouse/*

# Restart ingestion to release file handles of deleted files and create new ones
echo "  - Restarting Ingestion to reset file handles..."
docker-compose restart ingestion
echo "  - Waiting for Ingestion to be healthy..."
for i in {1..10}; do
  if curl -s "http://localhost:8090/health" > /dev/null; then
    break
  fi
  sleep 2
done

if ! curl -s "http://localhost:8090/health" > /dev/null; then
  echo "Service is DOWN after 20s. Please check logs."
  exit 1
fi

./scripts/send_sample_events.sh
echo "  - Sent events."

# 4. Check Raw Buffer/Upload
echo "[4/7] Verifying Raw Data..."
sleep 2 # Allow fsync/write
if ls data/buffer/request_facts/*.jsonl 1> /dev/null 2>&1; then
    echo "  - Buffer files exist: OK"
else
    echo "  - Buffer files MISSING in data/buffer/request_facts"
fi

# Force rotation (simulation)
# The service only rotates on 60s ticker. Restarting doesn't rotate current.jsonl (it appends).
# So for validation speed, we manually move the buffer file to raw.
echo "  - Simulating Rotation (Manual Move)..."

# Ensure we have data
if [ ! -f data/buffer/request_facts/current.jsonl ]; then
    echo "  - No buffer file to rotate!"
    exit 1
fi

DATE_DIR=$(date -u +"%Y-%m-%d")
HOUR_DIR=$(date -u +"%H")
mkdir -p data/raw/request_facts/$DATE_DIR/$HOUR_DIR
mkdir -p data/raw/service_events/$DATE_DIR/$HOUR_DIR

# Copy instead of move so we don't break the running service's open file handle (though likely fine if we just read it)
# Actually, if we move it, the service might get confused if it holds the fd? 
# In Unix, moving a file while open is fine, but the service will keep writing to the old inode (now in raw).
# So better to COPY for validation test.
cp data/buffer/request_facts/current.jsonl data/raw/request_facts/$DATE_DIR/$HOUR_DIR/batch_test.jsonl

if [ -f data/buffer/service_events/current.jsonl ]; then
    cp data/buffer/service_events/current.jsonl data/raw/service_events/$DATE_DIR/$HOUR_DIR/batch_test.jsonl
fi

echo "  - Copied buffer to raw: OK"

# Check raw/
if find data/raw/request_facts -name "*.jsonl" | grep -q ".jsonl"; then
    echo "  - Raw files found: OK"
else
    echo "  - Raw files MISSING in data/raw"
    exit 1
fi

# 5. Run Rollup
echo "[5/7] Running Rollup Job..."
# Use current time to cover the data we just sent
go run transforms/request_metrics_minute/main.go --process-time $(date -u +"%Y-%m-%dT%H:%M:%SZ") > /dev/null
echo "  - Rollup Job completed."

# Verify Warehouse
if find data/warehouse/request_metrics_minute -name "*.parquet" | grep -q ".parquet"; then
    echo "  - Parquet files found in Warehouse: OK"
else
    echo "  - Parquet files MISSING in Warehouse"
    exit 1
fi

# 6. Trino Query
echo "[6/7] Querying Trino..."
# Ensure table stats updated (Hive usually needs this partition discovery)
# Using `run-queries.sh`
./storage/trino/run-queries.sh > /dev/null 2>&1 && echo "  - Trino Query Success: OK" || echo "  - Trino Query FAILED"

# 7. Cube Query
echo "[7/7] Querying Cube API..."
CUBE_QUERY='{"query":{"measures":["RequestMetricsMinute.count"],"timeDimensions":[{"dimension":"RequestMetricsMinute.bucketStart","granularity":"day"}]}}'

echo "  - Waiting for Cube result..."
CUBE_SUCCESS=false
for i in {1..15}; do
  if curl -s -X POST "http://localhost:4000/cubejs-api/v1/load" \
    -H "Content-Type: application/json" \
    -d "$CUBE_QUERY" | grep -q "data"; then
    CUBE_SUCCESS=true
    break
  fi
  sleep 2
done

if [ "$CUBE_SUCCESS" = true ]; then
  echo "  - Cube Query Success: OK"
else
  echo "  - Cube Query FAILED"
fi

echo "=== Validation Complete ==="
