#!/usr/bin/env bash

#  Copyright 2026 The kpt Authors
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

set -euo pipefail

# Determine script and project root directories
HERE=$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")
ROOT=$(readlink -f "${HERE}/..")

# Configuration
PORCH_API_DIR="${ROOT}/api/porch/v1alpha1"
CRD_REF_DOCS_VERSION="v2.0.0"
CRD_REF_CONFIG="${ROOT}/docs/crd-ref-docs/config.yaml"
CRD_REF_MD_TEMPLATES="${ROOT}/docs/crd-ref-docs/markdown-templates"
CRD_REF_OUTPUT_PATH="${ROOT}/docs/content/en/docs/7_cli_api/api-ref.md"

# Validate required directories exist
if [[ ! -d "${PORCH_API_DIR}" ]]; then
    echo "Error: API directory not found: ${PORCH_API_DIR}"
    exit 1
fi

if [[ ! -f "${CRD_REF_CONFIG}" ]]; then
    echo "Error: Config file not found: ${CRD_REF_CONFIG}"
    exit 1
fi

if [[ ! -d "${CRD_REF_MD_TEMPLATES}" ]]; then
    echo "Error: Templates directory not found: ${CRD_REF_MD_TEMPLATES}"
    exit 1
fi

# Ensure crd-ref-docs is installed
if ! command -v crd-ref-docs &>/dev/null; then
    echo "crd-ref-docs not found, installing version ${CRD_REF_DOCS_VERSION}..."
    if ! go install "github.com/elastic/crd-ref-docs@${CRD_REF_DOCS_VERSION}"; then
        echo "Error: Failed to install crd-ref-docs"
        exit 1
    fi
    
    if ! command -v crd-ref-docs &>/dev/null; then
        echo "Error: crd-ref-docs installed but not found in PATH"
        echo "Ensure GOPATH/bin or GOBIN is in your PATH"
        exit 1
    fi
    echo "crd-ref-docs installed successfully"
fi

echo "Generating API reference documentation..."

if ! crd-ref-docs \
  --renderer markdown \
  --config "${CRD_REF_CONFIG}" \
  --output-path "${CRD_REF_OUTPUT_PATH}" \
  --max-depth 15 \
  --templates-dir "${CRD_REF_MD_TEMPLATES}" \
  --source-path "${PORCH_API_DIR}"; then
    echo "Error: crd-ref-docs command failed"
    exit 1
fi

echo "API reference documentation generated successfully"