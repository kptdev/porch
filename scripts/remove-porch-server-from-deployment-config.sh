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
self_dir="$(dirname "$(readlink -f "$0")")"

# function_runner_ip should match the --function-runner argument given to porch-server
# (also, the IP should be from the address range specified in deployments/local/metallb-conf.yaml)
function_runner_ip="172.18.255.201"  
git_root="$(readlink -f "${self_dir}/..")"
deployment_config_dir="${DEPLOYPORCHCONFIGDIR:-${git_root}/.build/deploy)}"
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

# remove porch-server Deployment from package
kpt fn eval \
  --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
  --match-kind Deployment \
  --match-name porch-server \
  --match-namespace porch-system \
  -- 'source=ctx.resource_list["items"] = []'

# make the api service point to the local porch-server
if [[ "$(uname)" == "Darwin" || -n "${DOCKER_HOST+x}" ]] || docker info 2>/dev/null | grep -q "Docker Desktop";
then
  echo "--- Docker Desktop detected. ---"
  kpt fn eval \
    --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
    --match-kind Service \
    --match-name api \
    --match-namespace porch-system \
    -- 'source=
for resource in ctx.resource_list["items"]:
  resource["spec"] = {
    "type": "ExternalName",
    "externalName": "host.docker.internal"
  }
'
  kpt fn eval \
    --image ghcr.io/kptdev/krm-functions-catalog/search-replace:v0.2.3 \
    --match-kind APIService \
    --match-name v1alpha1.porch.kpt.dev \
    -- 'by-path=spec.service.port' "put-value=4443"
else
  echo "--- Local Docker daemon detected. ---"
  docker_bridge_ip="$(docker network inspect bridge --format='{{(index .IPAM.Config 0).Gateway}}')"
  kpt fn eval \
    --image upsert-resource:v0.2.0 \
    --fn-config "${git_root}/deployments/local/porch-api-endpoints.yaml"
  kpt fn eval \
    --image ghcr.io/kptdev/krm-functions-catalog/search-replace:v0.2.3 \
    --match-kind Endpoints \
    --match-name api \
    --match-namespace porch-system \
    -- 'by-path=subsets[0].addresses[0].ip' "put-value=$docker_bridge_ip"
  kpt fn eval \
    --image ghcr.io/kptdev/krm-functions-catalog/starlark:v0.5.5 \
    --match-kind Service \
    --match-name api \
    --match-namespace porch-system \
    -- 'source=
for resource in ctx.resource_list["items"]:
  resource["spec"].pop("selector")'

fi
