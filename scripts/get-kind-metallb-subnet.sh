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

# Utility script to dynamically determine the MetalLB IP range from the kind Docker network.
# This removes the need to hardcode 172.18.255.x addresses in scripts and configs.
#
# Usage:
#   source scripts/get-kind-metallb-subnet.sh
#   # Now METALLB_IP_RANGE_START and METALLB_IP_RANGE_END are set
#
# Or call individual functions:
#   get_metallb_ip_range       -> sets METALLB_IP_RANGE_START and METALLB_IP_RANGE_END
#   get_service_lb_ip <svc> <ns> -> prints the LoadBalancer IP of a service
#   wait_for_service_lb_ip <svc> <ns> [timeout] -> waits for and prints the LB IP

# Only set strict mode when executed directly, not when sourced.
# This avoids changing the caller's shell options unexpectedly.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  set -euo pipefail
fi

# Get the subnet of the kind Docker network and derive a MetalLB-suitable IP range.
# The range is placed at the top of the subnet's last octet range (x.x.255.200 - x.x.255.250)
# to avoid conflicts with container IPs assigned by Docker.
#
# Sets: METALLB_IP_RANGE_START, METALLB_IP_RANGE_END
get_metallb_ip_range() {
  local kind_network="${KIND_DOCKER_NETWORK:-kind}"

  # Get the IPv4 subnet from the Docker network (e.g. "172.18.0.0/16")
  # Docker networks can have both IPv4 and IPv6 configs, so we iterate over all
  # IPAM configs and pick the one that looks like an IPv4 CIDR.
  local subnet
  subnet="$(docker network inspect "$kind_network" \
    --format='{{range .IPAM.Config}}{{.Subnet}} {{end}}' \
    | tr ' ' '\n' \
    | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/' \
    | head -1)" \
    || { echo "ERROR: Could not inspect Docker network '$kind_network'. Is kind running?" >&2; return 1; }

  if [[ -z "$subnet" ]]; then
    echo "ERROR: No IPv4 subnet found on Docker network '$kind_network'." >&2
    return 1
  fi

  # Parse the subnet base address and CIDR mask to compute a valid range.
  # We place the MetalLB pool at the high end of the subnet to avoid conflicts
  # with container IPs assigned by Docker.
  local base_ip cidr_mask
  base_ip="${subnet%%/*}"
  cidr_mask="${subnet##*/}"

  # Convert base IP to integer for arithmetic
  local IFS='.'
  # shellcheck disable=SC2086
  set -- $base_ip
  local ip_int=$(( ($1 << 24) + ($2 << 16) + ($3 << 8) + $4 ))
  IFS=' '

  # Calculate the number of host addresses in the subnet
  local host_bits=$(( 32 - cidr_mask ))
  local subnet_size=$(( 1 << host_bits ))

  # Place the pool near the top of the subnet: last 51 addresses before broadcast
  # (broadcast = base + subnet_size - 1, so we use base + subnet_size - 52 to base + subnet_size - 2)
  local start_int=$(( ip_int + subnet_size - 52 ))
  local end_int=$(( ip_int + subnet_size - 2 ))

  # Convert integers back to dotted-quad
  METALLB_IP_RANGE_START="$(( (start_int >> 24) & 255 )).$(( (start_int >> 16) & 255 )).$(( (start_int >> 8) & 255 )).$(( start_int & 255 ))"
  METALLB_IP_RANGE_END="$(( (end_int >> 24) & 255 )).$(( (end_int >> 16) & 255 )).$(( (end_int >> 8) & 255 )).$(( end_int & 255 ))"

  export METALLB_IP_RANGE_START
  export METALLB_IP_RANGE_END
}

# Generate a MetalLB IPAddressPool + L2Advertisement YAML config using the dynamic IP range.
# Prints the YAML to stdout.
generate_metallb_config() {
  get_metallb_ip_range

  cat <<EOF
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: example
  namespace: metallb-system
spec:
  addresses:
  - ${METALLB_IP_RANGE_START}-${METALLB_IP_RANGE_END}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: empty
  namespace: metallb-system
EOF
}

# Get the LoadBalancer IP of a Kubernetes service.
# Args: <service-name> <namespace>
# Prints the IP to stdout. Returns 1 if not available.
get_service_lb_ip() {
  local svc_name="$1"
  local namespace="$2"

  local ip
  ip="$(kubectl get svc "$svc_name" -n "$namespace" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null)"

  if [[ -z "$ip" || "$ip" == "null" ]]; then
    return 1
  fi

  echo "$ip"
}

# Wait for a LoadBalancer service to get an external IP assigned.
# Args: <service-name> <namespace> [timeout-seconds]
# Prints the IP to stdout once available.
wait_for_service_lb_ip() {
  local svc_name="$1"
  local namespace="$2"
  local timeout="${3:-60}"

  local elapsed=0
  local ip=""

  while [[ $elapsed -lt $timeout ]]; do
    ip="$(get_service_lb_ip "$svc_name" "$namespace" 2>/dev/null)" && break
    sleep 2
    elapsed=$((elapsed + 2))
  done

  if [[ -z "$ip" ]]; then
    echo "ERROR: Timed out waiting for LoadBalancer IP on $namespace/$svc_name after ${timeout}s" >&2
    return 1
  fi

  echo "$ip"
}

# Get the function-runner LoadBalancer IP.
# Falls back to waiting if not yet assigned.
get_function_runner_ip() {
  wait_for_service_lb_ip "function-runner" "porch-system" "${1:-60}"
}

# Get the Gitea LoadBalancer IP.
# Falls back to waiting if not yet assigned.
get_gitea_ip() {
  wait_for_service_lb_ip "gitea-lb" "gitea" "${1:-60}"
}
