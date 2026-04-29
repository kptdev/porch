#!/usr/bin/env bash
# Copyright 2022-2025 The kpt and Nephio Authors
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
STARLARK_IMG="ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5"
SEARCH_REPLACE_IMG="ghcr.io/kptdev/krm-functions-catalog/search-replace:v0.2"
SET_IMAGE_IMG="ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.1"

function error() {
  cat <<EOF
Error: ${1}
Usage: ${0} [flags]
Supported Flags:
  --destination DIRECTORY             ... directory in which to create the Porch deployment blueprint
  --server-image IMAGE                ... address of the Porch server image
  --controllers-image IMAGE           ... address of the Porch controllers image
  --function-image IMAGE              ... address of the Porch function runtime image
  --wrapper-server-image IMAGE        ... address of the Porch function wrapper server image
  --enabled-reconcilers RECONCILERS   ... comma-separated list of reconcilers that should be enabled in
                                          porch controller
  --ghcr-image-prefix PREFIX          ... GHCR image url prefix for running porch behind a proxy
  --fn-runner-warm-up-pod-cache BOOL  ... disable warm-up-pod-cache in function runner
  --porch-cache-type TYPE             ... porch cache type (CR or DB)
  --db-push-drafts-to-git BOOL        ... enable db-push-drafts-to-git flag for porch-server
  --dockerhub-mirror REGISTRY         ... alternate registry to pull additional images from (postgres)
  --create-v1alpha2-rpkg BOOL         ... enable v1alpha2 PackageRevision CRD creation by repo controller
EOF
  exit 1
}

# Flag variables
DESTINATION=""
SERVER_IMAGE=""
CONTROLLERS_IMAGE=""
FUNCTION_IMAGE=""
WRAPPER_SERVER_IMAGE=""
ENABLED_RECONCILERS=""
GHCR_IMAGE_PREFIX=""
DOCKERHUB_MIRROR=""
FN_RUNNER_WARM_UP_POD_CACHE="true"
PORCH_CACHE_TYPE="DB"
DB_PUSH_DRAFTS_TO_GIT="false"
CREATE_V1ALPHA2_RPKG="false"

while [[ $# -gt 0 ]]; do
  key="${1}"
  case "${key}" in
    --destination)
      DESTINATION="${2}"
      shift 2
    ;;
    --server-image)
      SERVER_IMAGE="${2}"
      shift 2
    ;;
    --controllers-image)
      CONTROLLERS_IMAGE="${2}"
      shift 2
    ;;
    --function-image)
      FUNCTION_IMAGE="${2}"
      shift 2
    ;;
    --wrapper-server-image)
      WRAPPER_SERVER_IMAGE="${2}"
      shift 2
    ;;
    --enabled-reconcilers)
      ENABLED_RECONCILERS="${2}"
      shift 2
      ;;
    --ghcr-image-prefix)
      GHCR_IMAGE_PREFIX="${2}"
      shift 2
      ;;
    --fn-runner-warm-up-pod-cache)
      FN_RUNNER_WARM_UP_POD_CACHE="${2}"
      shift 2
      ;;
    --porch-cache-type)
      PORCH_CACHE_TYPE="${2}"
      shift 2
      ;;
    --db-push-drafts-to-git)
      DB_PUSH_DRAFTS_TO_GIT="${2}"
      shift 2
      ;;
    --dockerhub-mirror)
      DOCKERHUB_MIRROR="${2}"
      shift 2
      ;;
    --create-v1alpha2-rpkg)
      CREATE_V1ALPHA2_RPKG="${2}"
      shift 2
      ;;
    *)
      error "Invalid argument: ${key}"
    ;;
  esac
done


function validate() {
  [ -n "${DESTINATION}"       ] || error "--destination is required"
  [ -n "${SERVER_IMAGE}"      ] || error "--server-image is required"
  [ -n "${CONTROLLERS_IMAGE}" ] || error "--controllers-image is required"
  [ -n "${FUNCTION_IMAGE}"    ] || error "--function-image is required"
}

