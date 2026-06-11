#!/bin/bash

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

# This script passes stdin through to stdout unchanged and writes multi-line
# output to stderr. Used to test that EvaluateFunctionResponse.Log preserves
# the raw multi-line stderr content.

input=$(cat)
printf "%s" "$input"

echo "Starting mutation" >&2
echo "Replacing value" >&2
echo "Completed" >&2
