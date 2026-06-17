#!/usr/bin/env bash
# Copyright 2026 The kpt Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
METRICS_DIR="${SCRIPT_DIR}/../deployments/metrics"
DOT_ENV_PATH="${SCRIPT_DIR}/../.env"
PORT_FORWARD_DIR="$(mktemp --directory --suffix "_porch-monitoring-pf.pid.d")"

if [[ -f "$DOT_ENV_PATH" ]]; then
    source "$DOT_ENV_PATH"
fi

# Configuration
NAMESPACE="${NAMESPACE:-porch-monitoring}"
PROMETHEUS_LOCAL_PORT="${PROMETHEUS_LOCAL_PORT:-9092}"
PROMETHEUS_CONTAINER_PORT="${PROMETHEUS_CONTAINER_PORT:-9090}"
PROMETHEUS_NODEPORT="${PROMETHEUS_NODEPORT:-30091}"
GRAFANA_LOCAL_PORT="${GRAFANA_LOCAL_PORT:-3001}"
GRAFANA_CONTAINER_PORT="${GRAFANA_CONTAINER_PORT:-3000}"
GRAFANA_NODEPORT="${GRAFANA_NODEPORT:-30301}"

GRAFANA_ADMIN_USER="${GRAFANA_ADMIN_USER:-porch}"
GRAFANA_ADMIN_PW="${GRAFANA_ADMIN_PW:-}"
[[ -z $GRAFANA_ADMIN_PW ]] && GRAFANA_ADMIN_PW="$(date +%s | shasum -a 256 | base64 | head -c 15)"

DOCKERHUB_MIRROR="${DOCKERHUB_MIRROR:-docker.io}"
KRM_FN_REGISTRY_URL="${KRM_FN_REGISTRY_URL:-ghcr.io/kptdev/krm-functions-catalog}"
PROMETHEUS_VERSION="${PROMETHEUS_VERSION:-latest}"
PROMETHEUS_IMAGE="${DOCKERHUB_MIRROR}/prom/prometheus:${PROMETHEUS_VERSION}"
GRAFANA_VERSION="${GRAFANA_VERSION:-latest}"
GRAFANA_IMAGE="${DOCKERHUB_MIRROR}/grafana/grafana:${GRAFANA_VERSION}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}
log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}
log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

if ! command -v kubectl &> /dev/null; then
    log_error "kubectl not found. Please install kubectl first."
    exit 1
fi

check_kpt() {
    if ! command -v kpt &> /dev/null; then
        log_error "kpt not found. Please install kpt from: https://kpt.dev/installation/"
        exit 1
    fi
}

