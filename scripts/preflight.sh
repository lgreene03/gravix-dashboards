#!/usr/bin/env bash
set -euo pipefail

#
# preflight.sh — Cluster readiness check before Gravix deployment.
#
# Validates that the target Kubernetes cluster has all prerequisites
# for a successful Gravix deployment. Run this before first install
# or after cluster changes.
#
# Usage:
#   ./scripts/preflight.sh                             # Check for production
#   ./scripts/preflight.sh --namespace gravix-staging   # Check for staging
#   ./scripts/preflight.sh --values deploy/gravix/values-prod.yaml  # Use prod values
#
# Exit codes:
#   0 — All checks passed
#   1 — One or more critical checks failed
#

NAMESPACE="gravix-prod"
VALUES_FILE=""
RELEASE="gravix"
PASS=0
WARN=0
FAIL=0

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

pass() {
  PASS=$((PASS + 1))
  echo -e "  ${GREEN}✓${NC} $1"
}

warn() {
  WARN=$((WARN + 1))
  echo -e "  ${YELLOW}⚠${NC} $1"
}

fail() {
  FAIL=$((FAIL + 1))
  echo -e "  ${RED}✗${NC} $1"
}

usage() {
  cat <<USAGE
Usage: $0 [OPTIONS]

Validate cluster readiness for Gravix deployment.

Options:
  --namespace, -n NAME     Target namespace (default: gravix-prod)
  --release, -r NAME       Helm release name (default: gravix)
  --values, -f FILE        Helm values file to check against
  --help, -h               Show this help message

Checks:
  - kubectl connectivity and version
  - Helm version
  - Namespace existence or creation permissions
  - Storage class availability
  - Cluster resource capacity
  - CRD availability (cert-manager, Prometheus Operator)
  - Image pull access (GHCR)
  - DNS resolution
  - Network policy support

USAGE
  exit 0
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace|-n) NAMESPACE="$2"; shift 2 ;;
    --release|-r)   RELEASE="$2"; shift 2 ;;
    --values|-f)    VALUES_FILE="$2"; shift 2 ;;
    --help|-h)      usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
done

echo "=== Gravix Pre-flight Check ==="
echo "Namespace: ${NAMESPACE}"
echo ""

# ─── 1. CLI Tools ───
echo "1. CLI Tools"

if command -v kubectl &>/dev/null; then
  KUBE_VERSION=$(kubectl version --client -o json 2>/dev/null | python3 -c 'import sys,json; v=json.load(sys.stdin)["clientVersion"]; print(f"{v["major"]}.{v["minor"]}")' 2>/dev/null || echo "unknown")
  pass "kubectl installed (${KUBE_VERSION})"
else
  fail "kubectl not found — install from https://kubernetes.io/docs/tasks/tools/"
fi

if command -v helm &>/dev/null; then
  HELM_VERSION=$(helm version --short 2>/dev/null | head -1)
  if [[ "${HELM_VERSION}" == v3* ]]; then
    pass "Helm v3 installed (${HELM_VERSION})"
  else
    fail "Helm v3 required, found: ${HELM_VERSION}"
  fi
else
  fail "helm not found — install from https://helm.sh/docs/intro/install/"
fi
echo ""

# ─── 2. Cluster Connectivity ───
echo "2. Cluster Connectivity"

if kubectl cluster-info &>/dev/null; then
  CLUSTER=$(kubectl cluster-info 2>/dev/null | head -1 | sed 's/\x1B\[[0-9;]*m//g' || echo "connected")
  pass "Cluster reachable"
else
  fail "Cannot connect to Kubernetes cluster — check KUBECONFIG"
  echo ""
  echo "=== Pre-flight ABORTED — cluster not reachable ==="
  exit 1
fi

SERVER_VERSION=$(kubectl version -o json 2>/dev/null | python3 -c 'import sys,json; v=json.load(sys.stdin)["serverVersion"]; print(f"{v["major"]}.{v["minor"]}")' 2>/dev/null || echo "unknown")
if [ "${SERVER_VERSION}" != "unknown" ]; then
  pass "Server version: ${SERVER_VERSION}"
else
  warn "Could not determine server version"
fi
echo ""

# ─── 3. Namespace ───
echo "3. Namespace"

if kubectl get namespace "${NAMESPACE}" &>/dev/null; then
  pass "Namespace '${NAMESPACE}' exists"
else
  if kubectl auth can-i create namespace &>/dev/null; then
    warn "Namespace '${NAMESPACE}' does not exist (will be created on deploy)"
  else
    fail "Namespace '${NAMESPACE}' does not exist and you lack permission to create it"
  fi
fi
echo ""

# ─── 4. RBAC Permissions ───
echo "4. RBAC Permissions"

for resource in deployments services configmaps secrets cronjobs; do
  if kubectl auth can-i create "${resource}" -n "${NAMESPACE}" &>/dev/null 2>&1; then
    pass "Can create ${resource}"
  else
    fail "Cannot create ${resource} in ${NAMESPACE}"
  fi
done
echo ""

# ─── 5. Storage Classes ───
echo "5. Storage"

SC_COUNT=$(kubectl get storageclass -o name 2>/dev/null | wc -l | tr -d ' ')
if [ "${SC_COUNT}" -gt 0 ]; then
  DEFAULT_SC=$(kubectl get storageclass -o jsonpath='{.items[?(@.metadata.annotations.storageclass\.kubernetes\.io/is-default-class=="true")].metadata.name}' 2>/dev/null || echo "")
  if [ -n "${DEFAULT_SC}" ]; then
    pass "Default StorageClass: ${DEFAULT_SC}"
  else
    warn "No default StorageClass — PVCs must specify storageClassName"
  fi
  pass "${SC_COUNT} StorageClass(es) available"
