#!/bin/bash

# Base URL
URL="http://localhost:8090"

echo "Checking if service is up..."
if curl -s "$URL/health" > /dev/null; then
    echo "Service is UP."
else
    echo "Service is DOWN. Please run 'docker-compose up -d --build' first."
    exit 1
fi

echo "Sending valid RequestFacts..."
BASE_URL="http://localhost:8090/api/v1"

echo "Sending sample events to $BASE_URL..."

# Generate UUIDv7 (time-ordered) — required by schema validation.
# Uses scripts/uuid7.py for proper v7 generation; falls back to uuidgen (v4)
# which will be rejected by ingestion if strict validation is enabled.

generate_uuid_v7() {
  python3 scripts/uuid7.py 2>/dev/null || uuidgen
}

# 1. Valid RequestFact
UUID=$(generate_uuid_v7)
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "Sending RequestFact ($UUID) at $NOW..."
curl -v -X POST "$BASE_URL/facts" \
  -H "Content-Type: application/json" \
  -d '{
    "event_id": "'"$UUID"'",
    "event_time": "'"$NOW"'",
    "service": "auth-service",
    "method": "POST",
    "path_template": "/login",
    "status_code": 200,
    "latency_ms": 45
  }'
echo ""

# 2. Valid ServiceEvent
UUID=$(generate_uuid_v7)
# Using same NOW is fine
echo "Sending ServiceEvent ($UUID) at $NOW..."
curl -X POST "$BASE_URL/events" \
  -H "Content-Type: application/json" \
  -d '{
    "event_id": "'"$UUID"'",
    "event_time": "'"$NOW"'",
    "service": "payment-service",
    "event_type": "payment_processed",
    "properties": {
      "currency": "USD",
      "amount": "99.99"
    }
  }'
echo ""

# 3. Invalid Fact (Missing fields)
echo "Sending Invalid Fact..."
curl -X POST "$BASE_URL/facts" \
  -H "Content-Type: application/json" \
  -d '{
    "service": "broken"
  }'
echo ""

echo "Done."
