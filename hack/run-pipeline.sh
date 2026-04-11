#!/usr/bin/env bash
# Run a SoftwareBuild CR against the local Kind cluster.
# Requires the cluster to be already running (hack/setup-cluster.sh).
#
# Usage:
#   ./hack/run-pipeline.sh <path-to-cr-or-overlay-dir> [namespace]
#
# Accepts either a single CR YAML file or a Kustomize overlay directory.
#
# Examples:
#   # Direct CR file
#   ./hack/run-pipeline.sh config/samples/automotive_v1alpha1_softwarebuild.yaml
#
#   # Kustomize overlay (local dev)
#   ./hack/run-pipeline.sh ../automotive-builds/body-ecu/overlays/local
#
#   # Kustomize overlay (CI / prod)
#   ./hack/run-pipeline.sh ../automotive-builds/body-ecu/overlays/prod
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <softwarebuild-cr.yaml | kustomize-overlay-dir> [namespace]" >&2
  exit 1
fi

INPUT="$1"
if [[ ! -e "${INPUT}" ]]; then
  echo "Path not found: ${INPUT}" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAMESPACE="${2:-automotive-builds}"
PIPELINE_START=$(date +%s)

if [[ -d "${INPUT}" ]]; then
  echo "=== Rendering Kustomize overlay: ${INPUT} ==="
  CR_FILE=$(mktemp /tmp/softwarebuild-XXXXXX.yaml)
  trap "rm -f '${CR_FILE}'" EXIT
  kubectl kustomize "${INPUT}" > "${CR_FILE}"
else
  CR_FILE="${INPUT}"
fi

echo "=== Reapplying CRDs ==="
cd "${ROOT_DIR}" && make install

CR_NAMES=$(grep -E '^\s+name:\s' "${CR_FILE}" | awk '{print $2}')
if [[ -z "${CR_NAMES}" ]]; then
  echo "No SoftwareBuild resources found in ${CR_FILE}" >&2
  exit 1
fi

echo "=== Cleaning up previous runs ==="
for name in ${CR_NAMES}; do
  kubectl delete softwarebuild "${name}" -n "${NAMESPACE}" --ignore-not-found
  kubectl delete pipelineruns -n "${NAMESPACE}" \
    -l "automotive.sdv.cloud.redhat.com/softwarebuild=${name}" \
    --ignore-not-found
done

echo "=== Building operator ==="
go build -o "${ROOT_DIR}/bin/manager" "${ROOT_DIR}/cmd/main.go"

echo "=== Starting operator in background ==="
"${ROOT_DIR}/bin/manager" &
OPERATOR_PID=$!
cleanup() {
  echo "Stopping operator (pid ${OPERATOR_PID})..."
  kill "${OPERATOR_PID}" 2>/dev/null || true
  [[ -d "${INPUT}" ]] && rm -f "${CR_FILE}" 2>/dev/null || true
}
trap cleanup EXIT
sleep 3

echo "=== Applying SoftwareBuild CRs ==="
kubectl apply -f "${CR_FILE}" -n "${NAMESPACE}"

for CR_NAME in ${CR_NAMES}; do
  echo ""
  echo "=== [$CR_NAME] Waiting for PipelineRun ==="
  RUN=""
  for i in $(seq 1 30); do
    RUN=$(kubectl get softwarebuild "${CR_NAME}" -n "${NAMESPACE}" \
      -o jsonpath='{.status.pipelineRunName}' 2>/dev/null || true)
    if [[ -n "${RUN}" ]]; then
      break
    fi
    echo "  waiting... (${i}/30)"
    sleep 2
  done

  if [[ -z "${RUN}" ]]; then
    echo "[$CR_NAME] Timed out waiting for PipelineRun." >&2
    kubectl get softwarebuild "${CR_NAME}" -n "${NAMESPACE}" -o yaml
    continue
  fi
  echo "[$CR_NAME] PipelineRun: ${RUN}"

  echo "=== [$CR_NAME] Waiting for pipeline to complete (timeout 30m) ==="
  kubectl wait --for=condition=Succeeded "pipelinerun/${RUN}" -n "${NAMESPACE}" --timeout=1800s || true

  echo ""
  echo "=== [$CR_NAME] Results ==="
  PHASE=$(kubectl get softwarebuild "${CR_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.phase}')
  echo "Phase: ${PHASE}"
  echo ""
  echo "TaskRuns:"
  kubectl get taskruns -n "${NAMESPACE}" -l "tekton.dev/pipelineRun=${RUN}" \
    --no-headers -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[0].reason' 2>/dev/null || true
  echo ""

  if [[ "${PHASE}" == "Failed" ]]; then
    echo "=== [$CR_NAME] Failure details ==="
    kubectl get pipelinerun "${RUN}" -n "${NAMESPACE}" \
      -o jsonpath='{.status.conditions[0].message}{"\n"}' 2>/dev/null || true
  fi
done

PIPELINE_END=$(date +%s)
echo "Total time: $(( PIPELINE_END - PIPELINE_START ))s"
