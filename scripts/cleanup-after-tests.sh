#!/usr/bin/env bash

#  Copyright 2025 The kpt Authors
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

# Stricter error handling
set -e # Exit on error
set -u # Must predefine variables
set -o pipefail # Check errors in piped commands
self_dir="$(dirname "$(readlink -f "$0")")"

rm -rf "$self_dir/../.cache"

kubectl get packagerev -A --no-headers | awk '{ print "-n", $1, $2 }' | xargs -L 1 --no-run-if-empty kubectl patch packagerev --type='json' -p='[{"op": "remove", "path": "/metadata/finalizers"}]'

kubectl get ns -o name --no-headers | egrep '(test|rpkg-|repo-)' | xargs -L 1 --no-run-if-empty kubectl delete
