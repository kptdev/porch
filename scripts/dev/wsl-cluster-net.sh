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
#
# ----------------------------------------------------------------------------
# wsl-cluster-net.sh
#
# Make a locally-run (e.g. VS Code debugger) porch-server able to reach the KRM
# function pods/services it creates inside a KinD cluster, from the WSL/Linux
# host.
#
# porch-server's in-process pod evaluator creates per-function Pods + ClusterIP
# Services in the function namespace and then dials them at their in-cluster DNS
# name:
#
#     <service>.<fn-namespace>.svc.cluster.local:9446
#
# For that to work from a host OUTSIDE the cluster, two things are needed:
#
#   1. Routing  - the Service CIDR (ClusterIPs) and Pod CIDR must route to the
#                 KinD node, which sits on the "kind" docker bridge that the
#                 host can already reach.
#   2. DNS      - *.svc.cluster.local must resolve via the in-cluster CoreDNS.
#                 This is wired into systemd-resolved as a per-link routing
#                 domain, and glibc is pointed at resolved via libnss-resolve.
#
# Everything is auto-discovered from the live cluster, so the script is generic
# across KinD clusters (any node IP, service CIDR, pod CIDR, CoreDNS IP, docker
# bridge). Settings are runtime-only and are reapplied by re-running the script
# (e.g. after `wsl --shutdown` or a KinD recreate).
#
# ---------------------------------------------------------------------------
# ONE-TIME HOST SETUP (prerequisites)
#
# The script programs routes and DNS at runtime but does NOT install packages
# or edit system files. The following must be set up once on the WSL distro.
# On Ubuntu 24.04/26.04 systemd-resolved is NOT installed by default, which is
# the usual cause of "Unit dbus-org.freedesktop.resolve1.service not found".
#
#   1. Enable systemd (so systemd-resolved can run). In /etc/wsl.conf:
#          [boot]
#          systemd=true
#      Then from Windows PowerShell: `wsl --shutdown` and reopen the distro.
#
#   2. Install systemd-resolved + the NSS plug-in and enable the service:
#          sudo apt-get update
#          sudo apt-get install -y systemd-resolved libnss-resolve
#          sudo systemctl enable --now systemd-resolved
#
#   3. Make glibc/Go use systemd-resolved for name resolution. Ensure the
#      'hosts:' line in /etc/nsswitch.conf includes 'resolve':
#          sudo sed -i 's/^hosts:.*/hosts:          files resolve [!UNAVAIL=return] dns/' /etc/nsswitch.conf
#
#   4. Point /etc/resolv.conf at the systemd-resolved stub and stop WSL from
#      regenerating it. In /etc/wsl.conf add:
#          [network]
#          generateResolvConf = false
#      Then:
#          sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
#          sudo systemctl restart systemd-resolved
#
# `check_prereqs` re-states the relevant fix if any of these are missing when
# you run `apply`, and `apply` makes no changes until they are satisfied.
# ---------------------------------------------------------------------------
#
# Usage:
#   scripts/dev/wsl-cluster-net.sh            # discover + apply (default)
#   scripts/dev/wsl-cluster-net.sh apply      # same as above
#   scripts/dev/wsl-cluster-net.sh status     # show what is configured now
#   scripts/dev/wsl-cluster-net.sh verify     # resolve+dial a function service
#   scripts/dev/wsl-cluster-net.sh down       # remove routes + DNS link config
#
# Environment overrides (all optional; auto-discovered when unset):
#   KIND_NETWORK     docker network name of the cluster   (default: kind)
#   FN_NAMESPACE     namespace where function pods run    (default: porch-fn-system)
#   NODE_IP          KinD node IP to route via
#   SERVICE_CIDR     service ClusterIP CIDR
#   POD_CIDR         pod CIDR
#   COREDNS_IP       in-cluster DNS ClusterIP
#   BRIDGE_IF        host docker-bridge interface for the kind network
#   WRAPPER_PORT     function wrapper-server port, for verify only (default: 9446)
# ----------------------------------------------------------------------------

set -euo pipefail

KIND_NETWORK="${KIND_NETWORK:-kind}"
FN_NAMESPACE="${FN_NAMESPACE:-porch-fn-system}"
WRAPPER_PORT="${WRAPPER_PORT:-9446}"
CLUSTER_DOMAIN="${CLUSTER_DOMAIN:-cluster.local}"

