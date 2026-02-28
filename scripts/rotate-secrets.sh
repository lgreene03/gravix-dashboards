#!/usr/bin/env bash
#
# rotate-secrets.sh — Rotate Gravix secrets in Kubernetes
#
# Usage:
#   ./scripts/rotate-secrets.sh [--namespace gravix-prod] [--release gravix]
#
# What it does:
#   1. Generates new cryptographically strong secrets
#   2. Updates the Kubernetes secret in-place
#   3. Restarts affected deployments to pick up new values
#
# For External Secrets Operator users:
#   Rotate secrets in your provider (AWS Secrets Manager, Vault, etc.)
#   and ESO will sync them automatically based on refreshInterval.
#   This script is NOT needed when using ESO.
#
set -euo pipefail

NAMESPACE="${NAMESPACE:-gravix-prod}"
RELEASE="${RELEASE:-gravix}"
SECRET_NAME="${RELEASE}-secrets"

# Parse flags
while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace|-n) NAMESPACE="$2"; shift 2 ;;
    --release|-r) RELEASE="$2"; SECRET_NAME="${RELEASE}-secrets"; shift 2 ;;
    --help|-h)
      echo "Usage: $0 [--namespace <ns>] [--release <name>]"
      echo ""
      echo "Rotates all Gravix secrets and restarts affected workloads."
      echo ""
      echo "Options:"
      echo "  --namespace, -n  Kubernetes namespace (default: gravix-prod)"
      echo "  --release, -r    Helm release name (default: gravix)"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

echo "=== Gravix Secret Rotation ==="
echo "Namespace: ${NAMESPACE}"
echo "Secret:    ${SECRET_NAME}"
echo ""

# Check prerequisites
if ! command -v kubectl &>/dev/null; then
  echo "ERROR: kubectl not found in PATH" >&2
  exit 1
fi

if ! kubectl get namespace "${NAMESPACE}" &>/dev/null; then
  echo "ERROR: Namespace '${NAMESPACE}' does not exist" >&2
  exit 1
fi

if ! kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" &>/dev/null; then
  echo "ERROR: Secret '${SECRET_NAME}' not found in namespace '${NAMESPACE}'" >&2
  exit 1
fi

# Generate new secrets
generate_secret() {
  openssl rand -base64 32 | tr -d '/+=' | head -c "$1"
}

NEW_API_KEY=$(generate_secret 32)
NEW_S3_ACCESS_KEY=$(generate_secret 20)
NEW_S3_SECRET_KEY=$(generate_secret 40)

echo "Generated new secrets:"
echo "  API Key:        ${NEW_API_KEY:0:4}...${NEW_API_KEY: -4} (${#NEW_API_KEY} chars)"
echo "  S3 Access Key:  ${NEW_S3_ACCESS_KEY:0:4}...${NEW_S3_ACCESS_KEY: -4} (${#NEW_S3_ACCESS_KEY} chars)"
echo "  S3 Secret Key:  ${NEW_S3_SECRET_KEY:0:4}...${NEW_S3_SECRET_KEY: -4} (${#NEW_S3_SECRET_KEY} chars)"
echo ""

# Confirm
read -r -p "Apply these new secrets to ${SECRET_NAME} in ${NAMESPACE}? [y/N] " confirm
if [[ ! "${confirm}" =~ ^[Yy]$ ]]; then
  echo "Aborted."
  exit 0
fi

# Patch the secret
echo ""
echo "Patching secret..."
kubectl patch secret "${SECRET_NAME}" -n "${NAMESPACE}" --type='json' -p="[
  {\"op\": \"replace\", \"path\": \"/data/api-key\", \"value\": \"$(echo -n "${NEW_API_KEY}" | base64)\"},
  {\"op\": \"replace\", \"path\": \"/data/s3-access-key\", \"value\": \"$(echo -n "${NEW_S3_ACCESS_KEY}" | base64)\"},
  {\"op\": \"replace\", \"path\": \"/data/s3-secret-key\", \"value\": \"$(echo -n "${NEW_S3_SECRET_KEY}" | base64)\"}
]"

echo "Secret patched successfully."

# Restart deployments that consume the secret
echo ""
echo "Restarting affected deployments..."

DEPLOYMENTS=("${RELEASE}-ingestion")

# Only restart if the deployment exists
for dep in "${RELEASE}-trino" "${RELEASE}-cube" "${RELEASE}-minio" "${RELEASE}-load-generator"; do
  if kubectl get deployment "${dep}" -n "${NAMESPACE}" &>/dev/null; then
    DEPLOYMENTS+=("${dep}")
  fi
done

for dep in "${DEPLOYMENTS[@]}"; do
  echo "  Restarting ${dep}..."
  kubectl rollout restart deployment "${dep}" -n "${NAMESPACE}"
done

echo ""
echo "Waiting for rollouts to complete..."
for dep in "${DEPLOYMENTS[@]}"; do
  kubectl rollout status deployment "${dep}" -n "${NAMESPACE}" --timeout=120s
done

echo ""
echo "=== Secret rotation complete ==="
echo ""
echo "IMPORTANT: If you have external clients sending to the ingestion API,"
echo "update them with the new API key: ${NEW_API_KEY:0:4}...${NEW_API_KEY: -4}"
echo ""
echo "NOTE: CronJobs (rollup, events-rollup, retention) will use the new"
echo "secrets on their next scheduled run — no restart needed."
