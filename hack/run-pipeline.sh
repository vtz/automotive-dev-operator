#!/usr/bin/env bash
# Run a SoftwareBuild CR against the local Kind cluster.
# Requires the cluster to be already running (hack/setup-cluster.sh).
#
# Usage:
#   ./hack/run-pipeline.sh <path-to-softwarebuild-cr.yaml> [namespace]
#
# Examples:
#   ./hack/run-pipeline.sh config/samples/automotive_v1alpha1_softwarebuild.yaml
#   ./hack/run-pipeline.sh ../automotive-builds/body-ecu/base/softwarebuild-native-sim.yaml
#   ./hack/run-pipeline.sh ../automotive-builds/body-ecu/base/softwarebuild-native-sim.yaml my-ns
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <softwarebuild-cr.yaml> [namespace]" >&2
  exit 1
fi

CR_FILE="$1"
if [[ ! -f "${CR_FILE}" ]]; then
  echo "File not found: ${CR_FILE}" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAMESPACE="${2:-automotive-builds}"
CR_NAME=$(kubectl apply -f "${CR_FILE}" -n "${NAMESPACE}" --dry-run=client -o jsonpath='{.metadata.name}')
PIPELINE_START=$(date +%s)

echo "=== Reapplying CRDs ==="
cd "${ROOT_DIR}" && make install

echo "=== Cleaning up previous run of '${CR_NAME}' ==="
kubectl delete softwarebuild "${CR_NAME}" -n "${NAMESPACE}" --ignore-not-found
kubectl delete pipelineruns -n "${NAMESPACE}" \
  -l "automotive.sdv.cloud.redhat.com/softwarebuild=${CR_NAME}" \
  --ignore-not-found

echo "=== Building operator ==="
go build -o "${ROOT_DIR}/bin/manager" "${ROOT_DIR}/cmd/main.go"

echo "=== Starting operator in background ==="
"${ROOT_DIR}/bin/manager" &
OPERATOR_PID=$!
trap "echo 'Stopping operator (pid ${OPERATOR_PID})...'; kill ${OPERATOR_PID} 2>/dev/null || true" EXIT
sleep 3

echo "=== Applying SoftwareBuild CR: ${CR_FILE} ==="
kubectl apply -f "${CR_FILE}" -n "${NAMESPACE}"

echo "=== Waiting for PipelineRun to be created ==="
for i in $(seq 1 30); do
  RUN=$(kubectl get softwarebuild "${CR_NAME}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.currentPipelineRun}' 2>/dev/null || true)
  if [[ -n "${RUN}" ]]; then
    break
  fi
  echo "  waiting... (${i}/30)"
  sleep 2
done

if [[ -z "${RUN:-}" ]]; then
  echo "Timed out waiting for PipelineRun to appear." >&2
  kubectl get softwarebuild "${CR_NAME}" -n "${NAMESPACE}" -o yaml
  exit 1
fi
echo "PipelineRun: ${RUN}"

echo "=== Waiting for pipeline to complete (timeout 30m) ==="
kubectl wait --for=condition=Succeeded "pipelinerun/${RUN}" -n "${NAMESPACE}" --timeout=1800s || true

echo ""
echo "=== Results ==="
PHASE=$(kubectl get softwarebuild "${CR_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.phase}')
echo "Phase: ${PHASE}"
echo ""
echo "TaskRuns:"
kubectl get taskruns -n "${NAMESPACE}" -l "tekton.dev/pipelineRun=${RUN}" \
  --no-headers -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[0].reason' 2>/dev/null || true
echo ""

if [[ "${PHASE}" == "Failed" ]]; then
  echo "=== Failure details ==="
  kubectl get pipelinerun "${RUN}" -n "${NAMESPACE}" \
    -o jsonpath='{.status.conditions[0].message}{"\n"}' 2>/dev/null || true
fi

PIPELINE_END=$(date +%s)
echo "Total time: $(( PIPELINE_END - PIPELINE_START ))s"
