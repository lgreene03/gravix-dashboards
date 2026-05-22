#!/usr/bin/env bash
# send.sh — builds the ServiceEvent JSON payload and POST it to Gravix.
#
# Called by action.yml. All inputs arrive via environment variables prefixed
# with _GRAVIX_ to avoid collisions with user env or GitHub's own variables.
#
# Uses Node.js (always present on GitHub-hosted runners) to build the JSON
# safely — no jq dependency, no string-escaping foot-guns.

set -euo pipefail

# Build the payload with Node so special characters in values are escaped
# correctly by JSON.stringify.
PAYLOAD=$(node - <<'EOF'
const {
  _GRAVIX_SERVICE:    service,
  _GRAVIX_EVENT_TYPE: eventType,
  _GRAVIX_MESSAGE:    message,
  _GRAVIX_VERSION:    version,
  GITHUB_SHA:         commitSha,
  GITHUB_REF_NAME:    branch,
  GITHUB_ACTOR:       author,
  GITHUB_REPOSITORY:  repository,
  GITHUB_RUN_ID:      runId,
  GITHUB_RUN_NUMBER:  runNumber,
  GITHUB_WORKFLOW:    workflow,
  GITHUB_SERVER_URL:  serverUrl,
} = process.env;

const properties = { commit_sha: commitSha, branch, author, repository };
if (runId)     properties.run_id     = runId;
if (runNumber) properties.run_number = runNumber;
if (workflow)  properties.workflow   = workflow;
if (version)   properties.version    = version;

// Deep-link to the run so operators can jump straight to the logs.
if (serverUrl && repository && runId) {
  properties.run_url = `${serverUrl}/${repository}/actions/runs/${runId}`;
}

const payload = {
  service,
  event_type: eventType,
  entity_id:  commitSha,
  properties,
};
if (message) payload.message = message;

process.stdout.write(JSON.stringify(payload));
EOF
)

GRAVIX_INGEST="${_GRAVIX_URL%/}/api/v1/events"

echo "gravix: sending ${_GRAVIX_EVENT_TYPE} event"
echo "gravix: service=${_GRAVIX_SERVICE} commit=${GITHUB_SHA:0:7} branch=${GITHUB_REF_NAME:-unknown}"

HTTP_STATUS=$(curl -s -o /tmp/gravix_response.json -w "%{http_code}" \
  -X POST "$GRAVIX_INGEST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${_GRAVIX_API_KEY}" \
  -d "$PAYLOAD" \
  --max-time 10)

if [ "$HTTP_STATUS" = "200" ] || [ "$HTTP_STATUS" = "201" ]; then
  echo "gravix: event recorded (HTTP ${HTTP_STATUS})"
else
  # Observability failures must never break the pipeline.
  echo "gravix: warning — ingestion returned HTTP ${HTTP_STATUS}"
  if [ -f /tmp/gravix_response.json ]; then
    echo "gravix: response body: $(cat /tmp/gravix_response.json)"
  fi
fi
