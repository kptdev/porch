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

# Source common configuration
source "$(dirname "$0")/../lib/common.sh"

PORCH_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )/../.." && pwd )"
CRDS_DIR="$PORCH_DIR/api/generated/crds"

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
  --fn-runner-warm-up-pod-cache BOOL  ... disable warm-up-pod-cache on porch-server
  --porch-cache-type TYPE             ... porch cache type (CR or DB)
  --db-push-drafts-to-git BOOL        ... enable db-push-drafts-to-git flag for porch-server and repo controller
  --create-v1alpha2-rpkg BOOL         ... enable v1alpha2 PackageRevision CRD creation by repo controller
  --allow-direct-publication-from-draft BOOL
                                      ... allow Draft → Published on porch-server and PR controller
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
FN_RUNNER_WARM_UP_POD_CACHE="true"
PORCH_CACHE_TYPE="DB"
DB_PUSH_DRAFTS_TO_GIT="false"
CREATE_V1ALPHA2_RPKG="false"
ALLOW_DIRECT_PUBLICATION_FROM_DRAFT="false"

# key=value pairs for the setters in porch-config.yaml, applied by apply_porch_config_setters
SETTERS=()

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
    --create-v1alpha2-rpkg)
      CREATE_V1ALPHA2_RPKG="${2}"
      shift 2
      ;;
    --allow-direct-publication-from-draft)
      ALLOW_DIRECT_PUBLICATION_FROM_DRAFT="${2}"
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

  kpt fn eval "${DESTINATION}" --image ${SET_IMAGE} -- \
    "name=${OLD}" \
    "newName=${IMG}" \
    "newTag=${TAG}"
}

# Queue a value for the `# kpt-set: ${name}` annotation in porch-config.yaml.
function add_setter() {
  SETTERS+=("${1}=${2}")
}

# Apply the queued setters to the PorchConfig resource, then render it into the
# porch-config ConfigMap (1-porch-config.yaml).
function apply_porch_config_setters() {
  cp "${PORCH_DIR}/deployments/config/porch-config.yaml" "${DESTINATION}/porch-config.yaml"

  kpt fn eval "${DESTINATION}" \
    --image "${APPLY_SETTERS}" \
    --match-kind PorchConfig \
    --match-name porch-config \
    --match-namespace porch-system \
    -- "${SETTERS[@]}"

  kpt fn eval "${DESTINATION}" \
    --image "${STARLARK}" \
    -- "source=$(cat "${PORCH_DIR}/scripts/deploy/render-porch-config.star")"

  # The source is only needed to render the ConfigMap.
  rm -f "${DESTINATION}/porch-config.yaml"
}

function customize_controller_reconcilers {
  local reconciler
  local -a reconcilers
  IFS=',' read -ra reconcilers <<< "${ENABLED_RECONCILERS}"
  for reconciler in "${reconcilers[@]}"; do
    case "${reconciler}" in
      repositories)       add_setter enableRepositories true ;;
      packagerevisions)   add_setter enablePackageRevisions true ;;
      packagevariants)    add_setter enablePackageVariants true ;;
      packagevariantsets) add_setter enablePackageVariantSets true ;;
      "")                 ;;
      *) echo "Warning: reconciler '${reconciler}' has no enable setter in porch-config.yaml, ignoring it for the config" ;;
    esac
  done
}

function configure_allow_direct_publication_from_draft() {
    if [[ "${ALLOW_DIRECT_PUBLICATION_FROM_DRAFT}" != "true" && "${ALLOW_DIRECT_PUBLICATION_FROM_DRAFT}" != "false" ]]; then
      error "--allow-direct-publication-from-draft must be true or false, got '${ALLOW_DIRECT_PUBLICATION_FROM_DRAFT}'"
    fi

    echo "Configuring allow-direct-publication-from-draft=${ALLOW_DIRECT_PUBLICATION_FROM_DRAFT}"
    add_setter allowDirectPublicationFromDraft "${ALLOW_DIRECT_PUBLICATION_FROM_DRAFT}"
}

