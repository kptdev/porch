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
self_dir="$(dirname "$(readlink -f "$0")")" 

# Start timing
start_time=$(date +%s)

porch_cluster_name=${PORCH_TEST_CLUSTER:-porch-test}
git_repo_name=${GIT_REPO_NAME:-$porch_cluster_name}
install_gitea=true

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --no-gitea)
      install_gitea=false
      shift
      ;;
    --help|-h)
      echo "Usage: $0 [--no-gitea] [--help]"
      echo "  --no-gitea    Skip gitea installation"
      echo "  --help        Show this help message"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

git_root="$(readlink -f "${self_dir}/..")"
cd "${git_root}"

function h1() {
  echo
  echo "** $*"
  echo 
}

##############################################
# check if git user.name is set in your environment
if ! git config --global user.name > /dev/null ; then
  cat << ENDOFMESG
Please set the user.name and user.email global configuration values in your git environment.
This is needed for porch tests to run correctly. 
E.g:
  git config --global user.name "My Name"
  git config --global user.email "myemail@example.com"
ENDOFMESG
  exit 128
fi

##############################################
h1 "Install kind cluster: $porch_cluster_name"
if ! kind get clusters | grep -q "^$porch_cluster_name\$" ; then
  mkdir -p ~/.kube
  kind create cluster \
    --config="${git_root}/deployments/local/kind_porch_test_cluster.yaml" \
    --name "$porch_cluster_name" \
    --kubeconfig ~/.kube/"kind-$porch_cluster_name"
else
  echo "Cluster already exists."
fi
kind export kubeconfig --name="$porch_cluster_name"

##############################################
h1 Install MetalLB
# Source helper for dynamic IP range discovery from kind Docker network
source "${git_root}/scripts/get-kind-metallb-subnet.sh"

if ! kubectl get namespace metallb-system >/dev/null 2>&1; then
  kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.14.9/config/manifests/metallb-native.yaml
  sleep 1
  echo "Waiting for controller to become ready..."
  kubectl wait --namespace metallb-system deploy controller \
                  --for=condition=available \
                  --timeout=90s
  # Generate MetalLB config dynamically from the kind Docker network subnet
  generate_metallb_config | kubectl apply -f -
else
  echo "MetalLB already installed"
fi

############################################
# Install gitea and setup test repos
if [ "$install_gitea" = true ]; then
  h1 "Install gitea and setup test repos"
  cd "${git_root}"
  ./scripts/install-dev-gitea-setup.sh "$git_repo_name"
else
  echo "Skipping gitea installation (--no-gitea flag provided)"
fi 

############################################
h1 Generate certs and keys
cd "${git_root}"
./deployments/local/makekeys.sh

############################################
h1 "Build the porch CLI (.build/porchctl)"
cd "${git_root}"
make porchctl

############################################
echo
echo "Done."
echo "Total setup time: $(($(date +%s) - start_time))s"
