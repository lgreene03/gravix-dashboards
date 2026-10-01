#!/usr/bin/env bash
# Proves a completely independent engine can read Gravix's Iceberg tables.
#
# Spark, not Trino. Trino wrote those tables, so Trino reading them back proves
# only that Trino is self-consistent — which is not the claim. The claim is
# interoperability, and the only thing that tests it is a different
# implementation of the same table format, given nothing but the table's path
# and S3 credentials. No metastore, no catalog, no Gravix binary, no
# Gravix-specific driver.
#
# The container is ephemeral and removed on success or failure. Per GRVX-1106
# §3, Spark is deliberately not a permanent docker-compose service: this is a
# verification, not a component.
#
# Exit codes:
#   0  Spark read the table and printed its row count
#   1  the read failed, or printed no row count
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
HADOOP_AWS="org.apache.hadoop:hadoop-aws:3.3.4"
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

TABLE="${WAREHOUSE}/raw/request_metrics_minute"

# Spark is given no catalog at all, only the table's path. Trino keeps its
# tables in its own file metastore (DD-033), which no other engine reads, but
# an Iceberg table describes itself: its metadata/ directory holds every
# metadata.json Trino has written, numbered in order, and Iceberg opens a
# table read-only from any one of them. This picks the newest and counts it.
# The number is compared as a number: it is zero-padded to five digits only
# until it outgrows them, which the five-minute sync reaches in months.
SPARK_SCRIPT="$(mktemp)"
SPARK_ERR="$(mktemp)"
trap 'rm -f "$SPARK_SCRIPT" "$SPARK_ERR"' EXIT
cat > "$SPARK_SCRIPT" <<'SCALA'
import org.apache.hadoop.fs.Path
// spark-shell prints an uncaught error to stdout and carries on, so every
// outcome is reported on a GRAVIX_ line and ends the run with its own status.
try {
  val metadataDir = new Path(sys.env("GRAVIX_TABLE") + "/metadata")
  val fs = metadataDir.getFileSystem(spark.sparkContext.hadoopConfiguration)
  def version(p: Path): Long = scala.util.Try(p.getName.takeWhile(_ != '-').toLong).getOrElse(-1L)
  val newest = fs.listStatus(metadataDir).map(_.getPath).filter(_.getName.endsWith(".metadata.json")).sortBy(version).lastOption.map(_.toString)
  newest match {
    case None =>
      println("GRAVIX_ERROR=no metadata.json under " + metadataDir)
      System.exit(1)
    case Some(metadata) =>
      println("GRAVIX_METADATA=" + metadata)
      println("GRAVIX_ROWS=" + spark.read.format("iceberg").load(metadata).count())
      System.exit(0)
  }
} catch {
  case e: Throwable =>
    println("GRAVIX_ERROR=" + e.toString.replace('\n', ' '))
    System.exit(1)
}
SCALA
chmod 644 "$SPARK_SCRIPT"

# The image ships Hadoop's client but not its S3 connector, so s3a:// needs
# hadoop-aws at the image's Hadoop version, which brings the AWS SDK with it.
# --packages resolves through Ivy, whose default cache is under a home
# directory the image's user does not have, so it is pointed at /tmp.
OUT="$(docker run --rm --network "$NETWORK" \
  -e "GRAVIX_TABLE=${TABLE}" \
  -v "${SPARK_SCRIPT}:/tmp/read_table.scala:ro" \
  "$SPARK_IMAGE" \
  /opt/spark/bin/spark-shell \
    --packages "$ICEBERG_RUNTIME,$HADOOP_AWS" \
    --conf spark.jars.ivy=/tmp/.ivy2 \
    --conf spark.ui.enabled=false \
    --conf spark.hadoop.fs.s3a.endpoint=http://minio:9000 \
    --conf "spark.hadoop.fs.s3a.access.key=${S3_ACCESS_KEY}" \
    --conf "spark.hadoop.fs.s3a.secret.key=${S3_SECRET_KEY}" \
    --conf spark.hadoop.fs.s3a.path.style.access=true \
    -i /tmp/read_table.scala </dev/null 2>"$SPARK_ERR")" \
  && STATUS=0 || STATUS=$?

# The answer is a GRAVIX_ROWS= line holding a bare integer, and the run must
# exit 0. This check once kept every digit of spark-sql's last line, whatever
# it said, after discarding the exit status, so it passed on a stack whose
# Iceberg catalog did not exist (F-067).
COUNT="$(printf '%s\n' "$OUT" | sed -n 's/^GRAVIX_ROWS=//p' | tail -1 | tr -d '[:space:]')"
METADATA="$(printf '%s\n' "$OUT" | sed -n 's/^GRAVIX_METADATA=//p' | tail -1)"
if [[ "$STATUS" -ne 0 || ! "$COUNT" =~ ^[0-9]+$ ]]; then
  echo "Spark could not read ${TABLE}" >&2
  echo "  exit status: $STATUS" >&2
  printf '%s\n' "$OUT" | grep '^GRAVIX_' | sed 's/^/  /' >&2 || true
  echo "  last lines Spark wrote to stderr:" >&2
  grep -v ' INFO ' "$SPARK_ERR" | tail -25 | sed 's/^/    /' >&2
  exit 1
fi

echo "spark read ${METADATA}: ${COUNT} row(s)"
echo "An engine that has never heard of Gravix read Gravix's warehouse."
