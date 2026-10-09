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

set -e

# Script to check version consistency between source files and docs/config.toml
# Ensures public docs align with the code being built and tested.
# Validates all version parameters including Porch component versions and compatibility ranges.
#
# Usage:
#   scripts/util/check-versions.sh              - Check only, fail on mismatches
#   scripts/util/check-versions.sh --fix        - Check and auto-fix all version mismatches (Go, kpt, kind, k8s)

FIX_MODE=""
if [ "$1" = "--fix" ]; then
  FIX_MODE="true"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

echo "Checking version consistency..."
echo ""

# Extract versions from sources
echo "=== Extracting versions from source files ==="
go_version=$(grep '^go ' go.mod | awk '{print $2}')
echo "Go version (go.mod): $go_version"

kpt_version=$(awk '/github.com\/kptdev\/kpt / {print $2; exit}' go.mod)
echo "kpt version (go.mod): $kpt_version"

porch_api_version=$(awk '/^[^#]*github.com\/kptdev\/porch\/api / {print $2; exit}' go.mod)
echo "Porch API version (go.mod): $porch_api_version"

kind_version=$(awk '/helm\/kind-action@v1/,/version:/ {if (/version:/) print $2}' .github/workflows/porch-e2e-ci-jobs.yaml | head -1)
echo "kind version (.github/workflows): $kind_version"

kube_node_image=$(grep "kindest/node:" deployments/local/kind_porch_test_cluster.yaml | sed 's/.*\(v[0-9]*\.[0-9]*\.[0-9]*\).*/\1/' | head -1)
echo "Kubernetes node image (local dev): $kube_node_image"

echo ""
echo "=== Versions in docs/config.toml ==="

# Extract all config.toml parameters at once (avoid duplicate greps)
# Dependency versions (existing - tested with)
config_go=$(grep '^version_go = ' docs/config.toml | cut -d'"' -f2)
config_kpt=$(grep '^version_kpt = ' docs/config.toml | cut -d'"' -f2)
config_kind=$(grep '^version_kind = ' docs/config.toml | cut -d'"' -f2)
config_kube=$(grep '^version_kube = ' docs/config.toml | cut -d'"' -f2)
config_git=$(grep '^version_git = ' docs/config.toml | cut -d'"' -f2)
config_docker=$(grep '^version_docker = ' docs/config.toml | cut -d'"' -f2)

# Porch component versions
config_porch_server=$(grep '^version_porch_server = ' docs/config.toml | cut -d'"' -f2)
config_porch_api=$(grep '^version_porch_api = ' docs/config.toml | cut -d'"' -f2)
config_porch_server_dev=$(grep '^version_porch_server_dev = ' docs/config.toml | cut -d'"' -f2)
config_porch_api_dev=$(grep '^version_porch_api_dev = ' docs/config.toml | cut -d'"' -f2)

# Go version support ranges
config_go_min_stable=$(grep '^version_go_min_stable = ' docs/config.toml | cut -d'"' -f2)
config_go_max_stable=$(grep '^version_go_max_stable = ' docs/config.toml | cut -d'"' -f2)
config_go_min_dev=$(grep '^version_go_min_dev = ' docs/config.toml | cut -d'"' -f2)
config_go_max_dev=$(grep '^version_go_max_dev = ' docs/config.toml | cut -d'"' -f2)

# Kubernetes version support
config_kube_min=$(grep '^version_kube_min = ' docs/config.toml | cut -d'"' -f2)
config_kube_latest=$(grep '^version_kube_latest = ' docs/config.toml | cut -d'"' -f2)

# Git and kpt CLI minimum versions
config_git_min=$(grep '^version_git_min = ' docs/config.toml | cut -d'"' -f2)
config_kpt_min=$(grep '^version_kpt_min = ' docs/config.toml | cut -d'"' -f2)

# Display extracted versions
echo "Dependencies (tested with):"
echo "  Go: $config_go | kpt: $config_kpt | kind: $config_kind | k8s: $config_kube"
echo "  Git: $config_git | Docker: $config_docker"
echo ""
echo "Porch components:"
echo "  Server (stable): $config_porch_server | API (stable): $config_porch_api"
echo "  Server (dev): $config_porch_server_dev | API (dev): $config_porch_api_dev"
echo ""
echo "Version support ranges:"
echo "  Go (stable): $config_go_min_stable - $config_go_max_stable"
echo "  Go (dev): $config_go_min_dev - $config_go_max_dev"
echo "  Kubernetes: min=$config_kube_min, latest=$config_kube_latest"
echo "  Git minimum: $config_git_min | kpt CLI minimum: $config_kpt_min"

echo ""
echo "=== Consistency Check ==="

errors=0
warnings=0
declare -a fixes_needed

# Helper function to extract major.minor version
get_minor_version() {
  echo "$1" | cut -d. -f1,2
}

