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

# build-versioned-docs.sh
#
# Builds versioned Hugo documentation using:
#   - Presentation (theme, layouts, config) from the current working tree
#   - Content from git tags for each released version
#
# Reads docs/versions.json to determine which versions to build.
# Resolves tagPattern globs to the latest semver tag dynamically.
#
# Usage:
#   ./scripts/build-versioned-docs.sh [output-dir]
#
# Environment:
#   HUGO_ENV - Hugo environment (default: production)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DOCS_DIR="${REPO_ROOT}/docs"
OUTPUT_DIR="${1:-${DOCS_DIR}/public}"
HUGO_ENV="${HUGO_ENV:-production}"

# Track temp directories for cleanup on exit
TEMP_DIRS=()
cleanup() {
  for d in "${TEMP_DIRS[@]}"; do
    rm -rf "${d}" 2>/dev/null || true
  done
  # Remove overlay file if left behind by a failed build
  rm -f "${DOCS_DIR}/config-versions-overlay.toml" 2>/dev/null || true
}
trap cleanup EXIT

# Ensure required tools are available
for cmd in hugo git npm awk tar mktemp; do
  if ! command -v "${cmd}" &> /dev/null; then
    echo "ERROR: ${cmd} is required but not installed." >&2
    exit 1
  fi
done

VERSIONS_FILE="${DOCS_DIR}/versions.json"
if [[ ! -f "${VERSIONS_FILE}" ]]; then
  echo "ERROR: ${VERSIONS_FILE} not found." >&2
  exit 1
fi

echo "==> Building versioned docs"
echo "    Output: ${OUTPUT_DIR}"
echo "    Versions file: ${VERSIONS_FILE}"
echo ""

# Ensure we have all tags (Netlify may do shallow clones)
echo "==> Fetching tags..."
git -C "${REPO_ROOT}" fetch --unshallow 2>/dev/null || true
git -C "${REPO_ROOT}" fetch --tags --force 2>/dev/null || true
echo ""

# Clean output directory (with safety check)
if [[ -z "${OUTPUT_DIR}" || "${OUTPUT_DIR}" == "/" ]]; then
  echo "ERROR: OUTPUT_DIR is empty or root. Refusing to delete." >&2
  exit 1
fi
rm -rf "${OUTPUT_DIR}"
mkdir -p "${OUTPUT_DIR}"

# Install npm dependencies once (needed for PostCSS/autoprefixer)
echo "==> Installing npm dependencies..."
(
  cd "${DOCS_DIR}"
  if [[ -f "package.json" ]]; then
    npm install --quiet || {
      echo "ERROR: npm install failed. CSS processing may not work correctly." >&2
      exit 1
    }
  fi
)
echo ""

# Build the main/latest version from the current working tree
echo "==> Building latest (main) docs..."

# Clear Hugo module cache to ensure correct versions are used
(cd "${DOCS_DIR}" && hugo mod clean 2>/dev/null || true)

