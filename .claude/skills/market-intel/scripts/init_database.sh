#!/bin/bash
# Initialize the market-intel directory structure
set -e

BASE_DIR="${1:-.}/market-intel"

mkdir -p "$BASE_DIR/db/history"
mkdir -p "$BASE_DIR/reports/weekly"
mkdir -p "$BASE_DIR/reports/execution-prompts"
mkdir -p "$BASE_DIR/meta"

# Initialize opportunities database if it doesn't exist
if [ ! -f "$BASE_DIR/db/opportunities.json" ]; then
  cat > "$BASE_DIR/db/opportunities.json" << 'EOF'
{
  "version": "1.0",
  "last_updated": null,
  "opportunities": []
}
EOF
  echo "Created opportunities.json"
fi

# Initialize learning file if it doesn't exist
if [ ! -f "$BASE_DIR/meta/learning.json" ]; then
  cat > "$BASE_DIR/meta/learning.json" << 'EOF'
{
  "version": "1.0",
  "last_updated": null,
  "industry_patterns": [],
  "saturation_warnings": [],
  "recurring_problem_types": [],
  "ai_capability_unlocks": [],
  "scoring_adjustments": []
}
EOF
  echo "Created learning.json"
fi

# Initialize sources tracker if it doesn't exist
if [ ! -f "$BASE_DIR/meta/sources.json" ]; then
  cat > "$BASE_DIR/meta/sources.json" << 'EOF'
{
  "version": "1.0",
  "last_updated": null,
  "sources": []
}
EOF
  echo "Created sources.json"
fi

echo "Market-intel directory initialized at $BASE_DIR"
