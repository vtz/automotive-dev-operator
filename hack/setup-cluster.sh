#!/usr/bin/env bash
# One-time setup: creates a Kind cluster with Tekton and installs CRDs.
# After this, use hack/run-pipeline.sh <cr.yaml> to run builds.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="automotive-dev"
TEKTON_VERSION="v1.9.1"
NAMESPACE="automotive-builds"

for bin in docker kind kubectl; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "Missing required dependency: $bin" >&2
    exit 1
  }
done

echo "=== Creating Kind cluster '${CLUSTER_NAME}' ==="
kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true
kind create cluster --name "${CLUSTER_NAME}" --wait 5m

echo "=== Installing Tekton Pipelines ${TEKTON_VERSION} ==="
kubectl apply --filename \
  "https://infra.tekton.dev/tekton-releases/pipeline/previous/${TEKTON_VERSION}/release.yaml"
echo "Waiting for Tekton controller..."
kubectl wait --for=condition=ready pod --all -n tekton-pipelines --timeout=180s

echo "=== Installing CRDs ==="
cd "${ROOT_DIR}" && make install

echo "=== Creating namespace '${NAMESPACE}' ==="
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

echo "=== Applying OperatorConfig ==="
kubectl apply -f - <<EOF
apiVersion: automotive.sdv.cloud.redhat.com/v1alpha1
kind: OperatorConfig
metadata:
  name: default
  namespace: ${NAMESPACE}
spec:
  softwareBuilds:
    enabled: true
EOF

echo ""
echo "Cluster ready. Run:"
echo "  ./hack/run-pipeline.sh <path-to-softwarebuild-cr.yaml>"
