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

# Generate deployment configuration

set -xe

# Source common configuration
source "$(dirname "$0")/common.sh"

echo "Creating deployment configuration..."

rm -rf "${DEPLOYPORCHCONFIGDIR}" || true
mkdir -p "${DEPLOYPORCHCONFIGDIR}"

./scripts/create-deployment-blueprint.sh \
  --destination "${DEPLOYPORCHCONFIGDIR}" \
  --server-image "${IMAGE_REPO}/${PORCH_SERVER_IMAGE}:${IMAGE_TAG}" \
  --controllers-image "${IMAGE_REPO}/${PORCH_CONTROLLERS_IMAGE}:${IMAGE_TAG}" \
  --function-image "${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG}" \
  --wrapper-server-image "${IMAGE_REPO}/${PORCH_WRAPPER_SERVER_IMAGE}:${IMAGE_TAG}" \
  --enabled-reconcilers "${ENABLED_RECONCILERS}" \
  --fn-runner-warm-up-pod-cache "${FN_RUNNER_WARM_UP_POD_CACHE}" \
  --porch-cache-type "${PORCH_CACHE_TYPE}" \
  --db-push-drafts-to-git "${DB_PUSH_DRAFTS_TO_GIT}" \
  --create-v1alpha2-rpkg "${CREATE_V1ALPHA2_RPKG}" \
  $(if [ -n "${PORCH_GHCR_PREFIX_URL}" ]; then echo "--ghcr-image-prefix \"${PORCH_GHCR_PREFIX_URL}\""; fi) \
  $(if [ -n "${DOCKERHUB_MIRROR}" ]; then echo "--dockerhub-mirror ${DOCKERHUB_MIRROR}"; fi)

echo "Deployment configuration created."