function enable_v1alpha2_packagerevisions() {
    echo "Enabling v1alpha2 PackageRevision CRD creation and PR controller"

    # Install the v1alpha2 PackageRevision CRD
    cp "${CRDS_DIR}/porch.kpt.dev_packagerevisions.yaml" \
       "${DESTINATION}/0-v1alpha2-packagerevisions.yaml"

    add_setter createV1Alpha2Rpkg true

    # Enable the PR controller that reconciles v1alpha2 CRDs
    ENABLED_RECONCILERS="${ENABLED_RECONCILERS},packagerevisions"

    # Wire the fn-runner address so the PR controller can render packages
    add_setter functionRunnerAddress "function-runner.porch-system.svc.cluster.local:9445"
}

function configure_porch_cache() {
    echo "Configuring Porch: cache=${PORCH_CACHE_TYPE}"
    
    adjust_reconcilers_for_cache_type

    add_setter cacheType "${PORCH_CACHE_TYPE^^}"
    
    if [[ "${PORCH_CACHE_TYPE^^}" == "CR" ]]; then
        echo "Configuring porch-api-server for CR cache"
        
        kpt fn eval ${DESTINATION} \
          --image ${STARLARK} \
          --match-kind Deployment \
          --match-name porch-server \
          --match-namespace porch-system \
          -- "source=
for resource in ctx.resource_list['items']:
    podspec = resource['spec']['template']['spec']
    
    # Remove the database secret env
    for container in podspec.get('containers', []):
        if 'envFrom' in container:
            container['envFrom'] = []"

        rm -f "${DESTINATION}"/*porch-postgres*.yaml 2>/dev/null || true
        
        configure_controllers_for_cr_cache
    else
        echo "Configuring porch-api-server for DB cache"
    fi
}

function configure_controllers_for_cr_cache() {
    echo "Configuring porch-controllers for CR cache (no database)"
    kpt fn eval ${DESTINATION} \
      --image ${STARLARK} \
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
    
    # Remove the database secret env
    for container in podspec.get('containers', []):
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
  cp "${CRDS_DIR}/config.porch.kpt.dev_repositories.yaml" \
   "${DESTINATION}/0-repositories.yaml"

  # PackageRev CRD
  cp "${CRDS_DIR}/config.porch.kpt.dev_packagerevs.yaml" \
     "${DESTINATION}/0-packagerevs.yaml"

  cp "${CRDS_DIR}/config.porch.kpt.dev_functionconfigs.yaml" \
     "${DESTINATION}/0-functionconfigs.yaml"

  cp "${CRDS_DIR}/config.porch.kpt.dev_servicetemplates.yaml" \
     "${DESTINATION}/0-servicetemplates.yaml"

  # Porch Deployment Config
  cp ${PORCH_DIR}/deployments/porch/*.yaml "${PORCH_DIR}/deployments/porch/Kptfile" "${DESTINATION}"

  # Move FunctionConfig/ServiceTemplate CR instances to a separate post-deploy directory.
  # These are applied after the main deployment rollout checks pass to avoid kpt live apply reconcile timeouts
  # caused by applying a CRD and its instances in the same operation.
  rm -rf "${DESTINATION}-post"
  mkdir -p "${DESTINATION}-post"
  mv "${DESTINATION}/22-function-templates.yaml" "${DESTINATION}/23-function-configurations.yaml" "${DESTINATION}-post/"
  # Copy Porch controller manager rbac
  cp ${PORCH_DIR}/controllers/config/rbac/role.yaml "${DESTINATION}/9-porch-controller-clusterrole.yaml"

  if [[ -n "${GHCR_IMAGE_PREFIX}" ]]; then
    add_setter defaultImagePrefix "${GHCR_IMAGE_PREFIX}"
  fi

  add_setter warmUpPodCache "${FN_RUNNER_WARM_UP_POD_CACHE}"

  configure_porch_cache

  add_setter pushDraftsToGit "${DB_PUSH_DRAFTS_TO_GIT}"

  if [[ "${CREATE_V1ALPHA2_RPKG}" == "true" ]]; then
    enable_v1alpha2_packagerevisions
  fi

  configure_allow_direct_publication_from_draft

  if [[ -n "${WRAPPER_SERVER_IMAGE}" ]]; then
    add_setter wrapperServerImage "${WRAPPER_SERVER_IMAGE}"
  fi

  # Copy RBAC for each enabled reconciler — must run after all reconcilers
  # have been added to ENABLED_RECONCILERS (e.g. by enable_v1alpha2_packagerevisions).
  IFS=',' read -ra RECONCILERS <<< "$ENABLED_RECONCILERS"
  
  # Generate webhook certificates for deployment
  WEBHOOK_CERT_DIR="${DESTINATION}/.webhook-certs"
  mkdir -p "${WEBHOOK_CERT_DIR}"
  bash "${PORCH_DIR}/scripts/lib/webhook-utils.sh" generate-certs "${WEBHOOK_CERT_DIR}" porch-controllers porch-system

  # Create Secret with webhook certificates for pod mounting
  bash "${PORCH_DIR}/scripts/lib/webhook-utils.sh" create-secret "${WEBHOOK_CERT_DIR}" "${DESTINATION}/8-webhook-certs-secret.yaml"
  
  for i in "${RECONCILERS[@]}"; do
    if [[ -f "${CRDS_DIR}/config.porch.kpt.dev_${i}.yaml" ]]; then
      # Copy over the CRD (if it exists)
      cp "${CRDS_DIR}/config.porch.kpt.dev_${i}.yaml" \
         "${DESTINATION}/0-${i}.yaml"
    fi

    # Copy over the rbac rules for the reconciler
    cp "${PORCH_DIR}/controllers/${i}/config/rbac/role.yaml" \
    "${DESTINATION}/9-porch-controller-${i}-clusterrole.yaml"
    # Copy over the rbac rules for the reconciler
    cp "${PORCH_DIR}/controllers/${i}/config/rbac/rolebinding.yaml" \
    "${DESTINATION}/9-porch-controller-${i}-clusterrolebinding.yaml"
    
    # Copy webhook manifests if they exist and inject caBundle
    if [[ -f "${PORCH_DIR}/controllers/${i}/config/webhook/manifests.yaml" ]]; then
      WEBHOOK_FILE="${DESTINATION}/9-porch-controller-${i}-validating-webhook.yaml"
      cp "${PORCH_DIR}/controllers/${i}/config/webhook/manifests.yaml" "${WEBHOOK_FILE}"
      
      # Inject caBundle from generated CA certificate (not server cert)
      CA_CERT_FILE="${WEBHOOK_CERT_DIR}/ca.crt"
      if [[ -f "$CA_CERT_FILE" ]]; then
        bash "${PORCH_DIR}/scripts/lib/webhook-utils.sh" inject-cabundle "${WEBHOOK_FILE}" "${CA_CERT_FILE}"
      else
        echo "Warning: Webhook CA certificate not found at $CA_CERT_FILE, skipping caBundle injection for ${i}"
      fi
    fi
  done

  customize_controller_reconcilers
  apply_porch_config_setters
  
  customize_image \
    "ghcr.io/kptdev/porch-function-runner:latest" \
    "${FUNCTION_IMAGE}"
  customize_image \
    "ghcr.io/kptdev/porch-server:latest" \
    "${SERVER_IMAGE}"
  customize_image \
    "ghcr.io/kptdev/porch-controllers:latest" \
    "${CONTROLLERS_IMAGE}"

  if [[ -n "${DOCKERHUB_MIRROR}" ]]; then
    customize_image \
    "docker.io/bitnamilegacy/postgresql:17.6.0-debian-12-r4" \
    "${DOCKERHUB_MIRROR}/bitnamilegacy/postgresql:17.6.0-debian-12-r4"
  fi
}

validate
main