function customize_image {
  local OLD="${1}"
  local NEW="${2}"
  local TAG="${NEW##*:}"
  local IMG="${NEW%:*}"

  kpt fn eval "${DESTINATION}" --image ${SET_IMAGE_IMG} -- \
    "name=${OLD}" \
    "newName=${IMG}" \
    "newTag=${TAG}"
}

function customize_image_in_env {
  local OLD="${1}"
  local NEW="${2}"
  local TAG="${NEW##*:}"
  local IMG="${NEW%:*}"
  local FN_CONFIG="${PORCH_DIR}/.build/set-image-config.yaml"

  cat > "${FN_CONFIG}" << EOF
apiVersion: fn.kpt.dev/v1alpha1
kind: SetImage
metadata:
  name: my-func-config
image:
  name: ${OLD}
  newName: ${IMG}
  newTag: ${TAG}
additionalImageFields:
- group: apps
  version: v1
  kind: Deployment
  path: spec/template/spec/containers[]/env[]/value
EOF

  trap "rm -f ${FN_CONFIG}" EXIT

  kpt fn eval "${DESTINATION}" --image ${SET_IMAGE_IMG} --fn-config "${FN_CONFIG}" || echo "kpt fn eval failed"
}


function customize_controller_reconcilers {
  kpt fn eval ${DESTINATION} \
    --image ${STARLARK_IMG} \
    --match-kind Deployment \
    --match-name porch-controllers \
    --match-namespace porch-system \
    -- "reconcilers=$ENABLED_RECONCILERS" 'source=
reconcilers = ctx.resource_list["functionConfig"]["data"]["reconcilers"].split(",")
for resource in ctx.resource_list["items"]:
  c = resource["spec"]["template"]["spec"]["containers"][0]
  if c["env"] == None:
    c["env"] = []
  for r in reconcilers:
    found = False
    for env in c["env"]:
      if env["name"] == "ENABLE_" + r.upper():
        env["value"] = "true"
        found = True
        break
    if not found:
      c["env"].append({"name": "ENABLE_" + r.upper(), "value": "true"})
'
}

function add_image_args_porch_server() {
    kpt fn eval ${DESTINATION} \
      --image ${STARLARK_IMG} \
      --match-kind Deployment \
      --match-name porch-server \
      --match-namespace porch-system \
      -- "source=
for resource in ctx.resource_list['items']:
  containers = resource['spec']['template']['spec']['containers']
  for container in containers:
    container['args'].append('--default-image-prefix=${GHCR_IMAGE_PREFIX}')
"
}

function disable_fn_runner_warm_up_pod_cache() {
    kpt fn eval ${DESTINATION} \
      --image ${SEARCH_REPLACE_IMG} \
      --match-kind Deployment \
      --match-name function-runner \
      --match-namespace porch-system \
      -- by-value="--warm-up-pod-cache=true" put-value="--warm-up-pod-cache=false"
}

function enable_db_push_drafts_to_git() {
    kpt fn eval ${DESTINATION} \
      --image ${SEARCH_REPLACE_IMG} \
      --match-kind Deployment \
      --match-name porch-server \
      --match-namespace porch-system \
      -- by-value="--db-push-drafts-to-git=false" put-value="--db-push-drafts-to-git=true"
}

