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

kind_version=$(awk '/helm\/kind-action@v1/,/version:/ {if (/version:/) print $2}' .github/workflows/porch-e2e-ci-jobs.yaml | head -1)
echo "kind version (.github/workflows): $kind_version"

kube_node_image=$(grep "kindest/node:" deployments/local/kind_porch_test_cluster.yaml | sed 's/.*\(v[0-9]*\.[0-9]*\.[0-9]*\).*/\1/' | head -1)
echo "Kubernetes node image (local dev): $kube_node_image"

echo ""
echo "=== Versions in docs/config.toml ==="

config_go=$(grep '^version_go = ' docs/config.toml | cut -d'"' -f2)
echo "Go version (config): $config_go"

config_kpt=$(grep '^version_kpt = ' docs/config.toml | cut -d'"' -f2)
echo "kpt version (config): $config_kpt"

config_kind=$(grep '^version_kind = ' docs/config.toml | cut -d'"' -f2)
echo "kind version (config): $config_kind"

config_kube=$(grep '^version_kube = ' docs/config.toml | cut -d'"' -f2)
echo "Kubernetes version (config): $config_kube"

config_git=$(grep '^version_git = ' docs/config.toml | cut -d'"' -f2)
config_docker=$(grep '^version_docker = ' docs/config.toml | cut -d'"' -f2)

echo ""
echo "=== Consistency Check ==="

errors=0
warnings=0
declare -a fixes_needed

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
exit 0
