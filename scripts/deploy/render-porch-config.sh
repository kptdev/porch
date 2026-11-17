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

# Renders the PorchConfig resource in a deployment package directory into the
# porch-config ConfigMap that is already in the same directory: data["porch.yaml"]
# becomes the spec of the PorchConfig as a YAML document.
#
# Usage: render-porch-config.sh PACKAGE_DIR

set -e
set -u
set -o pipefail

source "$(dirname "$0")/../lib/common.sh"

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 PACKAGE_DIR" >&2
  exit 1
fi

kpt fn eval "${1}" \
  --image "${STARLARK}" \
  -- 'source=
def scalar(v):
  if type(v) == "bool":
    return "true" if v else "false"
  if type(v) == "int" or type(v) == "float":
    return str(v)
  return "\"" + str(v).replace("\\", "\\\\").replace("\"", "\\\"") + "\""

def emit(v, indent):
  lines = []
  for k in sorted(v.keys()):
    x = v[k]
    if type(x) == "dict":
      lines.append(indent + k + ":")
      lines += emit(x, indent + "  ")
    elif type(x) == "list":
      lines.append(indent + k + ":")
      for e in x:
        lines.append(indent + "  - " + scalar(e))
    else:
      lines.append(indent + k + ": " + scalar(x))
  return lines

spec = None
for r in ctx.resource_list["items"]:
  if r["kind"] == "PorchConfig":
    spec = r["spec"]
for r in ctx.resource_list["items"]:
  if r["kind"] == "ConfigMap" and r["metadata"]["name"] == "porch-config":
    r["data"]["porch.yaml"] = "\n".join(emit(spec, "")) + "\n"
'