prepare_manifests() {
    local temp_dir=$(mktemp -d)
    cp -r "${METRICS_DIR}"/* "$temp_dir/"

    cat > "$temp_dir/Kptfile" <<EOF
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: porch-monitoring
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: Prometheus and Grafana monitoring stack for Porch
pipeline:
  mutators:
    - image: ${KRM_FN_REGISTRY_URL}/apply-setters:v0.2.0
      configMap:
        prometheus-image: "${PROMETHEUS_IMAGE}"
        grafana-image: "${GRAFANA_IMAGE}"
        prometheus-container-port: "${PROMETHEUS_CONTAINER_PORT}"
        grafana-container-port: "${GRAFANA_CONTAINER_PORT}"
        prometheus-nodeport: "${PROMETHEUS_NODEPORT}"
        grafana-nodeport: "${GRAFANA_NODEPORT}"
        grafana-user: "$(echo -n "$GRAFANA_ADMIN_USER" | base64)"
        grafana-pw: "$(echo -n "$GRAFANA_ADMIN_PW" | base64)"
    - image: ${KRM_FN_REGISTRY_URL}/set-namespace:v0.4.1
      configMap:
        namespace: ${NAMESPACE}
EOF

    kpt fn render "$temp_dir" #> /dev/null 2>&1

    echo "$temp_dir"
}

apply_manifests() {
    local manifests_dir=$1

    log_info "Applying manifests using kpt live apply..."
    if [ ! -f "$manifests_dir/resourcegroup.yaml" ]; then
        log_info "Initializing kpt inventory..."
        kpt live init "$manifests_dir" --namespace "$NAMESPACE" --name porch-monitoring
    fi

    log_info "Running kpt live apply..."
    kpt live apply "$manifests_dir" --reconcile-timeout=2m --output=events || {
        log_warn "kpt live apply reconcile timeout - resources are deployed but may still be starting up"
    }
}

create_namespace() {
    if kubectl get namespace "$NAMESPACE" &> /dev/null; then
        log_info "Namespace $NAMESPACE already exists"
    else
        log_info "Creating namespace $NAMESPACE"
        kubectl create namespace "$NAMESPACE"
    fi
}

deploy_monitoring() {
    log_info "Deploying monitoring stack..."
    log_info "Rendering manifests with kpt..."

    local manifests_dir
    manifests_dir=$(prepare_manifests)

    kubectl create configmap prometheus-config \
        --from-file="${SCRIPT_DIR}/../deployments/metrics-resources/prometheus-config.yaml" \
        -n "$NAMESPACE" \
        --dry-run=client -o yaml | kubectl apply -f -


    declare -a grafana_dashboards
    while read -r dashboard_file; do
        grafana_dashboards+=("--from-file=$(basename "$dashboard_file")=$dashboard_file")
    done < <(find "${SCRIPT_DIR}/../deployments/metrics-resources" -name "grafana*dashboard.json" -type f)

    kubectl create configmap grafana-dashboards \
        "${grafana_dashboards[@]}" \
        -n "$NAMESPACE" \
        --dry-run=client -o yaml | kubectl apply -f -

    apply_manifests "$manifests_dir"

    rm -rf "$manifests_dir"

    log_info "Monitoring stack deployed successfully"
}

wait_for_deployment() {
    local deployment=$1
    log_info "Waiting for $deployment to be ready..."
    kubectl wait --for=condition=available --timeout=300s deployment/"$deployment" -n "$NAMESPACE"
}

stop_port_forwards() {
    if find /tmp/tmp*_porch-monitoring-pf.pid.d/ -name '*.pid' -exec pkill -F '{}' \; 2>/dev/null; then
        find /tmp/tmp*_porch-monitoring-pf.pid.d/ -name '*.pid' ! -wholename "${PORT_FORWARD_DIR}*" -exec rm '{}' \; 2>/dev/null || true
        find /tmp/tmp*_porch-monitoring-pf.pid.d/ -type d ! -wholename "${PORT_FORWARD_DIR}*" -exec rmdir '{}' \; 2>/dev/null || true
    fi
}

get_service_urls() {
    log_info "Getting service URLs..."

    log_info "Setting up port forwarding..."
    stop_port_forwards
    sleep 2

    kubectl port-forward -n "${NAMESPACE}" deployment/prometheus "${PROMETHEUS_LOCAL_PORT}":"${PROMETHEUS_CONTAINER_PORT}" > /dev/null 2>&1 &
    PROMETHEUS_PF_PID=$!
    kubectl port-forward -n "${NAMESPACE}" deployment/grafana "${GRAFANA_LOCAL_PORT}":"${GRAFANA_CONTAINER_PORT}" > /dev/null 2>&1 &
    GRAFANA_PF_PID=$!

    sleep 2

    echo "${PROMETHEUS_PF_PID}" > "${PORT_FORWARD_DIR}"/porch-prometheus-pf.pid
    echo "${GRAFANA_PF_PID}" > "${PORT_FORWARD_DIR}"/porch-grafana-pf.pid

    PROMETHEUS_URL="http://localhost:${PROMETHEUS_LOCAL_PORT}"
    GRAFANA_URL="http://localhost:${GRAFANA_LOCAL_PORT}"

    echo ""
    log_info "=========================================="
    log_info "Services deployed successfully!"
    log_info "=========================================="
    echo ""
    log_info "Access via port-forward (recommended):"
    log_info "  Prometheus: ${PROMETHEUS_URL}"
    log_info "  Grafana:    ${GRAFANA_URL}"
    log_info "    Username: ${GRAFANA_ADMIN_USER}"
    log_info "    Password: ${GRAFANA_ADMIN_PW}"
    log_info "      stored in: kubectl -n porch-monitoring get secrets --selector app=grafana -o yaml"
    echo ""
    log_info "  - Prometheus is scraping metrics from:"
    log_info "      - porch-server port 9464"
    log_info "      - porch-controller port 9464"
    log_info "      - function-runner port 9464"
    log_info ""
    echo ""
    log_info "To stop port forwarding, run:"
    log_info "  find /tmp/tmp*_porch-monitoring-pf.pid.d/ -name '*.pid' -exec pkill -F '{}' \;"
    echo ""
}

cleanup() {
    log_warn "Cleaning up existing deployment..."

    log_info "Stopping port forwarding..."
    stop_port_forwards

    if kubectl get namespace "$NAMESPACE" &> /dev/null; then
        log_info "Deleting resources in namespace $NAMESPACE..."
        kubectl delete deployment prometheus grafana -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete service prometheus grafana -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete configmap prometheus-config grafana-dashboards grafana-dashboards-provider grafana-datasources -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete secret grafana-admin-creds -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete serviceaccount prometheus -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete clusterrole prometheus --ignore-not-found=true
        kubectl delete clusterrolebinding prometheus --ignore-not-found=true

        log_info "Deleting namespace $NAMESPACE..."
        kubectl delete namespace "$NAMESPACE" --ignore-not-found=true
    else
        log_info "Namespace $NAMESPACE does not exist, nothing to clean up"
    fi

    log_info "Cleanup completed"
}

main() {
    local action="${1:-deploy}"
    case "$action" in
        deploy)
            log_info "Starting deployment of Prometheus and Grafana..."
            check_kpt
            create_namespace
            deploy_monitoring
            wait_for_deployment prometheus
            wait_for_deployment grafana
            get_service_urls
            ;;
        cleanup)
            check_kpt
            cleanup
            ;;
        restart)
            check_kpt
            cleanup
            sleep 2
            main deploy
            ;;
        *)
            log_error "Unknown action: $action"
            echo "Usage: $0 {deploy|cleanup|restart}"
            echo ""
            echo "Environment variables:"
            echo "  NAMESPACE            - Kubernetes namespace (default: porch-monitoring)"
            echo "  PROMETHEUS_NODEPORT  - Prometheus NodePort (default: 30091)"
            echo "  GRAFANA_NODEPORT     - Grafana NodePort (default: 30301)"
            echo ""
            echo "Requirements:"
            echo "  - kpt CLI (install from: https://kpt.dev/installation/)"
            echo "  - kubectl configured with cluster access"
            exit 1
            ;;
    esac
}
main "$@"
