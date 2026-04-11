#!/usr/bin/env bash
# One-time setup: creates a Kind cluster with Tekton and installs CRDs.
# After this, use hack/run-pipeline.sh to run builds.
#
# Usage:
#   ./hack/setup-cluster.sh                          # basic cluster
#   ./hack/setup-cluster.sh --local /path/to/workspace my-image:tag
#
# The --local flag sets up the cluster for local development:
#   - Mounts the host workspace directory into the Kind node
#   - Creates a PVC backed by the mounted directory
#   - Loads the specified container image into Kind
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="automotive-dev"
TEKTON_VERSION="v1.9.1"
NAMESPACE="automotive-builds"
LOCAL_MODE=false
HOST_WORKSPACE=""
LOCAL_IMAGE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --local)
      LOCAL_MODE=true
      HOST_WORKSPACE="${2:?--local requires <workspace-dir> <image> arguments}"
      LOCAL_IMAGE="${3:?--local requires <workspace-dir> <image> arguments}"
      shift 3
      ;;
    *)
      echo "Unknown option: $1" >&2
      echo "Usage: $0 [--local <workspace-dir> <image>]" >&2
      exit 1
      ;;
  esac
done

if [[ "${LOCAL_MODE}" == "true" ]]; then
  if [[ ! -d "${HOST_WORKSPACE}" ]]; then
    echo "Workspace directory not found: ${HOST_WORKSPACE}" >&2
    exit 1
  fi
  HOST_WORKSPACE="$(cd "${HOST_WORKSPACE}" && pwd)"
fi

for bin in docker kind kubectl; do
  command -v "$bin" >/dev/null 2>&1 || {
    echo "Missing required dependency: $bin" >&2
    exit 1
  }
done

echo "=== Creating Kind cluster '${CLUSTER_NAME}' ==="
kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true

if [[ "${LOCAL_MODE}" == "true" ]]; then
  kind create cluster --name "${CLUSTER_NAME}" --wait 5m --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraMounts:
      - hostPath: ${HOST_WORKSPACE}
        containerPath: /host-workspace
        readOnly: true
EOF
else
  kind create cluster --name "${CLUSTER_NAME}" --wait 5m
fi

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

if [[ "${LOCAL_MODE}" == "true" ]]; then
  echo "=== Loading image '${LOCAL_IMAGE}' into Kind ==="
  kind load docker-image "${LOCAL_IMAGE}" --name "${CLUSTER_NAME}"

  WORKSPACE_NAME="$(basename "${HOST_WORKSPACE}")"
  PVC_NAME="${WORKSPACE_NAME}-workspace"

  echo "=== Creating writable PVC '${PVC_NAME}' ==="
  kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${PVC_NAME}
  namespace: ${NAMESPACE}
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
EOF

  echo "=== Populating PVC from host workspace ==="
  kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: pvc-populator
  namespace: ${NAMESPACE}
spec:
  restartPolicy: Never
  containers:
    - name: copy
      image: busybox
      command: ["sh", "-c", "cp -a /src/. /dest/ && echo 'Copy complete'"]
      volumeMounts:
        - name: host-src
          mountPath: /src
          readOnly: true
        - name: workspace
          mountPath: /dest
  volumes:
    - name: host-src
      hostPath:
        path: /host-workspace
    - name: workspace
      persistentVolumeClaim:
        claimName: ${PVC_NAME}
EOF

  echo "Waiting for copy to complete..."
  kubectl wait --for=condition=Ready pod/pvc-populator -n "${NAMESPACE}" --timeout=120s 2>/dev/null || true
  kubectl wait --for=jsonpath='{.status.phase}'=Succeeded pod/pvc-populator -n "${NAMESPACE}" --timeout=300s
  kubectl delete pod pvc-populator -n "${NAMESPACE}"

  echo ""
  echo "Cluster ready (local mode). Run:"
  echo "  ./hack/run-pipeline.sh <overlay-dir-or-cr.yaml>"
  echo ""
  echo "Local workspace: ${HOST_WORKSPACE} -> copied into PVC '${PVC_NAME}'"
  echo "Image:           ${LOCAL_IMAGE}"
else
  echo ""
  echo "Cluster ready. Run:"
  echo "  ./hack/run-pipeline.sh <path-to-softwarebuild-cr.yaml>"
fi