function enable_v1alpha2_packagerevisions() {
    echo "Enabling v1alpha2 PackageRevision CRD creation and PR controller"

    # Install the v1alpha2 PackageRevision CRD
    cp "${PORCH_DIR}/api/porch/v1alpha2/porch.kpt.dev_packagerevisions.yaml" \
       "${DESTINATION}/0-v1alpha2-packagerevisions.yaml"

    # Flip the controller flag
    kpt fn eval ${DESTINATION} \
      --image ${SEARCH_REPLACE_IMG} \
      --match-kind Deployment \
      --match-name porch-controllers \
      --match-namespace porch-system \
      -- by-value="--repositories.create-v1alpha2-rpkg=false" put-value="--repositories.create-v1alpha2-rpkg=true"

    # Enable the PR controller that reconciles v1alpha2 CRDs
    ENABLED_RECONCILERS="${ENABLED_RECONCILERS},packagerevisions"

    # Wire the fn-runner address so the PR controller can render packages
    kpt fn eval ${DESTINATION} \
      --image ${STARLARK_IMG} \
      --match-kind Deployment \
      --match-name porch-controllers \
      --match-namespace porch-system \
      -- 'source=
for resource in ctx.resource_list["items"]:
  for container in resource["spec"]["template"]["spec"]["containers"]:
    if container["name"] == "porch-controllers":
      if container["env"] == None:
        container["env"] = []
      container["env"].append({
        "name": "FUNCTION_RUNNER_ADDRESS",
        "value": "function-runner.porch-system.svc.cluster.local:9445"
      })
'
}

