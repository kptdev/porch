#!/usr/bin/env bash
# Fallback defaults for direct script execution
# NOTE: Users should use root Makefile targets instead of calling scripts directly

# Only set defaults if variables are not already exported from Makefile.
# .env entries do not override variables already set in the environment.

PORCHDIR=${PORCHDIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}

if [[ -f "${PORCHDIR}/.env" ]]; then
  _env_trim() {
    local s="$1"
    s="${s#"${s%%[![:space:]]*}"}"
    s="${s%"${s##*[![:space:]]}"}"
    printf '%s' "$s"
  }
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ -z "${line//[[:space:]]/}" ]] && continue
    [[ "$line" != *=* ]] && continue
    key="${line%%=*}"
    value="${line#*=}"
    key="$(_env_trim "$key")"
    value="$(_env_trim "$value")"
    [[ -z "$key" ]] && continue
    [[ -n "${!key+x}" ]] && continue
    export "${key}=${value}"
  done < "${PORCHDIR}/.env"
  unset -f _env_trim
  unset -v key value line
fi

IMAGE_REPO=${IMAGE_REPO:-ghcr.io/kptdev}

PORCH_SERVER_IMAGE=${PORCH_SERVER_IMAGE:-porch-server}
PORCH_CONTROLLERS_IMAGE=${PORCH_CONTROLLERS_IMAGE:-porch-controllers}
PORCH_FUNCTION_RUNNER_IMAGE=${PORCH_FUNCTION_RUNNER_IMAGE:-porch-function-runner}
PORCH_WRAPPER_SERVER_IMAGE=${PORCH_WRAPPER_SERVER_IMAGE:-porch-wrapper-server}
TEST_GIT_SERVER_IMAGE=${TEST_GIT_SERVER_IMAGE:-test-git-server}

SKIP_IMG_BUILD=${SKIP_IMG_BUILD:-false}
SKIP_PORCHSERVER_BUILD=${SKIP_PORCHSERVER_BUILD:-false}
SKIP_CONTROLLER_BUILD=${SKIP_CONTROLLER_BUILD:-false}
SKIP_FUNCTION_RUNNER_BUILD=${SKIP_FUNCTION_RUNNER_BUILD:-false}

KIND_CONTEXT_NAME=${KIND_CONTEXT_NAME:-porch-test}
ENABLED_RECONCILERS=${ENABLED_RECONCILERS:-"packagevariants,packagevariantsets,repositories"}
PORCH_CACHE_TYPE=${PORCH_CACHE_TYPE:-CR}
FN_RUNNER_WARM_UP_POD_CACHE=${FN_RUNNER_WARM_UP_POD_CACHE:-true}
DB_PUSH_DRAFTS_TO_GIT=${DB_PUSH_DRAFTS_TO_GIT:-false}
CREATE_V1ALPHA2_RPKG=${CREATE_V1ALPHA2_RPKG:-false}
DEPLOYPORCHCONFIGDIR=${DEPLOYPORCHCONFIGDIR:-${PORCHDIR}/.build/deploy}

DOCKERHUB_MIRROR=${DOCKERHUB_MIRROR:-""}
DOCKERHUB_MIRROR=${DOCKERHUB_MIRROR%/} # remove '/' suffix
PORCH_GHCR_PREFIX_URL=${PORCH_GHCR_PREFIX_URL:-ghcr.io/kptdev/krm-functions-catalog}
PORCH_GHCR_PREFIX_URL=${PORCH_GHCR_PREFIX_URL%/} # remove '/' suffix
