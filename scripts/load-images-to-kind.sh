#!/usr/bin/env bash
# Copyright 2025-2026 The kpt Authors
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

# Load images to kind cluster

set -e

# Source common configuration
source "$(dirname "$0")/common.sh"

# Override defaults for kind usage
IMAGE_REPO=${IMAGE_REPO:-porch-kind}
IMAGE_TAG=${IMAGE_TAG:-test}

echo "Loading images to kind cluster..."

if [ "${SKIP_IMG_BUILD}" = "false" ]; then

    # Build and load function runner if needed
    if ! docker exec "${KIND_CONTEXT_NAME}-control-plane" crictl images | grep -q "${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}  *${IMAGE_TAG} "; then
        echo "Building ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG}"
        IMAGE_NAME="${PORCH_FUNCTION_RUNNER_IMAGE}" WRAPPER_SERVER_IMAGE_NAME="${PORCH_WRAPPER_SERVER_IMAGE}" IMAGE_REPO="${IMAGE_REPO}" IMAGE_TAG="${IMAGE_TAG}" make -C func/ build-image
        kind load docker-image ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        kind load docker-image ${IMAGE_REPO}/${PORCH_WRAPPER_SERVER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
    else
        echo "Skipping building ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG} as it is already loaded into kind"
    fi

    # Build and load porch server if needed
    if [ "${SKIP_PORCHSERVER_BUILD}" = "false" ]; then
        echo "Building ${IMAGE_REPO}/${PORCH_SERVER_IMAGE}:${IMAGE_TAG}"
        IMAGE_NAME="${PORCH_SERVER_IMAGE}" IMAGE_REPO="${IMAGE_REPO}" IMAGE_TAG="${IMAGE_TAG}" make -C build/ build-image
        kind load docker-image ${IMAGE_REPO}/${PORCH_SERVER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        kubectl delete deployment -n porch-system --ignore-not-found=true porch-server
    fi

    # Build and load controllers if needed
    if [ "${SKIP_CONTROLLER_BUILD}" = "false" ]; then
        echo "Building ${IMAGE_REPO}/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG}"
        IMAGE_NAME="${PORCH_CONTROLLERS_IMAGE}" IMAGE_REPO="${IMAGE_REPO}" IMAGE_TAG="${IMAGE_TAG}" make -C controllers/ build-image
        kind load docker-image ${IMAGE_REPO}/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        kubectl delete deployment -n porch-system --ignore-not-found=true porch-controllers
    fi
else
    # Load pre-built images (skip in CI as they're already loaded)
    if [ "${CI:-false}" = "false" ]; then
        kind load docker-image ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        kind load docker-image ${IMAGE_REPO}/${PORCH_WRAPPER_SERVER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        kind load docker-image ${IMAGE_REPO}/${PORCH_SERVER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        kind load docker-image ${IMAGE_REPO}/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
    else
        echo "Skipping image loading in CI environment (images already loaded)"
    fi
fi

echo "Images loaded to kind cluster."