function configure_porch_cache() {
    echo "Configuring Porch: cache=${PORCH_CACHE_TYPE}"
    
    adjust_reconcilers_for_cache_type
    
    kpt fn eval ${DESTINATION} \
      --image ${SEARCH_REPLACE_IMG} \
      --match-kind ConfigMap \
      --match-name porch-config \
      --match-namespace porch-system \
      -- by-path=data.cache-type put-value="${PORCH_CACHE_TYPE}"
    
    if [[ "${PORCH_CACHE_TYPE^^}" == "CR" ]]; then
        echo "Configuring porch-api-server for CR cache"
        
        kpt fn eval ${DESTINATION} \
          --image ${STARLARK_IMG} \
          --match-kind Deployment \
          --match-name porch-server \
          --match-namespace porch-system \
          -- "source=
for resource in ctx.resource_list['items']:
    podspec = resource['spec']['template']['spec']
    
    # Update containers
    for container in podspec.get('containers', []):
        if 'envFrom' in container:
            container['envFrom'] = []
        
        args = container.get('args', [])
        for i, arg in enumerate(args):
            if arg.startswith('--cache-type='):
                args[i] = '--cache-type=cr'"

        rm -f "${DESTINATION}"/*porch-postgres*.yaml 2>/dev/null || true
        
        configure_controllers_for_cr_cache
    else
        echo "Configuring porch-api-server for DB cache"
    fi
}

function configure_controllers_for_cr_cache() {
    echo "Configuring porch-controllers for CR cache (no database)"
    kpt fn eval ${DESTINATION} \
      --image ${STARLARK_IMG} \
      --match-kind Deployment \
      --match-name porch-controllers \
      --match-namespace porch-system \
      -- "source=
for resource in ctx.resource_list['items']:
    podspec = resource['spec']['template']['spec']
    
    # Remove wait-for-postgres initContainer
    if 'initContainers' in podspec:
        new_init = [c for c in podspec['initContainers'] if c.get('name') != 'wait-for-postgres']
        if new_init:
            podspec['initContainers'] = new_init
        else:
            podspec.pop('initContainers')
    
    # Update container args and remove database env vars
    for container in podspec.get('containers', []):
        if container.get('name') == 'porch-controllers':
            args = container.get('args', [])
            for i, arg in enumerate(args):
                if arg.startswith('--repositories.cache-type='):
                    args[i] = '--repositories.cache-type=CR'
        if 'envFrom' in container:
            container['envFrom'] = []"
}

function adjust_reconcilers_for_cache_type() {
    # Always ensure repositories reconciler is enabled for standalone controller
    if [[ ! ",${ENABLED_RECONCILERS}," =~ ",repositories," ]]; then
        if [[ -n "${ENABLED_RECONCILERS}" ]]; then
            ENABLED_RECONCILERS="${ENABLED_RECONCILERS},repositories"
        else
            ENABLED_RECONCILERS="repositories"
        fi
        echo "Added 'repositories' to reconcilers list (standalone controller required for both CR and DB cache)"
    fi
}

function main() {
  # Repository CRD
  cp "./api/porchconfig/v1alpha1/config.porch.kpt.dev_repositories.yaml" \
   "${DESTINATION}/0-repositories.yaml"

  # PackageRev CRD
  cp "./internal/api/porchinternal/v1alpha1/config.porch.kpt.dev_packagerevs.yaml" \
     "${DESTINATION}/0-packagerevs.yaml"

  cp "./api/porchconfig/v1alpha1/config.porch.kpt.dev_functionconfigs.yaml" \
     "${DESTINATION}/0-functionconfigs.yaml"

  cp "./api/porchconfig/v1alpha1/config.porch.kpt.dev_servicetemplates.yaml" \
     "${DESTINATION}/0-servicetemplates.yaml"

  # Porch Deployment Config
  cp ${PORCH_DIR}/deployments/porch/*.yaml "${PORCH_DIR}/deployments/porch/Kptfile" "${DESTINATION}"
  # Copy Porch controller manager rbac
  cp ${PORCH_DIR}/controllers/config/rbac/role.yaml "${DESTINATION}/9-porch-controller-clusterrole.yaml"

  if [[ -n "${GHCR_IMAGE_PREFIX}" ]]; then
    add_image_args_porch_server
  fi

  if [[ "${FN_RUNNER_WARM_UP_POD_CACHE}" == "false" ]]; then
    disable_fn_runner_warm_up_pod_cache
  fi

  configure_porch_cache

  if [[ "${DB_PUSH_DRAFTS_TO_GIT}" == "true" ]]; then
    enable_db_push_drafts_to_git
  fi

  if [[ "${CREATE_V1ALPHA2_RPKG}" == "true" ]]; then
    enable_v1alpha2_packagerevisions
  fi

  # Copy RBAC for each enabled reconciler — must run after all reconcilers
  # have been added to ENABLED_RECONCILERS (e.g. by enable_v1alpha2_packagerevisions).
  IFS=',' read -ra RECONCILERS <<< "$ENABLED_RECONCILERS"
  for i in "${RECONCILERS[@]}"; do
    if [[ -f "${PORCH_DIR}/controllers/config/crd/bases/config.porch.kpt.dev_${i}.yaml" ]]; then
      # Copy over the CRD (if it exists)
      cp "${PORCH_DIR}/controllers/config/crd/bases/config.porch.kpt.dev_${i}.yaml" \
         "${DESTINATION}/0-${i}.yaml"
    fi

    # Copy over the rbac rules for the reconciler
    cp "${PORCH_DIR}/controllers/${i}/config/rbac/role.yaml" \
    "${DESTINATION}/9-porch-controller-${i}-clusterrole.yaml"
    # Copy over the rbac rules for the reconciler
    cp "${PORCH_DIR}/controllers/${i}/config/rbac/rolebinding.yaml" \
    "${DESTINATION}/9-porch-controller-${i}-clusterrolebinding.yaml"
  done

  customize_controller_reconcilers
  
  customize_image \
    "docker.io/nephio/porch-function-runner:latest" \
    "${FUNCTION_IMAGE}"
  customize_image \
    "docker.io/nephio/porch-server:latest" \
    "${SERVER_IMAGE}"
  customize_image \
    "docker.io/nephio/porch-controllers:latest" \
    "${CONTROLLERS_IMAGE}"
  customize_image_in_env \
    "docker.io/nephio/porch-wrapper-server:latest" \
    "${WRAPPER_SERVER_IMAGE}"

  if [[ -n "${DOCKERHUB_MIRROR}" ]]; then
    customize_image \
    "docker.io/bitnamilegacy/postgresql:17.6.0-debian-12-r4" \
    "${DOCKERHUB_MIRROR}/bitnamilegacy/postgresql:17.6.0-debian-12-r4"
  fi
}

validate
main
