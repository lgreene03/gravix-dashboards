#!/bin/bash

# A simple wrapper to run SQL against the Trino container (if running)

TRINO_CMD="docker exec -i gravix-trino trino --catalog gravix --schema raw"

echo "Initializing Tables..."
# You'd need to manually run the DDL once, or embed in startup
# A full solution would use a migrations container.
# For MVP, we'll try to execute the file directly.

cat storage/trino/init.sql | $TRINO_CMD 2>/dev/null
echo "Tables initialized (if they didn't exist)."

echo ""
echo "=== Sample Query: Error Rates per Service (Last Day) ==="
echo "
SELECT 
    bucket_start, 
    service, 
    SUM(request_count) as total_reqs, 
    SUM(error_count) as total_errs, 
    CAST(SUM(error_count) AS DOUBLE) / CAST(SUM(request_count) AS DOUBLE) as agg_error_rate
FROM request_metrics_minute 
GROUP BY 1, 2 
ORDER BY 1 DESC 
LIMIT 10;
" | $TRINO_CMD

echo ""
echo "=== Sample Query: 95th Percentile Latency Checks ==="
echo "
SELECT 
    bucket_start, 
    path_template, 
    p95_latency_ms 
FROM request_metrics_minute 
WHERE p95_latency_ms > 100 
ORDER BY p95_latency_ms DESC 
LIMIT 5;
" | $TRINO_CMD