# Generate version dropdown config from versions.json (single source of truth)
# NOTE: The AWK parser below expects versions.json to have one key per line
# (standard pretty-printed JSON). Reformatting to single-line objects will break this.
VERSIONS_TOML=$(awk '
  /"version"/ { gsub(/[",]/, ""); ver=$2 }
  /"path"/ { gsub(/[",]/, ""); path=$2; print "[[params.versions]]"; print "  version = \"" ver "\""; print "  url = \"" path "\""; print "" }
' "${VERSIONS_FILE}")

cat > "${DOCS_DIR}/config-versions-overlay.toml" <<EOF
${VERSIONS_TOML}
EOF

(
  cd "${DOCS_DIR}"
  hugo --gc --minify \
    --environment "${HUGO_ENV}" \
    --destination "${OUTPUT_DIR}" \
    -b "/" \
    --config config.toml,config-versions-overlay.toml
)
rm -f "${DOCS_DIR}/config-versions-overlay.toml"
echo "    Done: latest -> /"
echo ""

# resolve_latest_tag: given a glob pattern like "v1.5.*", find the highest semver tag
resolve_latest_tag() {
  local pattern="$1"
  git -C "${REPO_ROOT}" tag --list "${pattern}" --sort=-v:refname | head -1
}

# Parse versions.json and build each tagged version.
# Extract entries where tagPattern is not null
# NOTE: The AWK parser below expects versions.json to have one key per line
# (standard pretty-printed JSON). Reformatting to single-line objects will break this.
TAGGED_VERSIONS=$(awk '
  /"version"/ { gsub(/[",]/, ""); version=$2 }
  /"tagPattern"/ { gsub(/[",]/, ""); pattern=$2 }
  /"path"/ { gsub(/[",]/, ""); path=$2; if (pattern != "null" && pattern != "") print version "\t" pattern "\t" path }
' "${VERSIONS_FILE}")

while IFS=$'\t' read -r VERSION PATTERN URL_PATH; do
  [[ -z "${VERSION}" ]] && continue

  # Validate URL_PATH: must start with / and not contain traversal sequences
  if [[ ! "${URL_PATH}" =~ ^/[a-zA-Z0-9._/-]*$ ]] || [[ "${URL_PATH}" == *".."* ]]; then
    echo "    ERROR: Invalid URL_PATH '${URL_PATH}' for ${VERSION}. Must be an absolute path without traversal." >&2
    continue
  fi

  # Resolve the tag pattern to the latest matching tag
  TAG=$(resolve_latest_tag "${PATTERN}")
  if [[ -z "${TAG}" ]]; then
    echo "    WARNING: No tags matching '${PATTERN}' found, skipping ${VERSION}." >&2
    continue
  fi

  echo "==> Building ${VERSION} from tag ${TAG} (pattern: ${PATTERN})..."

  # Create a temporary build directory
  TEMP_DIR=$(mktemp -d)
  TEMP_DIRS+=("${TEMP_DIR}")
  TEMP_DOCS="${TEMP_DIR}/docs"
  mkdir -p "${TEMP_DOCS}"

  # Option A: presentation from current tree, content from tag
  # Copy the full docs directory (theme, layouts, config, assets) from current tree
  cp -r "${DOCS_DIR}/assets" "${TEMP_DOCS}/" 2>/dev/null || true
  cp -r "${DOCS_DIR}/layouts" "${TEMP_DOCS}/" 2>/dev/null || true
  cp -r "${DOCS_DIR}/static" "${TEMP_DOCS}/" 2>/dev/null || true
  ln -s "${DOCS_DIR}/node_modules" "${TEMP_DOCS}/node_modules" 2>/dev/null || true
  cp "${DOCS_DIR}/go.mod" "${TEMP_DOCS}/"
  cp "${DOCS_DIR}/go.sum" "${TEMP_DOCS}/" 2>/dev/null || true
  cp "${DOCS_DIR}/config.toml" "${TEMP_DOCS}/"
  cp "${DOCS_DIR}/package.json" "${TEMP_DOCS}/" 2>/dev/null || true
  cp "${DOCS_DIR}/postcss.config.js" "${TEMP_DOCS}/" 2>/dev/null || true

  # Extract docs content (required) and static images (optional) from the tag.
  # This gives us the version-specific documentation text.
  if ! git -C "${REPO_ROOT}" archive "${TAG}" -- docs/content/ 2>/dev/null | \
    tar -x -C "${TEMP_DIR}" 2>/dev/null; then
    echo "    WARNING: Failed to extract docs content from tag ${TAG}, skipping ${VERSION}." >&2
    continue
  fi

  # If the tag doesn't contain static images, keep the images from the current tree.
  git -C "${REPO_ROOT}" archive "${TAG}" -- docs/static/images/ 2>/dev/null | \
    tar -x -C "${TEMP_DIR}" 2>/dev/null || true

  # Verify that content was actually extracted
  if [[ ! -d "${TEMP_DIR}/docs/content" ]]; then
    echo "    WARNING: Tag ${TAG} has no docs/content/ directory, skipping ${VERSION}." >&2
    continue
  fi

  # Replace the home page with main's version for consistent nav/layout.
  cp "${DOCS_DIR}/content/en/_index.md" "${TEMP_DOCS}/content/en/_index.md" 2>/dev/null || true

  # Replace the docs section _index.md with main's version to prevent stale
  # menu entries (e.g. older tags may have menu: {main: ...} in front matter
  # that adds an unwanted "Docs" item to the top nav).
  cp "${DOCS_DIR}/content/en/docs/_index.md" "${TEMP_DOCS}/content/en/docs/_index.md" 2>/dev/null || true

  # Create a config overlay to mark this as an archived version
  VERSION_OUTPUT="${OUTPUT_DIR}${URL_PATH}"
  mkdir -p "${VERSION_OUTPUT}"

  # Generate the versions list for the override config
  # Use absolute URLs so version links work from any subdirectory
  VERSIONS_TOML=$(awk '
    /"version"/ { gsub(/[",]/, ""); ver=$2 }
    /"path"/ { gsub(/[",]/, ""); path=$2; print "[[params.versions]]"; print "  version = \"" ver "\""; print "  url = \"" path "\""; print "" }
  ' "${VERSIONS_FILE}")

  # Extract version params (version_go, version_kube, etc.) from the tagged config
  # so install instructions render with the correct values for that release
  TAGGED_CONFIG=$(git -C "${REPO_ROOT}" show "${TAG}:docs/config.toml" 2>/dev/null || true)
  VERSION_PARAMS=""
  if [[ -n "${TAGGED_CONFIG}" ]]; then
    # Pull dependency versions but NOT latestTag (we set that from the resolved tag)
    VERSION_PARAMS=$(echo "${TAGGED_CONFIG}" | grep -E '^(version_docker|version_go|version_git|version_kind|version_kube|version_kpt)\s*=' || true)
  fi

  cat > "${TEMP_DOCS}/config-version-override.toml" <<EOF
[params]
archived_version = true
version = "${VERSION}"
url_latest_version = "/"
version_menu = "Releases"
latestTag = "${TAG#v}"
${VERSION_PARAMS}

${VERSIONS_TOML}
EOF

  (
    cd "${TEMP_DOCS}"
    hugo --gc --minify \
      --environment "${HUGO_ENV}" \
      --destination "${VERSION_OUTPUT}" \
      -b "${URL_PATH}" \
      --config config.toml,config-version-override.toml
  )

  echo "    Done: ${VERSION} (${TAG}) -> ${URL_PATH}"
  echo ""

done <<< "${TAGGED_VERSIONS}"

echo "==> All versions built successfully."
echo "    Output directory: ${OUTPUT_DIR}"
ls -la "${OUTPUT_DIR}"
