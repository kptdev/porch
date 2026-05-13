#!/usr/bin/env bash
# Copyright 2025 The kpt Authors
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

# Reload a specific component in kind cluster

set -e

# Source common configuration
source "$(dirname "$0")/common.sh"

# Override defaults for kind usage
IMAGE_REPO=${IMAGE_REPO:-porch-kind}
IMAGE_TAG=${IMAGE_TAG:-test}

PATCH_TEMPLATE=`cat <<EOF
{
  "spec": {
    "template": {
      "metadata": {
        "annotations": {
          "kubectl.kubernetes.io/restartedAt": "__DATE__"
        }
      },
      "spec": {
        "containers": [
          {
            "name": "__CONTAINER_NAME__",
            "image": "__IMAGE_NAME__"
          }
        ]
      }
    }
  }
}
EOF
`

COMPONENT=$1

if [ -z "$COMPONENT" ]; then
    echo "Usage: $0 <component>"
    echo "Components: function-runner, server, controllers"
    exit 1
fi

case $COMPONENT in
    "function-runner")
        echo "Building ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG}"
        IMAGE_NAME="${PORCH_FUNCTION_RUNNER_IMAGE}" WRAPPER_SERVER_IMAGE_NAME="${PORCH_WRAPPER_SERVER_IMAGE}" make -C func/ build-image
        kind load docker-image ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG} ${IMAGE_REPO}/${PORCH_WRAPPER_SERVER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        PATCH=$(echo $PATCH_TEMPLATE | sed s/__DATE__/$(date +%s)/ | sed s/__CONTAINER_NAME__/function-runner/ | sed s/__IMAGE_NAME__/${IMAGE_REPO}\\/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG}/)
        kubectl patch -n porch-system deployment/function-runner -p "$PATCH"
        kubectl rollout status deployment function-runner -n porch-system 2>/dev/null || true
        ;;
    "server")
        echo "Building ${IMAGE_REPO}/${PORCH_SERVER_IMAGE}:${IMAGE_TAG}"
        IMAGE_NAME="${PORCH_SERVER_IMAGE}" make -C build/ build-image
        kind load docker-image ${IMAGE_REPO}/${PORCH_SERVER_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        PATCH=$(echo $PATCH_TEMPLATE | sed s/__DATE__/$(date +%s)/ | sed s/__CONTAINER_NAME__/porch-server/ | sed s/__IMAGE_NAME__/${IMAGE_REPO}\\/${PORCH_SERVER_IMAGE}:${IMAGE_TAG}/)
        kubectl patch -n porch-system deployment/porch-server -p "$PATCH"
        kubectl rollout status deployment porch-server -n porch-system 2>/dev/null || true
        ;;
    "controllers")
        echo "Building ${IMAGE_REPO}/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG}"
        IMAGE_NAME="${PORCH_CONTROLLERS_IMAGE}" make -C controllers/ build-image
        kind load docker-image ${IMAGE_REPO}/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG} -n ${KIND_CONTEXT_NAME}
        PATCH=$(echo $PATCH_TEMPLATE | sed s/__DATE__/$(date +%s)/ | sed s/__CONTAINER_NAME__/porch-controllers/ | sed s/__IMAGE_NAME__/${IMAGE_REPO}\\/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG}/)
        kubectl patch -n porch-system deployment/porch-controllers -p "$PATCH"
        kubectl rollout status deployment porch-controllers -n porch-system 2>/dev/null || true
        ;;
    *)
        echo "Unknown component: $COMPONENT"
        exit 1
        ;;
esac