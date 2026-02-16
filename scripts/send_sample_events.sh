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

# Helper to generate dummy UUIDv7-ish strings (using uuidgen for MVP, format matters more than version for simple curl)
# Real clients should use UUIDv7 lib. Here we just use standard UUIDv4 as placeholder since ingestion doesn't strictly check version 7 in MVP curl (wait, it DOES check version 7 in code!)
# So we need a way to generate UUIDv7 or mock it.
# Actually, the validation code: if uid.Version() != 7 { return error }
# So we MUST generate valid v7.
# Since standard `uuidgen` is v4, we might fail ingestion.
# Alternative: Modify schema validation to allow v4 for MVP testing OR finding a way to generate v7 in bash.
# For simplicity, let's use a python one-liner if available.

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
