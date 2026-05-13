#!/usr/bin/env bash
# Copyright 2022-2024 The kpt Authors
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

function_runner_ip="${1:-172.18.255.202}"
self_dir="$(dirname "$(readlink -f "$0")")"
deployment_config_dir="${DEPLOYPORCHCONFIGDIR:-$(readlink -f "${self_dir}/../.build/deploy")}"

cd "${deployment_config_dir}"

# expose function-runner to local processes
kpt fn eval \
  --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
  --match-kind Service \
  --match-name function-runner \
  --match-namespace porch-system \
  -- "ip=${function_runner_ip}"  'source=
ip = ctx.resource_list["functionConfig"]["data"]["ip"]
for resource in ctx.resource_list["items"]:
  resource["metadata"].setdefault("annotations", {})["metallb.universe.tf/loadBalancerIPs"] = ip
  resource["spec"]["type"] = "LoadBalancer"
  resource["spec"]["ports"][0]["nodePort"] = 30001'

# remove porch-controllers Deployment from package
kpt fn eval \
  --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
  --match-kind Deployment \
  --match-name porch-controllers \
  --match-namespace porch-system \
  -- 'source=ctx.resource_list["items"] = []'
