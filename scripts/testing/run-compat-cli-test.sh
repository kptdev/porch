#!/usr/bin/env bash
# Copyright 2026 The kpt Authors
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

set -o pipefail

CLI_VERSION="${1:?Usage: $0 <cli_version> <server_version>}"
SERVER_VERSION="${2:?Usage: $0 <cli_version> <server_version>}"

cp "${GITHUB_WORKSPACE}/.build/cli/${CLI_VERSION}/porchctl" "${GITHUB_WORKSPACE}/.build/porchctl"
echo "Using porchctl from ${CLI_VERSION}:"
.build/porchctl version 2>/dev/null || echo "version check skipped"

LOG_FILE=".build/compat-results/test-${CLI_VERSION}.log"
set +e
E2E=1 go test -v -timeout 20m ./test/e2e/cli 2>&1 | tee "${LOG_FILE}"
TEST_EXIT=${PIPESTATUS[0]}
set -e

RESULT="pass"
FAILED_TESTS=""
if [ "${TEST_EXIT}" -ne 0 ]; then
  RESULT="fail"
  FAILED_TESTS=$(grep -E '^[[:space:]]*--- FAIL:' "${LOG_FILE}" | sed 's/.*--- FAIL: //' | sed 's/ (.*//' | tr '\n' ',' | sed 's/,$//')
fi

cat > ".build/compat-results/result-${CLI_VERSION}.json" <<EOJSON
{
  "server_version": "${SERVER_VERSION}",
  "cli_version": "${CLI_VERSION}",
  "kpt_version": "$(cat .build/kpt-version.txt 2>/dev/null || echo 'unknown')",
  "result": "${RESULT}",
  "failed_tests": "${FAILED_TESTS}"
}
EOJSON

echo "Result: ${RESULT} | Failed tests: ${FAILED_TESTS}"