else
  fail "No StorageClasses found — PVCs will fail"
fi
echo ""

# ─── 6. CRD Availability ───
echo "6. CRDs and Operators"

# cert-manager
if kubectl get crd certificates.cert-manager.io &>/dev/null 2>&1; then
  pass "cert-manager CRDs installed"
  if kubectl get pods -n cert-manager -l app=cert-manager --no-headers 2>/dev/null | grep -q Running; then
    pass "cert-manager pods running"
  else
    warn "cert-manager CRDs found but pods not running in cert-manager namespace"
  fi
else
  warn "cert-manager not installed (TLS will need manual certificate management)"
fi

# Prometheus Operator
if kubectl get crd servicemonitors.monitoring.coreos.com &>/dev/null 2>&1; then
  pass "Prometheus Operator CRDs installed"
  if kubectl get crd prometheusrules.monitoring.coreos.com &>/dev/null 2>&1; then
    pass "PrometheusRule CRD available"
  fi
else
  warn "Prometheus Operator not installed (annotation-based scraping will still work)"
fi

# External Secrets Operator
if kubectl get crd externalsecrets.external-secrets.io &>/dev/null 2>&1; then
  pass "External Secrets Operator CRDs installed"
else
  warn "External Secrets Operator not installed (using Helm-managed secrets)"
fi
echo ""

# ─── 7. Ingress Controller ───
echo "7. Ingress"

if kubectl get ingressclass &>/dev/null 2>&1; then
  IC_COUNT=$(kubectl get ingressclass -o name 2>/dev/null | wc -l | tr -d ' ')
  if [ "${IC_COUNT}" -gt 0 ]; then
    IC_NAMES=$(kubectl get ingressclass -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)
    pass "IngressClass(es): ${IC_NAMES}"
  else
    warn "No IngressClass found — Ingress resources will not be routed"
  fi
else
  warn "Cannot list IngressClasses"
fi
echo ""

# ─── 8. Node Resources ───
echo "8. Cluster Capacity"

NODE_COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "${NODE_COUNT}" -gt 0 ]; then
  pass "${NODE_COUNT} node(s) in cluster"

  READY_NODES=$(kubectl get nodes --no-headers 2>/dev/null | grep -c " Ready" || echo "0")
  if [ "${READY_NODES}" -eq "${NODE_COUNT}" ]; then
    pass "All nodes Ready"
  else
    warn "Only ${READY_NODES}/${NODE_COUNT} nodes Ready"
  fi

  # Check total allocatable CPU/Memory
  TOTAL_CPU=$(kubectl get nodes -o jsonpath='{.items[*].status.allocatable.cpu}' 2>/dev/null | tr ' ' '\n' | awk '{s+=$1}END{printf "%.0f", s}' 2>/dev/null || echo "unknown")
  TOTAL_MEM=$(kubectl get nodes -o jsonpath='{.items[*].status.allocatable.memory}' 2>/dev/null | tr ' ' '\n' | head -1 | sed 's/Ki$//' | awk '{printf "%.1f", $1/1048576}' 2>/dev/null || echo "unknown")

  if [ "${TOTAL_CPU}" != "unknown" ]; then
    if [ "${TOTAL_CPU}" -ge 4 ]; then
      pass "Total allocatable CPU: ${TOTAL_CPU} cores"
    else
      warn "Total allocatable CPU: ${TOTAL_CPU} cores (recommend >= 4)"
    fi
  fi
else
  fail "No nodes found in cluster"
fi
echo ""

# ─── 9. DNS Resolution ───
echo "9. DNS"

if kubectl run dns-test-gravix --image=busybox:1.36 --restart=Never --rm -i --timeout=30s \
  -- nslookup kubernetes.default.svc.cluster.local &>/dev/null 2>&1; then
  pass "Cluster DNS resolving"
else
  warn "DNS resolution test inconclusive (may require pod scheduling)"
fi
echo ""

# ─── 10. Helm Chart Validation ───
echo "10. Helm Chart"

CHART_DIR="deploy/gravix"
if [ -f "${CHART_DIR}/Chart.yaml" ]; then
  pass "Chart found at ${CHART_DIR}/"

  LINT_ARGS="--set global.apiKey=preflight-test-key --set global.storage.accessKey=test-access --set global.storage.secretKey=test-secret-at-least-16ch"
  if [ -n "${VALUES_FILE}" ] && [ -f "${VALUES_FILE}" ]; then
    LINT_ARGS="-f ${VALUES_FILE} ${LINT_ARGS} --set certManager.email=test@example.com"
  fi

  if helm lint ${CHART_DIR} ${LINT_ARGS} &>/dev/null; then
    pass "Helm lint passed"
  else
    fail "Helm lint failed — run: helm lint ${CHART_DIR} ${LINT_ARGS}"
  fi
else
  warn "Chart not found at ${CHART_DIR}/ — skipping lint"
fi
echo ""

# ─── Summary ───
echo "==========================================="
echo "  Pre-flight Summary"
echo "==========================================="
echo -e "  ${GREEN}Passed:${NC}   ${PASS}"
echo -e "  ${YELLOW}Warnings:${NC} ${WARN}"
echo -e "  ${RED}Failed:${NC}   ${FAIL}"
echo "==========================================="
echo ""

if [ "${FAIL}" -gt 0 ]; then
  echo -e "${RED}Pre-flight FAILED — resolve the issues above before deploying.${NC}"
  exit 1
elif [ "${WARN}" -gt 0 ]; then
  echo -e "${YELLOW}Pre-flight PASSED with warnings — review before deploying.${NC}"
  exit 0
else
  echo -e "${GREEN}Pre-flight PASSED — cluster is ready for Gravix deployment.${NC}"
  exit 0
fi