# Helper function to compare versions (returns 0 if equal, 1 if v1 < v2, 2 if v1 > v2)
compare_versions() {
  # Returns 0 if $1 == $2, 1 if $1 < $2, 2 if $1 > $2
  local v1=$1 v2=$2
  if [ "$v1" = "$v2" ]; then return 0; fi
  local lower=$(echo -e "$v1\n$v2" | sort -V | head -n1)
  if [ "$lower" = "$v1" ]; then return 1; else return 2; fi
}

# Go version check (CRITICAL - we control this in go.mod)
if [ "$go_version" != "$config_go" ]; then
  echo "FAIL: Go version mismatch - source: $go_version, config: $config_go"
  errors=$((errors + 1))
  if [ "$FIX_MODE" = "true" ]; then
    fixes_needed+=("version_go|$go_version")
  fi
else
  echo "✓ Go version matches: $config_go"
fi

# kpt version check (CRITICAL - core dependency in go.mod)
if [ "$kpt_version" != "$config_kpt" ]; then
  echo "FAIL: kpt version mismatch - source: $kpt_version, config: $config_kpt"
  errors=$((errors + 1))
  if [ "$FIX_MODE" = "true" ]; then
    fixes_needed+=("version_kpt|$kpt_version")
  fi
else
  echo "✓ kpt version matches: $config_kpt"
fi

# Porch API version check (WARNING - uses replace directive during development)
# During development, the replace directive takes precedence, so we can't reliably extract the version
# On main branch, compare against dev version; otherwise compare against stable
BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
if [ "$BRANCH" = "main" ]; then
  target_api_version="v$config_porch_api_dev"
else
  target_api_version="v$config_porch_api"
fi

if [ -n "$porch_api_version" ] && [ "$porch_api_version" != "replace" ] && [ "$porch_api_version" != "$target_api_version" ]; then
  echo "WARN: Porch API version mismatch - go.mod: $porch_api_version, config: $target_api_version"
  warnings=$((warnings + 1))
elif [ "$porch_api_version" != "replace" ]; then
  echo "✓ Porch API version matches: $target_api_version (using replace directive in development)"
fi

# kind version check (WARNING - test environment, controls k8s version)
if [ "$kind_version" != "$config_kind" ]; then
  echo "WARN: kind version mismatch - source: $kind_version, config: $config_kind"
  echo "      Consider updating docs/config.toml to match the test environment"
  warnings=$((warnings + 1))
  if [ "$FIX_MODE" = "true" ]; then
    fixes_needed+=("version_kind|$kind_version")
  fi
else
  echo "✓ kind version matches: $config_kind"
fi

# Kubernetes version check (compare dev config with config.toml)
if [ "$kube_node_image" != "$config_kube" ]; then
  echo "WARN: Kubernetes version mismatch - dev config: $kube_node_image, docs config: $config_kube"
  warnings=$((warnings + 1))
  if [ "$FIX_MODE" = "true" ]; then
    fixes_needed+=("version_kube|$kube_node_image")
  fi
else
  echo "✓ Kubernetes version matches: $config_kube"
fi

echo "(info) Kubernetes version (from config): $config_kube (derived from kind)"

echo ""
echo "=== Porch Version Consistency Checks ==="

# Check that Porch server versions exist as git tags
if ! git rev-list "v$config_porch_server" >/dev/null 2>&1; then
  echo "WARN: Porch server version v$config_porch_server not found in git tags"
  warnings=$((warnings + 1))
else
  echo "✓ Porch server version v$config_porch_server exists as git tag"
fi

if ! git rev-list "v$config_porch_server_dev" >/dev/null 2>&1; then
  echo "WARN: Porch server dev version v$config_porch_server_dev not found in git tags"
  warnings=$((warnings + 1))
else
  echo "✓ Porch server dev version v$config_porch_server_dev exists as git tag"
fi

# Check that API module versions exist as git tags
if ! git rev-list "api/v$config_porch_api" >/dev/null 2>&1; then
  echo "WARN: Porch API version v$config_porch_api not found in git tags"
  warnings=$((warnings + 1))
else
  echo "✓ Porch API version v$config_porch_api exists as git tag"
fi

if ! git rev-list "api/v$config_porch_api_dev" >/dev/null 2>&1; then
  echo "WARN: Porch API dev version v$config_porch_api_dev not found in git tags"
  warnings=$((warnings + 1))
else
  echo "✓ Porch API dev version v$config_porch_api_dev exists as git tag"
fi

# Check that dev versions look like pre-releases
if [[ "$config_porch_server_dev" != *"-pre"* && "$config_porch_server_dev" != *"-alpha"* && "$config_porch_server_dev" != *"-beta"* ]]; then
  echo "WARN: version_porch_server_dev ($config_porch_server_dev) doesn't look like a pre-release"
  warnings=$((warnings + 1))
else
  echo "✓ Porch server dev version looks like a pre-release"
fi

echo ""
echo "=== Version Range Consistency Checks ==="

