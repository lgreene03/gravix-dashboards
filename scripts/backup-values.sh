#!/usr/bin/env bash
set -euo pipefail

#
# backup-values.sh — Export current Helm release values and state.
#
# Creates a local backup of the running Helm release configuration,
# including deployed values, release history, and resource inventory.
# Useful before upgrades, migrations, or disaster recovery.
#
# Usage:
#   ./scripts/backup-values.sh                            # Back up default release
#   ./scripts/backup-values.sh --namespace gravix-staging  # Back up staging
#   ./scripts/backup-values.sh --output /tmp/gravix-backup # Custom output directory
#
# Output structure:
#   <output-dir>/
#     values.yaml              — Deployed Helm values (user-supplied + computed)
#     history.txt              — Helm release revision history
#     resources.yaml           — All Kubernetes resources owned by the release
#     secrets-metadata.txt     — Secret names and keys (NOT values)
#     pvcs.yaml                — PersistentVolumeClaim definitions
#     configmaps.yaml          — ConfigMap definitions
#     manifest.json            — Backup metadata
#

NAMESPACE="gravix-prod"
RELEASE="gravix"
OUTPUT=""
INCLUDE_MANIFESTS=false

usage() {
  cat <<USAGE
Usage: $0 [OPTIONS]

Export Helm release state for backup or migration.

Options:
  --namespace, -n NAME     Kubernetes namespace (default: gravix-prod)
  --release, -r NAME       Helm release name (default: gravix)
  --output, -o DIR         Output directory (default: ./gravix-backup-<timestamp>)
  --include-manifests      Include full rendered manifests (may contain secrets)
  --help, -h               Show this help message

Examples:
  $0                                          # Back up production release
  $0 --namespace gravix-staging               # Back up staging release
  $0 --output /secure/backups/gravix          # Custom output path
  $0 --include-manifests                      # Include full manifests

USAGE
  exit 0
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace|-n) NAMESPACE="$2"; shift 2 ;;
    --release|-r)   RELEASE="$2"; shift 2 ;;
    --output|-o)    OUTPUT="$2"; shift 2 ;;
    --include-manifests) INCLUDE_MANIFESTS=true; shift ;;
    --help|-h)      usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
done

# Determine output directory
TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
if [ -z "${OUTPUT}" ]; then
  OUTPUT="./gravix-backup-${TIMESTAMP}"
fi

echo "=== Gravix Values Backup ==="
echo "Namespace: ${NAMESPACE}"
echo "Release:   ${RELEASE}"
echo "Output:    ${OUTPUT}"
echo ""

# Check prerequisites
for cmd in kubectl helm; do
  if ! command -v "${cmd}" &>/dev/null; then
    echo "ERROR: ${cmd} not found in PATH" >&2
    exit 1
  fi
done

# Verify release exists
if ! helm status "${RELEASE}" --namespace "${NAMESPACE}" &>/dev/null; then
  echo "ERROR: Release '${RELEASE}' not found in namespace '${NAMESPACE}'" >&2
  exit 1
fi

mkdir -p "${OUTPUT}"

# ── 1. Export deployed values ──
echo "Exporting deployed values..."
helm get values "${RELEASE}" --namespace "${NAMESPACE}" -o yaml > "${OUTPUT}/values.yaml"
echo "  ✓ values.yaml"

# ── 2. Export all values (including defaults) ──
echo "Exporting all values (user + defaults)..."
helm get values "${RELEASE}" --namespace "${NAMESPACE}" --all -o yaml > "${OUTPUT}/values-all.yaml"
echo "  ✓ values-all.yaml"

# ── 3. Release history ──
echo "Exporting release history..."
helm history "${RELEASE}" --namespace "${NAMESPACE}" --max 20 > "${OUTPUT}/history.txt" 2>/dev/null || echo "(no history available)" > "${OUTPUT}/history.txt"
echo "  ✓ history.txt"

# ── 4. Resource inventory ──
echo "Exporting resource inventory..."
kubectl get all -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}" -o yaml > "${OUTPUT}/resources.yaml" 2>/dev/null || echo "# No resources found" > "${OUTPUT}/resources.yaml"
echo "  ✓ resources.yaml"

# ── 5. Secret metadata (names and keys only — NOT values) ──
echo "Exporting secret metadata (keys only, no values)..."
{
  echo "# Secret metadata — keys listed below, values NOT included"
  echo "# To restore secrets, use rotate-secrets.sh or External Secrets Operator"
  echo ""
  kubectl get secrets -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}" \
    -o jsonpath='{range .items[*]}Secret: {.metadata.name}{"\n"}  Keys: {range .data}{@}{", "}{end}{"\n\n"}{end}' 2>/dev/null || echo "# No secrets found"
} > "${OUTPUT}/secrets-metadata.txt"
echo "  ✓ secrets-metadata.txt"

# ── 6. PVC definitions ──
echo "Exporting PersistentVolumeClaim definitions..."
kubectl get pvc -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}" -o yaml > "${OUTPUT}/pvcs.yaml" 2>/dev/null || echo "# No PVCs found" > "${OUTPUT}/pvcs.yaml"
echo "  ✓ pvcs.yaml"

# ── 7. ConfigMap definitions ──
echo "Exporting ConfigMap definitions..."
kubectl get configmap -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}" -o yaml > "${OUTPUT}/configmaps.yaml" 2>/dev/null || echo "# No ConfigMaps found" > "${OUTPUT}/configmaps.yaml"
echo "  ✓ configmaps.yaml"

# ── 8. CronJob schedules ──
echo "Exporting CronJob schedules..."
kubectl get cronjobs -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE}" -o wide > "${OUTPUT}/cronjobs.txt" 2>/dev/null || echo "# No CronJobs found" > "${OUTPUT}/cronjobs.txt"
echo "  ✓ cronjobs.txt"

# ── 9. Full rendered manifests (optional) ──
if [ "${INCLUDE_MANIFESTS}" = true ]; then
  echo "Exporting full rendered manifests..."
  helm get manifest "${RELEASE}" --namespace "${NAMESPACE}" > "${OUTPUT}/manifests.yaml"
  echo "  ✓ manifests.yaml"
  echo "  ⚠ WARNING: manifests.yaml may contain secret values!"
fi

# ── 10. Write backup manifest ──
cat > "${OUTPUT}/manifest.json" <<EOF
{
  "backup_type": "helm-values",
  "timestamp": "${TIMESTAMP}",
  "namespace": "${NAMESPACE}",
  "release": "${RELEASE}",
  "helm_version": "$(helm version --short 2>/dev/null || echo 'unknown')",
  "kubectl_version": "$(kubectl version --client --short 2>/dev/null || kubectl version --client -o json 2>/dev/null | python3 -c 'import sys,json; print(json.load(sys.stdin)["clientVersion"]["gitVersion"])' 2>/dev/null || echo 'unknown')",
  "includes_manifests": ${INCLUDE_MANIFESTS},
  "created_by": "backup-values.sh"
}
EOF
echo "  ✓ manifest.json"

echo ""
echo "=== Backup complete ==="
echo "Location: ${OUTPUT}"
echo "Files:    $(find "${OUTPUT}" -type f | wc -l | tr -d ' ')"
echo "Size:     $(du -sh "${OUTPUT}" | cut -f1)"
echo ""
echo "To restore from this backup:"
echo "  helm upgrade ${RELEASE} deploy/gravix \\"
echo "    -f ${OUTPUT}/values.yaml \\"
echo "    --namespace ${NAMESPACE}"
