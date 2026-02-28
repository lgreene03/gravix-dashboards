#!/usr/bin/env bash
set -euo pipefail

#
# Gravix Helm rollback helper.
#
# Rolls back to a previous Helm revision or a specific image tag.
#
# Usage:
#   ./scripts/rollback.sh                          # Roll back to previous revision
#   ./scripts/rollback.sh --revision 3              # Roll back to revision 3
#   ./scripts/rollback.sh --tag sha-abc1234         # Redeploy with a specific image tag
#   ./scripts/rollback.sh --namespace gravix-prod   # Target a specific namespace
#
# Prerequisites:
#   - kubectl configured with cluster access
#   - helm v3 installed
#

NAMESPACE="gravix-prod"
RELEASE="gravix"
REVISION=""
IMAGE_TAG=""
DRY_RUN=false

usage() {
  cat <<USAGE
Usage: $0 [OPTIONS]

Options:
  --namespace, -n NAME    Kubernetes namespace (default: gravix-prod)
  --release, -r NAME      Helm release name (default: gravix)
  --revision REV          Roll back to a specific Helm revision number
  --tag TAG               Redeploy with a specific image tag instead of rolling back
  --dry-run               Show what would happen without making changes
  --help, -h              Show this help message

Examples:
  $0                                    # Roll back to previous revision
  $0 --revision 3                       # Roll back to revision 3
  $0 --tag sha-abc1234                  # Redeploy with a specific image tag
  $0 --namespace gravix-staging         # Roll back staging environment
  $0 --dry-run                          # Preview rollback without executing

USAGE
  exit 0
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace|-n) NAMESPACE="$2"; shift 2 ;;
    --release|-r)   RELEASE="$2"; shift 2 ;;
    --revision)     REVISION="$2"; shift 2 ;;
    --tag)          IMAGE_TAG="$2"; shift 2 ;;
    --dry-run)      DRY_RUN=true; shift ;;
    --help|-h)      usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
done

echo "=== Gravix Rollback ==="
echo "Namespace: ${NAMESPACE}"
echo "Release:   ${RELEASE}"
echo ""

# Show current state
echo "=== Current release history ==="
helm history "${RELEASE}" --namespace "${NAMESPACE}" --max 5 2>/dev/null || {
  echo "ERROR: Release '${RELEASE}' not found in namespace '${NAMESPACE}'"
  exit 1
}
echo ""

# Show current pod status
echo "=== Current pod status ==="
kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}" --no-headers 2>/dev/null || true
echo ""

if [ -n "${IMAGE_TAG}" ]; then
  # ── Redeploy with a specific tag ──
  echo "Action: Redeploy with image tag '${IMAGE_TAG}'"

  if [ "${DRY_RUN}" = true ]; then
    echo "[DRY RUN] Would run: helm upgrade ${RELEASE} deploy/gravix -n ${NAMESPACE} --set *.image.tag=${IMAGE_TAG} --atomic"
    exit 0
  fi

  read -rp "Proceed with redeployment? (y/N) " confirm
  if [[ ! "${confirm}" =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 0
  fi

  # Determine values file based on namespace
  VALUES_ARGS=""
  if [[ "${NAMESPACE}" == *"prod"* ]]; then
    VALUES_ARGS="-f deploy/gravix/values-prod.yaml"
  fi

  helm upgrade "${RELEASE}" deploy/gravix \
    ${VALUES_ARGS} \
    --namespace "${NAMESPACE}" \
    --set "ingestion.image.tag=${IMAGE_TAG}" \
    --set "rollupJob.image.tag=${IMAGE_TAG}" \
    --set "retentionJob.image.tag=${IMAGE_TAG}" \
    --wait \
    --timeout 5m \
    --atomic

else
  # ── Helm revision rollback ──
  if [ -z "${REVISION}" ]; then
    # Default to previous revision
    CURRENT=$(helm history "${RELEASE}" --namespace "${NAMESPACE}" --max 1 -o json | python3 -c "import sys,json; print(json.load(sys.stdin)[0]['revision'])" 2>/dev/null || echo "")
    if [ -z "${CURRENT}" ] || [ "${CURRENT}" -le 1 ]; then
      echo "ERROR: No previous revision to roll back to."
      exit 1
    fi
    REVISION=$((CURRENT - 1))
    echo "Action: Roll back to previous revision ${REVISION} (current: ${CURRENT})"
  else
    echo "Action: Roll back to revision ${REVISION}"
  fi

  if [ "${DRY_RUN}" = true ]; then
    echo "[DRY RUN] Would run: helm rollback ${RELEASE} ${REVISION} -n ${NAMESPACE} --wait --timeout 5m"
    exit 0
  fi

  read -rp "Proceed with rollback to revision ${REVISION}? (y/N) " confirm
  if [[ ! "${confirm}" =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 0
  fi

  helm rollback "${RELEASE}" "${REVISION}" \
    --namespace "${NAMESPACE}" \
    --wait \
    --timeout 5m
fi

echo ""
echo "=== Post-rollback status ==="
kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}"
echo ""

# Verify ingestion is healthy
echo "=== Verifying ingestion health ==="
kubectl -n "${NAMESPACE}" wait --for=condition=ready pod \
  -l app=ingestion --timeout=120s && echo "Ingestion pods healthy" || echo "WARNING: Ingestion pods may not be ready"

echo ""
echo "Rollback complete."
