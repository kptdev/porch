#!/usr/bin/env bash
# Copyright 2024, 2026 The kpt Authors
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

function error() {
  cat <<EOF
Error: ${1}
Usage: ${0} [flags]
Supported Flags:
  --destination DIRECTORY             ... directory in which the kpt pkg will be copied to
  --pkg         PKG                   ... name of the kpt pkg
  --kubeconfig  KUBECONFIG            ... the kubeconfig to use with kpt
EOF
  exit 1
}

# Flag variables
DESTINATION=""
PKG=""
KUBECONFIG=""

while [[ $# -gt 0 ]]; do
  key="${1}"
  case "${key}" in
    --destination)
      DESTINATION="${2}"
      shift 2
    ;;

    --pkg)
      PKG="${2}"
      shift 2
    ;;

    --kubeconfig)
      KUBECONFIG="${2}"
      shift 2
    ;;

    *)
      error "Invalid argument: ${key}"
    ;;
  esac
done

function validate() {
  [ -n "${DESTINATION}"  ] || error "--destination is required"
  [ -n "${PKG}"          ] || error "--pkg is required"
}

function deploy-local-pkg {
	cp -R ./test/pkgs/${PKG} "${DESTINATION}"
  kpt fn render ${DESTINATION}/${PKG} --kubeconfig ${KUBECONFIG}
  kpt live init ${DESTINATION}/${PKG} --kubeconfig ${KUBECONFIG}
  kpt live apply ${DESTINATION}/${PKG} --kubeconfig ${KUBECONFIG}
}

function main() {
  deploy-local-pkg
}

validate
main