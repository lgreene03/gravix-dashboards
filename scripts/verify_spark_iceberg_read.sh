#!/usr/bin/env bash
# Proves a completely independent engine can read Gravix's Iceberg tables.
#
# Spark, not Trino. Trino wrote those tables, so Trino reading them back proves
# only that Trino is self-consistent — which is not the claim. The claim is
# interoperability, and the only thing that tests it is a different
# implementation of the same table format, given nothing but the warehouse path
# and S3 credentials. No metastore, no Gravix binary, no Gravix-specific driver.
#
# The container is ephemeral and removed on success or failure. Per GRVX-1106
# §3, Spark is deliberately not a permanent docker-compose service: this is a
# verification, not a component.
#
# Exit codes:
#   0  the Spark query succeeded and printed a non-negative integer
#   1  the query failed, or its output was not a non-negative integer
#   2  Trino or MinIO is unreachable
#
# Usage: ./scripts/verify_spark_iceberg_read.sh
set -euo pipefail

S3_BUCKET="${S3_BUCKET:-gravix}"
S3_ACCESS_KEY="${S3_ACCESS_KEY:-}"
S3_SECRET_KEY="${S3_SECRET_KEY:-}"
TRINO_URL="${TRINO_URL:-http://localhost:8081/v1/info}"
MINIO_URL="${MINIO_URL:-http://localhost:9000/minio/health/live}"

SPARK_IMAGE="apache/spark:3.5.3"
ICEBERG_RUNTIME="org.apache.iceberg:iceberg-spark-runtime-3.5_2.12:1.6.1"
WAREHOUSE="s3a://${S3_BUCKET}/iceberg-warehouse"

if ! curl -sf "$TRINO_URL" >/dev/null 2>&1 || ! curl -sf "$MINIO_URL" >/dev/null 2>&1; then
  echo "Trino or MinIO not reachable — run docker-compose up -d trino minio first" >&2
  exit 2
fi

if [[ -z "$S3_ACCESS_KEY" || -z "$S3_SECRET_KEY" ]]; then
  echo "S3_ACCESS_KEY and S3_SECRET_KEY must be set (see .env.example)" >&2
  exit 2
fi

# The compose network, so s3a://minio:9000 resolves the same way Trino sees it.
NETWORK="$(docker network ls --filter name=backend --format '{{.Name}}' | head -1)"
NETWORK="${NETWORK:-bridge}"

OUT="$(docker run --rm --network "$NETWORK" "$SPARK_IMAGE" \
  /opt/spark/bin/spark-sql \
    --packages "$ICEBERG_RUNTIME" \
    --conf spark.sql.catalog.gravix_iceberg=org.apache.iceberg.spark.SparkCatalog \
    --conf spark.sql.catalog.gravix_iceberg.type=hadoop \
    --conf "spark.sql.catalog.gravix_iceberg.warehouse=${WAREHOUSE}" \
    --conf spark.hadoop.fs.s3a.endpoint=http://minio:9000 \
    --conf "spark.hadoop.fs.s3a.access.key=${S3_ACCESS_KEY}" \
    --conf "spark.hadoop.fs.s3a.secret.key=${S3_SECRET_KEY}" \
    --conf spark.hadoop.fs.s3a.path.style.access=true \
    -e "SELECT COUNT(*) FROM gravix_iceberg.raw.request_metrics_minute" 2>/dev/null | tail -1 || true)"

COUNT="$(echo "$OUT" | tr -dc '0-9')"
if [[ -z "$COUNT" ]]; then
  echo "spark-sql query failed or returned non-numeric output" >&2
  echo "  output: ${OUT:-<empty>}" >&2
  exit 1
fi

echo "spark read gravix_iceberg.raw.request_metrics_minute: ${COUNT} row(s)"
echo "An engine that has never heard of Gravix read Gravix's warehouse."