# Go version range validation (stable)
if compare_versions "$config_go_min_stable" "$config_go_max_stable"; result=$?; [ "$result" = 2 ]; then
  echo "FAIL: Go min_stable ($config_go_min_stable) is greater than max_stable ($config_go_max_stable)"
  errors=$((errors + 1))
else
  echo "✓ Go stable range is valid: $config_go_min_stable <= $config_go_max_stable"
fi

# Go version range validation (dev)
if compare_versions "$config_go_min_dev" "$config_go_max_dev"; result=$?; [ "$result" = 2 ]; then
  echo "FAIL: Go min_dev ($config_go_min_dev) is greater than max_dev ($config_go_max_dev)"
  errors=$((errors + 1))
else
  echo "✓ Go dev range is valid: $config_go_min_dev <= $config_go_max_dev"
fi

# Kubernetes version validation
if compare_versions "$config_kube_min" "$config_kube_latest"; result=$?; [ "$result" = 2 ]; then
  echo "FAIL: Kubernetes min ($config_kube_min) is greater than latest ($config_kube_latest)"
  errors=$((errors + 1))
else
  echo "✓ Kubernetes version range is valid: $config_kube_min <= $config_kube_latest"
fi

# Current Go version should be within tested range
if compare_versions "$config_go" "$config_go_max_stable"; result=$?; [ "$result" = 2 ]; then
  echo "WARN: Current Go version ($config_go) is newer than tested max_stable ($config_go_max_stable)"
  warnings=$((warnings + 1))
elif compare_versions "$config_go" "$config_go_min_stable"; result=$?; [ "$result" = 1 ]; then
  echo "WARN: Current Go version ($config_go) is older than tested min_stable ($config_go_min_stable)"
  warnings=$((warnings + 1))
else
  echo "✓ Current Go version ($config_go) is within tested range"
fi

# kubectl version should be within one minor version of Kubernetes API server
# Extract minor versions (major.minor)
kube_minor=$(get_minor_version "$config_kube_latest")
# For min version guidance (kubectl can be one minor older)
kube_min_minor=$(get_minor_version "$config_kube_min")

echo ""
echo "=== Kubectl Compatibility Check ==="
echo "Kubernetes cluster versions: $config_kube_min (min) to $config_kube_latest (latest)"
echo "kubectl should be within one minor version of the cluster API server"
echo "  Recommended range: $kube_min_minor to $kube_minor"

# Only check runner versions if in CI
if [ -n "$GITHUB_ACTIONS" ]; then
  echo ""
  echo "=== Runner Environment Checks (CI only) ==="
  
  git_version=$(git --version | awk '{print $3}')
  echo "Git version (runner): $git_version"
  
  docker_version=$(docker --version | sed 's/.*version \([0-9.]*\).*/\1/')
  echo "Docker version (runner): $docker_version"
  
  # Compare with config (config has 'v' prefix, extract for comparison)
  config_git_unprefixed="${config_git#v}"
  config_docker_unprefixed="${config_docker#v}"
  
  if [ "$git_version" != "$config_git_unprefixed" ]; then
    echo "  (info) Git mismatch: runner has $git_version, config has $config_git"
  else
    echo "  ✓ Git matches config: $git_version"
  fi
  
  if [ "$docker_version" != "$config_docker_unprefixed" ]; then
    echo "  (info) Docker mismatch: runner has $docker_version, config has $config_docker"
  else
    echo "  ✓ Docker matches config: $docker_version"
  fi
fi

echo ""

# Handle auto-fix if requested
if [ "$FIX_MODE" != "" ] && [ ${#fixes_needed[@]} -gt 0 ]; then
  echo "=== Auto-Fixing Versions ==="
  temp_file=$(mktemp)
  cp docs/config.toml "$temp_file"
  for fix in "${fixes_needed[@]}"; do
    key="${fix%|*}"
    value="${fix#*|}"
    echo "Updating $key = \"$value\""
    sed "s/^$key = \"[^\"]*\"/$key = \"$value\"/" "$temp_file" > "${temp_file}.tmp"
    mv "${temp_file}.tmp" "$temp_file"
  done
  mv "$temp_file" docs/config.toml
  echo "✓ Updated docs/config.toml"
  echo ""
  echo "Please review the changes and commit them:"
  echo "  git diff docs/config.toml"
  echo "  git add docs/config.toml"
  echo "  git commit -s -m 'chore: update version pinning in docs'"
  exit 0
fi

echo ""
if [ "$errors" -gt 0 ]; then
  echo "FAILED: $errors critical version mismatch(es) detected."
  echo ""
  echo "To auto-fix critical mismatches, run:"
  echo "  scripts/util/check-versions.sh --fix"
  echo "  or: make check-versions-fix"
  exit 1
fi

echo "SUCCESS: Critical versions are aligned with code."
if [ "$warnings" -gt 0 ]; then
  echo "         ($warnings warning(s) detected - review above)"
fi
exit 0
