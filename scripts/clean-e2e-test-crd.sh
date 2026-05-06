#!/usr/bin/env bash
# Copyright 2026 The Nephio Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e
set -u
set -o pipefail

PORCH_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )/.." && pwd )"
cd "$PORCH_DIR"

kind_cluster="porch-e2e-crd"

# Timing helper
step_start=0
function timer_start() {
  step_start=$(date +%s)
  echo "=== [$1] started at $(date '+%H:%M:%S') ==="
}
function timer_end() {
  local elapsed=$(( $(date +%s) - step_start ))
  echo "=== [$1] finished in ${elapsed}s ==="
}

timer_start "delete old cluster"
kind delete cluster --name "$kind_cluster" || true
timer_end "delete old cluster"

deploy_config_dir="${PORCH_DIR}/.build/clean-e2e-test-crd"
rm -rf "$deploy_config_dir" || true
mkdir -p "$deploy_config_dir"

timer_start "setup-dev-env"
make setup-dev-env PORCH_TEST_CLUSTER="$kind_cluster" GIT_REPO_NAME="porch-test"
timer_end "setup-dev-env"

timer_start "run-in-kind-v1alpha2"
make run-in-kind-v1alpha2 IMAGE_TAG='test' KIND_CONTEXT_NAME="$kind_cluster" DEPLOYPORCHCONFIGDIR="$deploy_config_dir"
timer_end "run-in-kind-v1alpha2"

timer_start "wait for pods"
echo "Waiting for porch-system pods to be ready..."
kubectl wait --for=condition=ready pod -l app=porch-server -n porch-system --timeout=120s
kubectl wait --for=condition=ready pod -l k8s-app=porch-controllers -n porch-system --timeout=120s
kubectl wait --for=condition=ready pod -l app=function-runner -n porch-system --timeout=120s
timer_end "wait for pods"

echo "--- test/e2e/crd ---"
timer_start "e2e tests"
E2E=1 go test -v -failfast ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='!migration'
timer_end "e2e tests"

echo "--- deleting kind cluster ($kind_cluster) ---"
#kind delete cluster --name "$kind_cluster" || true

echo "Done."
