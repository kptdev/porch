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

# verify-release-artifacts.sh
#
# Verifies that all expected artifacts are published to a GitHub release
# and that their checksums match the published checksums.txt.
#
# Usage:
#   ./scripts/verify-release-artifacts.sh <tag>
#
# Requirements:
#   - gh CLI installed and authenticated (gh auth login)
#   - For CI: GITHUB_TOKEN env var is set automatically by GitHub Actions
#
# Example:
#   ./scripts/verify-release-artifacts.sh v1.5.11

set -euo pipefail

TAG="${1:-}"
if [[ -z "${TAG}" ]]; then
  echo "ERROR: tag argument is required"
  echo "Usage: $0 <tag>"
  exit 1
fi

# Ensure gh CLI is available
if ! command -v gh &>/dev/null; then
  echo "ERROR: gh CLI is not installed. Install from https://cli.github.com/"
  exit 1
fi

# Determine the checksum command (Linux: sha256sum, macOS: shasum -a 256)
if command -v sha256sum &>/dev/null; then
  SHA256_CMD="sha256sum"
elif command -v shasum &>/dev/null; then
  SHA256_CMD="shasum -a 256"
else
  echo "ERROR: neither sha256sum nor shasum found"
  exit 1
fi

# Strip leading 'v' to get the version number (goreleaser uses version without 'v')
VERSION="${TAG#v}"

# Repository in owner/name format — detect from env or infer from gh
REPO="${GITHUB_REPOSITORY:-}"
if [[ -z "${REPO}" ]]; then
  REPO=$(gh repo view --json nameWithOwner -q '.nameWithOwner' 2>/dev/null || true)
fi
if [[ -z "${REPO}" ]]; then
  echo "ERROR: could not determine repository. Set GITHUB_REPOSITORY or run from a git checkout."
  exit 1
fi

echo "==> Verifying release artifacts for ${TAG} in ${REPO}"

# Expected artifact names based on goreleaser.yaml configuration
EXPECTED_ASSETS=(
  "porchctl_${VERSION}_darwin_amd64.tar.gz"
  "porchctl_${VERSION}_darwin_arm64.tar.gz"
  "porchctl_${VERSION}_linux_amd64.tar.gz"
  "porchctl_${VERSION}_linux_arm64.tar.gz"
  "porch_blueprint.tar.gz"
  "checksums.txt"
)

# Retry logic for asset listing — the release may take a moment to propagate
MAX_RETRIES=5
RETRY_DELAY=10
ASSETS=""

for ((i = 1; i <= MAX_RETRIES; i++)); do
  echo "  Attempt ${i}/${MAX_RETRIES}: listing release assets..."
  ASSETS=$(gh release view "${TAG}" --repo "${REPO}" --json assets -q '.assets[].name' 2>/dev/null || true)
  if [[ -n "${ASSETS}" ]]; then
    break
  fi
  if [[ ${i} -lt ${MAX_RETRIES} ]]; then
    echo "  Release not found yet, retrying in ${RETRY_DELAY}s..."
    sleep "${RETRY_DELAY}"
  fi
done

if [[ -z "${ASSETS}" ]]; then
  echo "ERROR: could not retrieve release assets for tag ${TAG}"
  echo "       The release may not exist or has no assets."
  exit 1
fi

echo ""
echo "==> Assets found on release:"
while IFS= read -r asset; do
  echo "    ${asset}"
done <<< "${ASSETS}"
echo ""

# Check all expected assets are present
MISSING=()
for expected in "${EXPECTED_ASSETS[@]}"; do
  if ! grep -qxF "${expected}" <<< "${ASSETS}"; then
    MISSING+=("${expected}")
  fi
done

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "ERROR: the following expected artifacts are MISSING from the release:"
  for m in "${MISSING[@]}"; do
    echo "  - ${m}"
  done
  exit 1
fi

echo "==> All expected artifacts are present."

# Download artifacts and verify checksums
WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT

echo ""
echo "==> Downloading artifacts to verify checksums..."
gh release download "${TAG}" --repo "${REPO}" --dir "${WORKDIR}"

echo ""
echo "==> Verifying checksums..."
cd "${WORKDIR}"

if [[ ! -f "checksums.txt" ]]; then
  echo "ERROR: checksums.txt was not found after download"
  exit 1
fi

echo "  Contents of checksums.txt:"
sed 's/^/    /' < checksums.txt
echo ""

# Verify each file listed in checksums.txt
FAILED=0
CHECKED=0
while IFS= read -r line; do
  # Skip empty lines
  [[ -z "${line}" ]] && continue
  # Each line is: <hash>  <filename> (two spaces between hash and name)
  CHECKSUM=$(awk '{print $1}' <<< "${line}")
  FILENAME=$(awk '{print $2}' <<< "${line}")

  if [[ -z "${FILENAME}" || -z "${CHECKSUM}" ]]; then
    echo "  ! Skipping malformed line: ${line}"
    continue
  fi

  if [[ -f "${FILENAME}" ]]; then
    ACTUAL=$(${SHA256_CMD} "${FILENAME}" | awk '{print $1}')
    if [[ "${ACTUAL}" == "${CHECKSUM}" ]]; then
      echo "  ✓ ${FILENAME}"
      CHECKED=$((CHECKED + 1))
    else
      echo "  ✗ ${FILENAME} — CHECKSUM MISMATCH"
      echo "    expected: ${CHECKSUM}"
      echo "    actual:   ${ACTUAL}"
      FAILED=1
    fi
  else
    echo "  ? ${FILENAME} — not downloaded (skipped)"
  fi
done < checksums.txt

echo ""
if [[ ${FAILED} -ne 0 ]]; then
  echo "ERROR: one or more checksum verifications FAILED"
  exit 1
fi

if [[ ${CHECKED} -eq 0 ]]; then
  echo "ERROR: no files were checked against checksums — something is wrong"
  exit 1
fi

echo "==> All checksums verified successfully (${CHECKED} files)."
echo "==> Release ${TAG} is complete and valid."