log()  { echo "[wsl-cluster-net] $*"; }
err()  { echo "[wsl-cluster-net] ERROR: $*" >&2; }
die()  { err "$*"; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }

# --- discovery helpers -------------------------------------------------------

discover_node_ip() {
  [[ -n "${NODE_IP:-}" ]] && { echo "$NODE_IP"; return; }
  kubectl get nodes \
    -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null
}

discover_coredns_ip() {
  [[ -n "${COREDNS_IP:-}" ]] && { echo "$COREDNS_IP"; return; }
  # kube-dns is the stable Service name for CoreDNS.
  kubectl -n kube-system get svc kube-dns \
    -o jsonpath='{.spec.clusterIP}' 2>/dev/null
}

# Service CIDR: prefer kubeadm-config, then apiserver flags, then infer from the
# kubernetes service ClusterIP.
discover_service_cidr() {
  [[ -n "${SERVICE_CIDR:-}" ]] && { echo "$SERVICE_CIDR"; return; }
  local cidr
  cidr=$(kubectl -n kube-system get cm kubeadm-config \
      -o jsonpath='{.data.ClusterConfiguration}' 2>/dev/null \
      | sed -n 's/.*serviceSubnet:[[:space:]]*\([0-9./]*\).*/\1/p' | head -1)
  [[ -n "$cidr" ]] && { echo "$cidr"; return; }

  cidr=$(kubectl -n kube-system get pod -l component=kube-apiserver \
      -o jsonpath='{.items[0].spec.containers[0].command}' 2>/dev/null \
      | tr ',' '\n' | sed -n 's/.*service-cluster-ip-range=\([0-9./]*\).*/\1/p' | head -1)
  [[ -n "$cidr" ]] && { echo "$cidr"; return; }

  # Last resort: derive a /16 from the kubernetes service ClusterIP.
  local svcip
  svcip=$(kubectl get svc kubernetes -n default -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
  [[ -n "$svcip" ]] && echo "$(echo "$svcip" | cut -d. -f1-2).0.0/16"
}

# Pod CIDR: prefer kubeadm-config (cluster-wide), fall back to node .spec.podCIDR.
discover_pod_cidr() {
  [[ -n "${POD_CIDR:-}" ]] && { echo "$POD_CIDR"; return; }
  local cidr
  cidr=$(kubectl -n kube-system get cm kubeadm-config \
      -o jsonpath='{.data.ClusterConfiguration}' 2>/dev/null \
      | sed -n 's/.*podSubnet:[[:space:]]*\([0-9./]*\).*/\1/p' | head -1)
  [[ -n "$cidr" ]] && { echo "$cidr"; return; }

  kubectl get node -o jsonpath='{.items[0].spec.podCIDR}' 2>/dev/null
}

# Host interface for the kind docker network: br-<first 12 chars of network id>.
discover_bridge_if() {
  [[ -n "${BRIDGE_IF:-}" ]] && { echo "$BRIDGE_IF"; return; }
  local netid gw iface
  netid=$(docker network inspect "$KIND_NETWORK" -f '{{.Id}}' 2>/dev/null | cut -c1-12)
  if [[ -n "$netid" ]] && ip -o link show "br-${netid}" >/dev/null 2>&1; then
    echo "br-${netid}"
    return
  fi
  # Fall back: find the host bridge that owns the network's IPv4 gateway.
  gw=$(docker network inspect "$KIND_NETWORK" \
        -f '{{range .IPAM.Config}}{{if .Gateway}}{{.Gateway}} {{end}}{{end}}' 2>/dev/null \
        | tr ' ' '\n' | grep -E '^[0-9]+\.' | head -1)
  [[ -z "$gw" ]] && return
  iface=$(ip -o -4 addr show | awk -v g="${gw}/" '$4 ~ ("^" g) {print $2; exit}')
  echo "$iface"
}

# --- resolve all values once -------------------------------------------------

resolve_all() {
  need kubectl; need docker; need ip; need resolvectl
  NODE_IP="$(discover_node_ip)"
  COREDNS_IP="$(discover_coredns_ip)"
  SERVICE_CIDR="$(discover_service_cidr)"
  POD_CIDR="$(discover_pod_cidr)"
  BRIDGE_IF="$(discover_bridge_if)"

  [[ -n "$NODE_IP"      ]] || die "could not discover KinD node IP (is the cluster reachable via kubectl?)"
  [[ -n "$COREDNS_IP"   ]] || die "could not discover CoreDNS (kube-dns) ClusterIP"
  [[ -n "$SERVICE_CIDR" ]] || die "could not discover service CIDR"
  [[ -n "$POD_CIDR"     ]] || die "could not discover pod CIDR"
  [[ -n "$BRIDGE_IF"    ]] || die "could not discover host bridge interface for docker network '$KIND_NETWORK'"
}

print_discovered() {
  log "docker network : $KIND_NETWORK"
  log "node IP        : $NODE_IP"
  log "service CIDR   : $SERVICE_CIDR"
  log "pod CIDR       : $POD_CIDR"
  log "CoreDNS IP     : $COREDNS_IP"
  log "bridge iface   : $BRIDGE_IF"
  log "fn namespace   : $FN_NAMESPACE"
}

# --- prerequisite checks -----------------------------------------------------

# Verify systemd-resolved + libnss-resolve + nsswitch are set up for split DNS.
# Returns non-zero and prints exact remediation steps if anything is missing.
# On Ubuntu 24.04/26.04 systemd-resolved is NOT installed by default, which is
# the usual cause of "Unit dbus-org.freedesktop.resolve1.service not found".
check_prereqs() {
  local ok=0

  if ! resolvectl status >/dev/null 2>&1; then
    err "systemd-resolved is not running/reachable (resolvectl cannot talk to it)."
    err "On Ubuntu 24.04+/26.04 it is not installed by default. Fix:"
    err "  sudo apt-get update && sudo apt-get install -y systemd-resolved libnss-resolve"
    err "  sudo systemctl enable --now systemd-resolved"
    err "  (ensure /etc/wsl.conf has [boot] systemd=true, then 'wsl --shutdown')"
    ok=1
  fi

  if ! ls /usr/lib/*/libnss_resolve.so.2 >/dev/null 2>&1; then
    err "libnss-resolve is not installed; glibc (and Go) will not use resolved's"
    err "per-domain routing, so *.svc.cluster.local may fail to resolve. Fix:"
    err "  sudo apt-get install -y libnss-resolve"
    ok=1
  fi

  if ! grep -qE '^hosts:.*\bresolve\b' /etc/nsswitch.conf 2>/dev/null; then
    err "/etc/nsswitch.conf 'hosts:' line does not include 'resolve'. Fix:"
    err "  sudo sed -i 's/^hosts:.*/hosts:          files resolve [!UNAVAIL=return] dns/' /etc/nsswitch.conf"
    ok=1
  fi

  return $ok
}

# --- actions -----------------------------------------------------------------

do_apply() {
  resolve_all
  print_discovered
  check_prereqs || die "prerequisites not met; nothing applied (see messages above)"
  log "adding routes via node $NODE_IP"
  sudo ip route replace "$POD_CIDR"     via "$NODE_IP"
  sudo ip route replace "$SERVICE_CIDR" via "$NODE_IP"
  log "binding CoreDNS $COREDNS_IP + ~$CLUSTER_DOMAIN routing domain to $BRIDGE_IF"
  sudo resolvectl dns    "$BRIDGE_IF" "$COREDNS_IP"
  sudo resolvectl domain "$BRIDGE_IF" "~${CLUSTER_DOMAIN}" "~svc.${CLUSTER_DOMAIN}"
  log "done. run '$0 verify' to test."
}

# Best-effort discovery for teardown: never fails, never exits. Populates
# whatever can be determined (honoring env overrides); leaves the rest empty.
# Teardown must work even when the cluster/API is gone, so unlike resolve_all
# this does not 'die' on missing values.
resolve_all_soft() {
  NODE_IP="${NODE_IP:-$(discover_node_ip 2>/dev/null || true)}"
  COREDNS_IP="${COREDNS_IP:-$(discover_coredns_ip 2>/dev/null || true)}"
  SERVICE_CIDR="${SERVICE_CIDR:-$(discover_service_cidr 2>/dev/null || true)}"
  POD_CIDR="${POD_CIDR:-$(discover_pod_cidr 2>/dev/null || true)}"
  BRIDGE_IF="${BRIDGE_IF:-$(discover_bridge_if 2>/dev/null || true)}"
}

do_down() {
  resolve_all_soft

  # Routes: delete by CIDR. Try the node-specific form first, then a plain
  # delete so stale routes are removed even if the node IP is now unknown.
  for cidr in "${POD_CIDR:-}" "${SERVICE_CIDR:-}"; do
    [[ -z "$cidr" ]] && continue
    if [[ -n "${NODE_IP:-}" ]]; then
      sudo ip route del "$cidr" via "$NODE_IP" 2>/dev/null || true
    fi
    sudo ip route del "$cidr" 2>/dev/null || true
  done

  # DNS: revert the kind bridge link. If the bridge is unknown (cluster gone),
  # there is nothing resolved-side left to revert for it.
  if [[ -n "${BRIDGE_IF:-}" ]]; then
    sudo resolvectl revert "$BRIDGE_IF" 2>/dev/null || true
    log "removed routes and reverted DNS on $BRIDGE_IF"
  else
    log "removed routes; bridge interface not found, skipped DNS revert"
  fi
}

do_status() {
  resolve_all
  print_discovered
  echo
  log "routes:"
  ip route | grep -E "$(echo "$POD_CIDR" | cut -d/ -f1 | cut -d. -f1-2)|$(echo "$SERVICE_CIDR" | cut -d/ -f1 | cut -d. -f1-2)" || echo "  (none)"
  echo
  log "resolved DNS on $BRIDGE_IF:"
  resolvectl status "$BRIDGE_IF" 2>/dev/null | grep -E "Current Scopes|DNS Servers|DNS Domain" || echo "  (none)"
}

do_verify() {
  resolve_all
  local svc fqdn ip
  svc=$(kubectl -n "$FN_NAMESPACE" get svc -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [[ -z "$svc" ]]; then
    log "no services in namespace '$FN_NAMESPACE' yet; testing CoreDNS Service instead"
    fqdn="kube-dns.kube-system.svc.${CLUSTER_DOMAIN}"
  else
    fqdn="${svc}.${FN_NAMESPACE}.svc.${CLUSTER_DOMAIN}"
  fi

  log "resolving $fqdn (via glibc/getent)"
  ip=$(getent hosts "$fqdn" | awk '{print $1}' | head -1 || true)
  [[ -n "$ip" ]] || die "DNS resolution FAILED for $fqdn (check libnss-resolve + nsswitch, see '$0 status')"
  log "resolved -> $ip"

  if [[ -n "$svc" ]]; then
    log "TCP dial ${ip}:${WRAPPER_PORT} (porch-server's function dial target)"
    if timeout 3 bash -c "cat < /dev/null > /dev/tcp/${ip}/${WRAPPER_PORT}" 2>/dev/null; then
      log "OK: ${fqdn}:${WRAPPER_PORT} reachable"
    else
      die "DIAL FAILED to ${ip}:${WRAPPER_PORT} (routing issue; see '$0 status')"
    fi
  fi
  log "verify passed."
}

# Is this environment one where the script can/should run? Used by the soft
# 'apply-if-applicable' mode so callers (e.g. make targets, CI) can invoke it
# unconditionally without failing on non-WSL / non-KinD / non-systemd setups.
applicable() {
  # Only meaningful on WSL2 (host outside the cluster network).
  grep -qiE 'microsoft|wsl' /proc/version 2>/dev/null || return 1
  # Required tooling.
  command -v kubectl    >/dev/null 2>&1 || return 1
  command -v docker     >/dev/null 2>&1 || return 1
  command -v ip         >/dev/null 2>&1 || return 1
  command -v resolvectl >/dev/null 2>&1 || return 1
  # The KinD docker network must exist.
  docker network inspect "$KIND_NETWORK" >/dev/null 2>&1 || return 1
  # kube API must be reachable.
  kubectl version --request-timeout=3s >/dev/null 2>&1 || return 1
  return 0
}

# Soft apply: quiet on success, never fails the caller. Safe to call from build
# tooling on any platform; no-op when not on WSL+KinD. Only prints on problems.
do_apply_if_applicable() {
  applicable || exit 0
  if err_msg=$(do_apply 2>&1); then
    log "applied routes + cluster DNS"
  else
    log "WARN: could not apply cluster networking (continuing):"
    echo "$err_msg" | sed 's/^/[wsl-cluster-net]   /' >&2
  fi
  exit 0
}

case "${1:-apply}" in
  apply|"")           do_apply ;;
  apply-if-applicable) do_apply_if_applicable ;;
  down)               do_down ;;
  status)             do_status ;;
  verify)             do_verify ;;
  -h|--help|help)
    sed -n '77,92p' "$0" ;;
  *) die "unknown command: $1 (use: apply | apply-if-applicable | down | status | verify)" ;;
esac
