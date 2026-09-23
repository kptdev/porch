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

set -o errexit
set -o nounset
set -o pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

cd "${repo_root}"

# Get the k8s.io/apiserver version from the go.mod.
version=$(grep "k8s.io/apiserver" go.mod | sed -n 's/.*k8s.io\/apiserver v\(.*\)/\1/p' | grep -v '=>')

# Download the k8s.io/apiserver to the third_party.

if [ -d "third_party/k8s.io/apiserver-v${version}" ]; then
    rm -rf "third_party/k8s.io/apiserver-v${version}"
fi

mkdir -p "third_party/k8s.io"
cd "third_party/k8s.io"

git clone --depth=1 --branch="v${version}" https://github.com/kubernetes/apiserver.git "apiserver-v${version}" -q 2> /dev/null
rm -rf "apiserver-v${version}/.git"
rm -rf "apiserver-v${version}/.github"
find "apiserver-v${version}" -name OWNERS -type f -delete # This needs to be removed so it doesn't confuse prow with invalid owners from the k8s project
cd "../.."

# Re-apply the local patches that fork the vendored apiserver from upstream.
# These live in scripts/util/patches and are temporary legacy debt that should
# be removed together with the migration away from the aggregated apiserver.
# See scripts/util/patches/README.md for details.
patch_dir="${repo_root}/scripts/util/patches"
vendor_dir="third_party/k8s.io/apiserver-v${version}"
if [ -d "${patch_dir}" ]; then
    for patch in "${patch_dir}"/*.patch; do
        [ -e "${patch}" ] || continue
        echo "Applying patch $(basename "${patch}") to ${vendor_dir}"
        if ! git apply --directory="${vendor_dir}" --check "${patch}"; then
            echo "ERROR: patch $(basename "${patch}") does not apply to apiserver v${version}." >&2
            echo "Upstream likely changed the patched code. Refresh the patch against the new" >&2
            echo "upstream source (or drop it if it is now obsolete), then re-run this script." >&2
            exit 1
        fi
        git apply --directory="${vendor_dir}" "${patch}"
    done
fi

echo "Vendored apiserver v${version} into ${vendor_dir}"
echo "Applied local patches from scripts/util/patches."
echo "Add the following to go.mod: \"replace k8s.io/apiserver v${version} => ./third_party/k8s.io/apiserver-v${version}\"" 