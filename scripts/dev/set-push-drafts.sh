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

# Show or set push-drafts-to-git on the running porch-controllers and porch-server of a
# dev cluster, without a redeploy.
#
# Usage: set-push-drafts.sh [true|false|status]
#   true|false  set the flag on both deployments (only those that differ are patched)
#   status      show the current value (default); exits 2 if the two deployments disagree
#
# Only the live deployments change. The next deploy (for example run-in-kind-v1alpha2)
# applies DB_PUSH_DRAFTS_TO_GIT again.

set -e # Exit on error
set -u # Must predefine variables
set -o pipefail # Check errors in piped commands

NAMESPACE=${NAMESPACE:-porch-system}
ROLLOUT_TIMEOUT=${ROLLOUT_TIMEOUT:-180s}

# The two deployments and the flag each uses; the flags must always agree.
DEPLOYMENTS=(porch-controllers porch-server)
FLAGS=(--repositories.push-drafts-to-git --db-push-drafts-to-git)

ACTION=${1:-status}
case "${ACTION}" in
  true | false | status) ;;
  *)
    echo "Usage: $0 [true|false|status]" >&2
    exit 1
    ;;
esac

# container_args <deployment>: one arg per line of the first container.
container_args() {
  kubectl get deployment "$1" -n "${NAMESPACE}" \
    -o jsonpath='{range .spec.template.spec.containers[0].args[*]}{@}{"\n"}{end}'
}

# flag_value <deployment> <flag>: prints the flag's current value.
flag_value() {
  local line
  line=$(container_args "$1" | grep -- "^$2=" | head -n 1 || true)
  if [[ -z "${line}" ]]; then
    echo "Flag $2 not found in the args of deployment/$1" >&2
    return 1
  fi
  echo "${line#*=}"
}

# patch_flag <deployment> <flag> <value>: replaces the flag in the first container's args.
# The flag is found by name, so the order of the args does not matter.
patch_flag() {
  local deployment=$1 flag=$2 value=$3 position
  position=$(container_args "${deployment}" | grep -n -- "^${flag}=" | head -n 1 | cut -d: -f1 || true)
  if [[ -z "${position}" ]]; then
    echo "Flag ${flag} not found in the args of deployment/${deployment}" >&2
    return 1
  fi
  kubectl patch deployment "${deployment}" -n "${NAMESPACE}" --type=json \
    -p="[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/args/$((position - 1))\",\"value\":\"${flag}=${value}\"}]"
}

# show_status: prints the current values; returns 2 if the deployments disagree.
show_status() {
  local i value first="" consistent=true
  for i in "${!DEPLOYMENTS[@]}"; do
    value=$(flag_value "${DEPLOYMENTS[$i]}" "${FLAGS[$i]}")
    echo "  ${DEPLOYMENTS[$i]}: ${FLAGS[$i]}=${value}"
    if [[ "${i}" -eq 0 ]]; then
      first=${value}
    elif [[ "${value}" != "${first}" ]]; then
      consistent=false
    fi
  done
  if [[ "${consistent}" != "true" ]]; then
    echo "WARNING: the deployments disagree; run '$0 true' or '$0 false' to align them." >&2
    return 2
  fi
}

if [[ "${ACTION}" == "status" ]]; then
  echo "push-drafts-to-git:"
  show_status
  exit $?
fi

changed=()
for i in "${!DEPLOYMENTS[@]}"; do
  current=$(flag_value "${DEPLOYMENTS[$i]}" "${FLAGS[$i]}")
  if [[ "${current}" == "${ACTION}" ]]; then
    echo "${DEPLOYMENTS[$i]} already has ${FLAGS[$i]}=${ACTION}"
    continue
  fi
  patch_flag "${DEPLOYMENTS[$i]}" "${FLAGS[$i]}" "${ACTION}"
  changed+=("${DEPLOYMENTS[$i]}")
done

for deployment in "${changed[@]+"${changed[@]}"}"; do
  kubectl rollout status "deployment/${deployment}" -n "${NAMESPACE}" --timeout="${ROLLOUT_TIMEOUT}"
done

echo "push-drafts-to-git:"
show_status
