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
PYROSCOPE_LOCAL_PORT="${PYROSCOPE_LOCAL_PORT:-4040}"
PYROSCOPE_CONTAINER_PORT="${PYROSCOPE_CONTAINER_PORT:-4040}"
JAEGER_LOCAL_PORT="${JAEGER_LOCAL_PORT:-16686}"
JAEGER_OTLP_CONTAINER_PORT="${JAEGER_OTLP_CONTAINER_PORT:-4317}"
JAEGER_UI_CONTAINER_PORT="${JAEGER_UI_CONTAINER_PORT:-16686}"

GRAFANA_ADMIN_USER="${GRAFANA_ADMIN_USER:-porch}"
GRAFANA_ADMIN_PW="${GRAFANA_ADMIN_PW:-}"
[[ -z $GRAFANA_ADMIN_PW ]] && GRAFANA_ADMIN_PW="$(date +%s | shasum -a 256 | base64 | head -c 15)"

DOCKERHUB_MIRROR="${DOCKERHUB_MIRROR:-docker.io}"
KRM_FN_REGISTRY_URL="${KRM_FN_REGISTRY_URL:-ghcr.io/kptdev/krm-functions-catalog}"
PROMETHEUS_VERSION="${PROMETHEUS_VERSION:-latest}"
PROMETHEUS_IMAGE="${DOCKERHUB_MIRROR}/prom/prometheus:${PROMETHEUS_VERSION}"
GRAFANA_VERSION="${GRAFANA_VERSION:-latest}"
GRAFANA_IMAGE="${DOCKERHUB_MIRROR}/grafana/grafana:${GRAFANA_VERSION}"
PYROSCOPE_VERSION="${PYROSCOPE_VERSION:-latest}"
PYROSCOPE_IMAGE="${DOCKERHUB_MIRROR}/grafana/pyroscope:${PYROSCOPE_VERSION}"
JAEGER_VERSION="${JAEGER_VERSION:-latest}"
JAEGER_IMAGE="${DOCKERHUB_MIRROR}/jaegertracing/all-in-one:${JAEGER_VERSION}"
ALLOY_VERSION="${ALLOY_VERSION:-latest}"
ALLOY_IMAGE="${DOCKERHUB_MIRROR}/grafana/alloy:${ALLOY_VERSION}"
POSTGRES_EXPORTER_VERSION="${POSTGRES_EXPORTER_VERSION:-v0.16.0}"
POSTGRES_EXPORTER_IMAGE="${DOCKERHUB_MIRROR}/prometheuscommunity/postgres-exporter:${POSTGRES_EXPORTER_VERSION}"
POSTGRES_EXPORTER_CONTAINER_PORT="${POSTGRES_EXPORTER_CONTAINER_PORT:-9187}"
POSTGRES_DB_USER="${POSTGRES_DB_USER:-porch}"
POSTGRES_DB_PASSWORD="${POSTGRES_DB_PASSWORD:-porch}"
POSTGRES_EXPORTER_DATA_SOURCE_URI="${POSTGRES_EXPORTER_DATA_SOURCE_URI:-porch-postgresql.porch-system.svc.cluster.local:5432/porch?sslmode=disable}"

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
  description: Prometheus, Grafana, Jaeger, and profiling stack for Porch
