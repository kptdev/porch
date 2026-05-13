#!/usr/bin/env bash
# Copyright 2022-2026 The kpt Authors
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

# Stricter error handling
set -e # Exit on error
set -u # Must predefine variables
set -o pipefail # Check errors in piped commands

PORCH_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )/.." && pwd )"
cd $PORCH_DIR

kind_cluster="porch-e2e"
kind delete cluster --name "$kind_cluster" || true

# NOTE: use a fix target dir, as opposed to a temporary one, to be able to check the deployment config after the tests have run
deploy_config_dir="${PORCH_DIR}/.build/clean-e2e-test"
rm -rf "$deploy_config_dir" || true
mkdir -p "$deploy_config_dir"
make setup-dev-env PORCH_TEST_CLUSTER="$kind_cluster" GIT_REPO_NAME="porch-test"
make run-in-kind IMAGE_TAG='test' KIND_CONTEXT_NAME="$kind_cluster" DEPLOYPORCHCONFIGDIR="$deploy_config_dir"

sleep 20

echo "--- test/e2e ---"
E2E=1 go test -failfast -v ./test/e2e/api
echo "--- deleting kind cluster that is dedicated for clean end-to-end tests ($kind_cluster) ---"
kind delete cluster --name "$kind_cluster" || true

echo "Done."
