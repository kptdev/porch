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
 
# Stricter error handling
set -e # Exit on error
set -u # Must predefine variables
set -o pipefail # Check errors in piped commands
 
self_dir="$(dirname "$(readlink -f "$0")")"
git_repo_name=${1:-porch-test}
gitea_ip=${2:-172.18.255.200}  # should be from the address range in deployments/local/metallb-conf.yaml
 
git_root="$(readlink -f "${self_dir}/..")"
TEST_BLUEPRINTS_PATH="${git_root}/test/pkgs/test-pkgs/test-blueprints.bundle"
cd "${git_root}"
 
function h1() {
  echo
  echo "** $*"
  echo
}

# Function to reload test-blueprints from bundle
reload_test_blueprints_bundle() {
  h1 Reload test-blueprints from bundle
  
  # Setup port-forward
  kubectl port-forward -n gitea svc/gitea-lb 3000:3000 >/dev/null 2>&1 &
  PORT_FORWARD_PID=$!
  sleep 3
  
  # Delete existing repo
  h1 "Deleting existing test-blueprints repository"
  curl -s -X DELETE "http://porch:secret@localhost:3000/api/v1/repos/porch/test-blueprints" >/dev/null 2>&1 || true
  
  # Recreate repo from bundle
  h1 "Recreating test-blueprints repository"
  curl -s -H "content-type: application/json" "http://porch:secret@localhost:3000/api/v1/user/repos" --data '{"name":"test-blueprints"}' >/dev/null 2>&1
  
  TEST_BLUEPRINTS_TMP_DIR=$(mktemp -d)
  cd "$TEST_BLUEPRINTS_TMP_DIR"
  h1 "Cloning from bundle: $TEST_BLUEPRINTS_PATH"
  git clone "$TEST_BLUEPRINTS_PATH" -b main
  cd test-blueprints
  
  git gc
  git remote rename origin upstream
  git remote add origin "http://porch:secret@localhost:3000/porch/test-blueprints"
  git push -u origin --all
  git push -u origin --tags
  
  cd "${git_root}"
  rm -fr "$TEST_BLUEPRINTS_TMP_DIR"
  
  kill $PORT_FORWARD_PID 2>/dev/null || true
  echo "Test-blueprints repo reloaded from bundle"
}
 