pipeline:
  mutators:
    - image: ${KRM_FN_REGISTRY_URL}/apply-setters:v0.2.0
      configMap:
        prometheus-image: "${PROMETHEUS_IMAGE}"
        grafana-image: "${GRAFANA_IMAGE}"
        pyroscope-image: "${PYROSCOPE_IMAGE}"
        jaeger-image: "${JAEGER_IMAGE}"
        alloy-image: "${ALLOY_IMAGE}"
        postgres-exporter-image: "${POSTGRES_EXPORTER_IMAGE}"
        prometheus-container-port: "${PROMETHEUS_CONTAINER_PORT}"
        grafana-container-port: "${GRAFANA_CONTAINER_PORT}"
        jaeger-otlp-container-port: "${JAEGER_OTLP_CONTAINER_PORT}"
        jaeger-ui-container-port: "${JAEGER_UI_CONTAINER_PORT}"
        postgres-exporter-container-port: "${POSTGRES_EXPORTER_CONTAINER_PORT}"
        prometheus-nodeport: "${PROMETHEUS_NODEPORT}"
        grafana-nodeport: "${GRAFANA_NODEPORT}"
        grafana-user: "$(echo -n "$GRAFANA_ADMIN_USER" | base64)"
        grafana-pw: "$(echo -n "$GRAFANA_ADMIN_PW" | base64)"
        postgres-exporter-db-user: "$(echo -n "$POSTGRES_DB_USER" | base64)"
        postgres-exporter-db-password: "$(echo -n "$POSTGRES_DB_PASSWORD" | base64)"
        postgres-exporter-data-source-uri: "${POSTGRES_EXPORTER_DATA_SOURCE_URI}"
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

    kubectl create configmap alloy-config \
        --from-file=config.alloy="${SCRIPT_DIR}/../deployments/metrics-resources/alloy-config.alloy" \
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
    kubectl port-forward -n "${NAMESPACE}" deployment/pyroscope "${PYROSCOPE_LOCAL_PORT}":"${PYROSCOPE_CONTAINER_PORT}" > /dev/null 2>&1 &
    PYROSCOPE_PF_PID=$!
    kubectl port-forward -n "${NAMESPACE}" service/jaeger-http "${JAEGER_LOCAL_PORT}":"${JAEGER_UI_CONTAINER_PORT}" > /dev/null 2>&1 &
    JAEGER_PF_PID=$!

    sleep 2

    echo "${PROMETHEUS_PF_PID}" > "${PORT_FORWARD_DIR}"/porch-prometheus-pf.pid
    echo "${GRAFANA_PF_PID}" > "${PORT_FORWARD_DIR}"/porch-grafana-pf.pid
    echo "${PYROSCOPE_PF_PID}" > "${PORT_FORWARD_DIR}"/porch-pyroscope-pf.pid
    echo "${JAEGER_PF_PID}" > "${PORT_FORWARD_DIR}"/porch-jaeger-pf.pid

    PROMETHEUS_URL="http://localhost:${PROMETHEUS_LOCAL_PORT}"
    GRAFANA_URL="http://localhost:${GRAFANA_LOCAL_PORT}"
    PYROSCOPE_URL="http://localhost:${PYROSCOPE_LOCAL_PORT}"
    JAEGER_URL="http://localhost:${JAEGER_LOCAL_PORT}"

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
    log_info "  Pyroscope:  ${PYROSCOPE_URL}"
    log_info "  Jaeger:     ${JAEGER_URL}"
    echo ""
    log_info "  - Porch components export traces to Jaeger OTLP (grpc) at:"
    log_info "      jaeger-otlp.${NAMESPACE}.svc.cluster.local:${JAEGER_OTLP_CONTAINER_PORT}"
    echo ""
    log_info "  - Alloy discovers annotated pods in the cluster via profiles.grafana.com/*"
    log_info "      - porch-server, porch-controllers, function-runner (port name: pprof)"
    log_info ""
    log_info "  - Prometheus is scraping metrics from:"
    log_info "      - porch-server port 9464"
    log_info "      - porch-controller port 9464"
    log_info "      - function-runner port 9464"
    log_info "      - postgres-exporter port ${POSTGRES_EXPORTER_CONTAINER_PORT}"
    log_info "      - perf tests on host 172.17.0.1:9095 (when -enable-prometheus is set)"
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
        kubectl delete deployment prometheus grafana pyroscope alloy postgres-exporter jaeger -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete service prometheus grafana pyroscope postgres-exporter jaeger-otlp jaeger-http -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete configmap prometheus-config alloy-config grafana-dashboards grafana-dashboards-provider grafana-datasources -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete secret grafana-admin-creds postgres-exporter-db-creds -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete serviceaccount prometheus pyroscope alloy jaeger -n "$NAMESPACE" --ignore-not-found=true
        kubectl delete clusterrole prometheus alloy --ignore-not-found=true
        kubectl delete clusterrolebinding prometheus alloy --ignore-not-found=true

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
            log_info "Starting deployment of monitoring stack..."
            check_kpt
            create_namespace
            deploy_monitoring
            wait_for_deployment prometheus
            wait_for_deployment grafana
            wait_for_deployment pyroscope
            wait_for_deployment jaeger
            wait_for_deployment alloy
            wait_for_deployment postgres-exporter
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
            echo "  DOCKERHUB_MIRROR     - Registry mirror for images (default: docker.io)"
            echo "  PROMETHEUS_VERSION   - Prometheus image version (default: latest)"
            echo "  GRAFANA_VERSION      - Grafana image version (default: latest)"
            echo "  PYROSCOPE_VERSION    - Pyroscope image version (default: latest)"
            echo "  PYROSCOPE_LOCAL_PORT - Pyroscope local port-forward port (default: 4040)"
            echo "  JAEGER_VERSION       - Jaeger image version (default: latest)"
            echo "  JAEGER_LOCAL_PORT    - Jaeger UI local port-forward port (default: 16686)"
            echo "  ALLOY_VERSION                 - Grafana Alloy image version (default: latest)"
            echo "  POSTGRES_EXPORTER_VERSION     - Postgres exporter image version (default: v0.16.0)"
            echo "  POSTGRES_EXPORTER_CONTAINER_PORT - Postgres exporter port (default: 9187)"
            echo "  POSTGRES_DB_USER              - Postgres DB user for exporter (default: porch)"
            echo "  POSTGRES_DB_PASSWORD          - Postgres DB password for exporter (default: porch)"
            echo "  POSTGRES_EXPORTER_DATA_SOURCE_URI - Postgres connection URI for exporter"
            echo ""
            echo "Requirements:"
            echo "  - kpt CLI (install from: https://kpt.dev/installation/)"
            echo "  - kubectl configured with cluster access"
            exit 1
            ;;
    esac
}
main "$@"