# Check if reload command
if [ $# -gt 0 ] && [ "$1" == "reload" ]; then
  reload_test_blueprints_bundle
  exit 0
fi

# Retry function for curl commands
function retry_curl() {
  local max_attempts=$1
  local delay=$2
  shift 2
  local attempt=1
  
  while [ $attempt -le $max_attempts ]; do
    if "$@"; then
      return 0
    fi
    echo "Attempt $attempt/$max_attempts failed, retrying in ${delay} seconds..."
    sleep $delay
    attempt=$((attempt + 1))
  done
  
  echo "ERROR: Command failed after $max_attempts attempts: $*"
  return 1
}
 
h1 Install Gitea and init test repos
mkdir -p "${git_root}/.build"
cd "${git_root}/.build"
if [ -d gitea ]; then
  h1 Dev Gitea pkg already present
else
  cp -r "${git_root}/test/pkgs/gitea-dev" gitea
fi
 
cd "${git_root}/.build/gitea"
 
# Check if the gitea service of type LoadBalancer exists in the 'gitea' namespace
if kubectl get svc gitea-lb -n gitea --no-headers 2>/dev/null | grep -q LoadBalancer; then
  h1 Gitea LoadBalancer service exists. Skipping mutations
else
  h1 Gitea LoadBalancer service does not exist. Mutating pkg...
 
  kpt fn eval \
    --image ghcr.io/kptdev/krm-functions-catalog/set-annotations:v0.1.7 \
    --match-kind Service \
    --match-name gitea-lb \
    --match-namespace gitea \
    -- "metallb.universe.tf/loadBalancerIPs=${gitea_ip}"
fi
 
kpt fn render
kpt live init || true
kpt live apply --inventory-policy=adopt
echo "Waiting for gitea deployment to become ready..."
kubectl wait deployment gitea --for=condition=Available --namespace=gitea --timeout=120s
echo "Waiting for gitea pod to become ready..."
kubectl wait pod -l app=gitea --for=condition=Ready --namespace=gitea --timeout=90s
 
###############################################################
 
h1 Create git repos in gitea

# Ensure port-forward is working and wait for Gitea API to be ready
echo "Setting up port-forward to Gitea service..."
kubectl port-forward -n gitea svc/gitea-lb 3000:3000 >/dev/null 2>&1 &
PORT_FORWARD_PID=$!
sleep 3

# Wait for Gitea API to be ready with retry logic
echo "Waiting for Gitea API to be ready..."
max_attempts=30
attempt=1
while [ $attempt -le $max_attempts ]; do
  if curl -s -f "http://localhost:3000/api/healthz" >/dev/null 2>&1; then
    echo "Gitea API is ready after $attempt attempts"
    break
  fi
  echo "Attempt $attempt/$max_attempts: Gitea API not ready, waiting 5 seconds..."
  sleep 5
  attempt=$((attempt + 1))
done

if [ $attempt -gt $max_attempts ]; then
  echo "ERROR: Gitea API failed to become ready after $max_attempts attempts"
  kill $PORT_FORWARD_PID 2>/dev/null || true
  exit 1
fi

# Create repository with retry logic (handle existing repos gracefully)
if curl -s -f "http://porch:secret@localhost:3000/api/v1/repos/porch/$git_repo_name" >/dev/null 2>&1; then
  echo "Repository $git_repo_name already exists, skipping creation"
else
  if retry_curl 5 3 curl -s -f -H "content-type: application/json" "http://porch:secret@localhost:3000/api/v1/user/repos" --data "{\"name\":\"$git_repo_name\"}"; then
    echo "Successfully created repository $git_repo_name"
  else
    echo "ERROR: Failed to create repository $git_repo_name"
    exit 1
  fi
fi
GITEA_TMP_DIR=$(mktemp -d)
cd "$GITEA_TMP_DIR"
git clone "http://porch:secret@localhost:3000/porch/$git_repo_name.git"
cd "$git_repo_name"
if ! git rev-parse -q --verify refs/remotes/origin/main >/dev/null; then
  echo "Add main branch to git repo:"
  git switch -c  main
  touch README.md
  git add README.md
  git config user.name porch
  git commit -m "first commit"
  git push -u origin main
else
  echo "main branch already exists in git repo."
fi
cd "${git_root}"
rm -fr "$GITEA_TMP_DIR"
 
test_git_repo_name="test-blueprints"

# Create test-blueprints repository with retry logic (handle existing repos gracefully)
if curl -s -f "http://porch:secret@localhost:3000/api/v1/repos/porch/$test_git_repo_name" >/dev/null 2>&1; then
  echo "Repository $test_git_repo_name already exists, skipping creation"
else
  if retry_curl 5 3 curl -s -f -H "content-type: application/json" "http://porch:secret@localhost:3000/api/v1/user/repos" --data "{\"name\":\"$test_git_repo_name\"}"; then
    echo "Successfully created repository $test_git_repo_name"
  else
    echo "ERROR: Failed to create repository $test_git_repo_name"
    exit 1
  fi
fi
TEST_BLUEPRINTS_TMP_DIR=$(mktemp -d)
cd "$TEST_BLUEPRINTS_TMP_DIR"
git clone "$TEST_BLUEPRINTS_PATH" -b main
cd "$test_git_repo_name"
 
git gc
git remote rename origin upstream
git remote add origin "http://porch:secret@localhost:3000/porch/$test_git_repo_name"
git push -u origin --all
git push -u origin --tags
 
cd "${git_root}"
rm -fr "$TEST_BLUEPRINTS_TMP_DIR"

# Clean up port-forward
echo "Cleaning up port-forward..."
kill $PORT_FORWARD_PID 2>/dev/null || true
echo "Gitea setup completed successfully"
