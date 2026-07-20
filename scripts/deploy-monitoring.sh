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
PORCH_NAMESPACE="${PORCH_NAMESPACE:-porch-system}"
PORCH_TRACE_DEPLOYMENTS=(
    porch-server
    function-runner
    porch-controllers
)
PORCH_PPROF_DEPLOYMENTS=(
    porch-server:porch-server
    function-runner:porch-function-runner
    porch-controllers:porch-controllers
)
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

BASE_MANIFESTS=(
    prometheus-deployment.yaml
    grafana-deployment.yaml
    postgres-exporter-deployment.yaml
)
JAEGER_MANIFESTS=(
    jaeger-deployment.yaml
)
PYROSCOPE_MANIFESTS=(
    pyroscope-deployment.yaml
)

JAEGER_OTLP_ENDPOINT="${JAEGER_OTLP_ENDPOINT:-http://jaeger-otlp.${NAMESPACE}.svc.cluster.local:${JAEGER_OTLP_CONTAINER_PORT}}"

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

is_base_deployed() {
    local deployment
    for deployment in prometheus grafana postgres-exporter; do
        if ! kubectl get deployment "$deployment" -n "$NAMESPACE" &> /dev/null; then
            return 1
        fi
    done
    return 0
}

is_jaeger_deployed() {
    kubectl get deployment jaeger -n "$NAMESPACE" &> /dev/null
}

is_pyroscope_deployed() {
    kubectl get deployment pyroscope -n "$NAMESPACE" &> /dev/null
}

prepare_manifests() {
    local temp_dir
    temp_dir=$(mktemp -d)
    local manifest

    for manifest in "$@"; do
        cp "${METRICS_DIR}/${manifest}" "$temp_dir/"
    done

    cat > "$temp_dir/Kptfile" <<EOF
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: porch-monitoring
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: Prometheus, Grafana, Postgres Exporter, Jaeger, and Pyroscope stack for Porch
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
    local -a manifest_files=()
    local manifest_file

    shopt -s nullglob
    for manifest_file in "${manifests_dir}"/*-deployment.yaml; do
        manifest_files+=("$manifest_file")
    done
    shopt -u nullglob

    if [ ${#manifest_files[@]} -eq 0 ]; then
        log_error "No deployment manifests to apply"
        exit 1
    fi

    log_info "Applying manifests..."
    local -a kubectl_args=()
    for manifest_file in "${manifest_files[@]}"; do
        kubectl_args+=(-f "$manifest_file")
    done
    kubectl apply "${kubectl_args[@]}"
}

create_namespace() {
    if kubectl get namespace "$NAMESPACE" &> /dev/null; then
        log_info "Namespace $NAMESPACE already exists"
    else
        log_info "Creating namespace $NAMESPACE"
        kubectl create namespace "$NAMESPACE"
    fi
}

apply_base_configmaps() {
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
}

apply_pyroscope_configmaps() {
    kubectl create configmap alloy-config \
        --from-file=config.alloy="${SCRIPT_DIR}/../deployments/metrics-resources/alloy-config.alloy" \
        -n "$NAMESPACE" \
        --dry-run=client -o yaml | kubectl apply -f -
}

deploy_base_stack() {
    log_info "Deploying base monitoring stack (Prometheus, Grafana, Postgres Exporter)..."
    log_info "Rendering manifests with kpt..."

    apply_base_configmaps

    local manifests_dir
    manifests_dir=$(prepare_manifests "${BASE_MANIFESTS[@]}")
    apply_manifests "$manifests_dir"
    rm -rf "$manifests_dir"

    wait_for_deployment prometheus
    wait_for_deployment grafana
    wait_for_deployment postgres-exporter

    log_info "Base monitoring stack deployed successfully"
}

ensure_base_deployed() {
    if is_base_deployed; then
        log_info "Base monitoring stack already deployed"
        return 0
    fi

    log_info "Base monitoring stack not found, deploying first..."
    deploy_base_stack
}

deploy_base() {
    check_kpt
    create_namespace
    deploy_base_stack
    get_service_urls
}

deploy_jaeger() {
    check_kpt
    create_namespace
    ensure_base_deployed

    log_info "Deploying Jaeger..."
    log_info "Rendering manifests with kpt..."

    local manifests_dir
    manifests_dir=$(prepare_manifests "${JAEGER_MANIFESTS[@]}")
    apply_manifests "$manifests_dir"
    rm -rf "$manifests_dir"

    wait_for_deployment jaeger
    enable_porch_trace_export

    log_info "Jaeger deployed successfully"
    get_service_urls
}

deploy_pyroscope() {
    check_kpt
    create_namespace
    ensure_base_deployed

    log_info "Deploying Pyroscope and Alloy..."
    log_info "Rendering manifests with kpt..."

    apply_pyroscope_configmaps

    local manifests_dir
    manifests_dir=$(prepare_manifests "${PYROSCOPE_MANIFESTS[@]}")
    apply_manifests "$manifests_dir"
    rm -rf "$manifests_dir"

    wait_for_deployment pyroscope
    wait_for_deployment alloy
    enable_porch_pprof_annotations

    log_info "Pyroscope and Alloy deployed successfully"
    get_service_urls
}

wait_for_deployment() {
    local deployment=$1
    log_info "Waiting for $deployment to be ready..."
    kubectl wait --for=condition=available --timeout=300s deployment/"$deployment" -n "$NAMESPACE"
}

enable_porch_trace_export() {
    local deployment
    local -a trace_env=(
        "OTEL_TRACES_EXPORTER=otlp"
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=${JAEGER_OTLP_ENDPOINT}"
        "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc"
    )

    for deployment in "${PORCH_TRACE_DEPLOYMENTS[@]}"; do
        if kubectl get deployment "$deployment" -n "$PORCH_NAMESPACE" &> /dev/null; then
            log_info "Enabling trace export on ${PORCH_NAMESPACE}/${deployment}..."
            kubectl set env deployment/"$deployment" -n "$PORCH_NAMESPACE" "${trace_env[@]}"
        else
            log_info "Skipping trace export for ${PORCH_NAMESPACE}/${deployment} (not deployed)"
        fi
    done
}

disable_porch_trace_export() {
    local deployment
    local -a trace_env=(
        "OTEL_TRACES_EXPORTER=none"
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT-"
        "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL-"
    )

    for deployment in "${PORCH_TRACE_DEPLOYMENTS[@]}"; do
        if kubectl get deployment "$deployment" -n "$PORCH_NAMESPACE" &> /dev/null; then
            log_info "Disabling trace export on ${PORCH_NAMESPACE}/${deployment}..."
            kubectl set env deployment/"$deployment" -n "$PORCH_NAMESPACE" "${trace_env[@]}"
        fi
    done
}

enable_porch_pprof_annotations() {
    local entry deployment service_name
    for entry in "${PORCH_PPROF_DEPLOYMENTS[@]}"; do
        deployment="${entry%%:*}"
        service_name="${entry#*:}"
        if kubectl get deployment "$deployment" -n "$PORCH_NAMESPACE" &> /dev/null; then
            log_info "Enabling pprof annotations on ${PORCH_NAMESPACE}/${deployment}..."
            kubectl patch deployment "$deployment" -n "$PORCH_NAMESPACE" --type merge -p "$(cat <<EOF
{
  "spec": {
    "template": {
      "metadata": {
        "annotations": {
          "profiles.grafana.com/service_name": "${service_name}",
          "profiles.grafana.com/cpu.scrape": "true",
          "profiles.grafana.com/cpu.port_name": "pprof",
          "profiles.grafana.com/memory.scrape": "true",
          "profiles.grafana.com/memory.port_name": "pprof",
          "profiles.grafana.com/goroutine.scrape": "true",
          "profiles.grafana.com/goroutine.port_name": "pprof",
          "profiles.grafana.com/block.scrape": "true",
          "profiles.grafana.com/block.port_name": "pprof",
          "profiles.grafana.com/mutex.scrape": "true",
          "profiles.grafana.com/mutex.port_name": "pprof"
        }
      }
    }
  }
}
EOF
)"
        else
            log_info "Skipping pprof annotations for ${PORCH_NAMESPACE}/${deployment} (not deployed)"
        fi
    done
}

disable_porch_pprof_annotations() {
    local entry deployment
    for entry in "${PORCH_PPROF_DEPLOYMENTS[@]}"; do
        deployment="${entry%%:*}"
        if kubectl get deployment "$deployment" -n "$PORCH_NAMESPACE" &> /dev/null; then
            log_info "Disabling pprof annotations on ${PORCH_NAMESPACE}/${deployment}..."
            kubectl patch deployment "$deployment" -n "$PORCH_NAMESPACE" --type merge -p '{
  "spec": {
    "template": {
      "metadata": {
        "annotations": {
          "profiles.grafana.com/service_name": null,
          "profiles.grafana.com/cpu.scrape": null,
          "profiles.grafana.com/cpu.port_name": null,
          "profiles.grafana.com/memory.scrape": null,
          "profiles.grafana.com/memory.port_name": null,
          "profiles.grafana.com/goroutine.scrape": null,
          "profiles.grafana.com/goroutine.port_name": null,
          "profiles.grafana.com/block.scrape": null,
          "profiles.grafana.com/block.port_name": null,
          "profiles.grafana.com/mutex.scrape": null,
          "profiles.grafana.com/mutex.port_name": null
        }
      }
    }
  }
}'
        fi
    done
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

    if is_base_deployed; then
        kubectl port-forward -n "${NAMESPACE}" deployment/prometheus "${PROMETHEUS_LOCAL_PORT}":"${PROMETHEUS_CONTAINER_PORT}" > /dev/null 2>&1 &
        echo "$!" > "${PORT_FORWARD_DIR}"/porch-prometheus-pf.pid
        kubectl port-forward -n "${NAMESPACE}" deployment/grafana "${GRAFANA_LOCAL_PORT}":"${GRAFANA_CONTAINER_PORT}" > /dev/null 2>&1 &
        echo "$!" > "${PORT_FORWARD_DIR}"/porch-grafana-pf.pid
    fi

    if is_pyroscope_deployed; then
        kubectl port-forward -n "${NAMESPACE}" deployment/pyroscope "${PYROSCOPE_LOCAL_PORT}":"${PYROSCOPE_CONTAINER_PORT}" > /dev/null 2>&1 &
        echo "$!" > "${PORT_FORWARD_DIR}"/porch-pyroscope-pf.pid
    fi

    if is_jaeger_deployed; then
        kubectl port-forward -n "${NAMESPACE}" service/jaeger-http "${JAEGER_LOCAL_PORT}":"${JAEGER_UI_CONTAINER_PORT}" > /dev/null 2>&1 &
        echo "$!" > "${PORT_FORWARD_DIR}"/porch-jaeger-pf.pid
    fi

    sleep 2

    echo ""
    log_info "=========================================="
    log_info "Services deployed successfully!"
    log_info "=========================================="
    echo ""
    log_info "Access via port-forward (recommended):"

    if is_base_deployed; then
        log_info "  Prometheus: http://localhost:${PROMETHEUS_LOCAL_PORT}"
        log_info "  Grafana:    http://localhost:${GRAFANA_LOCAL_PORT}"
        log_info "    Username: ${GRAFANA_ADMIN_USER}"
        log_info "    Password: ${GRAFANA_ADMIN_PW}"
        log_info "      stored in: kubectl -n ${NAMESPACE} get secrets --selector app=grafana -o yaml"
        echo ""
        log_info "  - Prometheus is scraping metrics from:"
        log_info "      - porch-server port 9464"
        log_info "      - porch-controller port 9464"
        log_info "      - function-runner port 9464"
        log_info "      - postgres-exporter port ${POSTGRES_EXPORTER_CONTAINER_PORT}"
        log_info "      - perf tests on host 172.17.0.1:9095 (when -enable-prometheus is set)"
    fi

    if is_pyroscope_deployed; then
        log_info "  Pyroscope:  http://localhost:${PYROSCOPE_LOCAL_PORT}"
        echo ""
        log_info "  - Alloy discovers porch-server, porch-controllers, and function-runner"
        log_info "      via profiles.grafana.com/* annotations (port name: pprof)"
    fi

    if is_jaeger_deployed; then
        log_info "  Jaeger:     http://localhost:${JAEGER_LOCAL_PORT}"
        echo ""
        log_info "  - Porch components export traces to Jaeger OTLP (grpc) at:"
        log_info "      jaeger-otlp.${NAMESPACE}.svc.cluster.local:${JAEGER_OTLP_CONTAINER_PORT}"
    fi

    echo ""
    log_info "To stop port forwarding, run:"
    log_info "  find /tmp/tmp*_porch-monitoring-pf.pid.d/ -name '*.pid' -exec pkill -F '{}' \;"
    echo ""
}

cleanup() {
    log_warn "Cleaning up existing deployment..."

    log_info "Stopping port forwarding..."
    stop_port_forwards
    disable_porch_trace_export
    disable_porch_pprof_annotations

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
            log_info "Starting deployment of base monitoring stack..."
            deploy_base
            ;;
        jaeger)
            log_info "Starting deployment of Jaeger..."
            deploy_jaeger
            ;;
        pyroscope)
            log_info "Starting deployment of Pyroscope..."
            deploy_pyroscope
            ;;
        cleanup)
            check_kpt
            cleanup
            ;;
        restart)
            check_kpt
            cleanup
            sleep 2
            deploy_base
            ;;
        *)
            log_error "Unknown action: $action"
            echo "Usage: $0 {deploy|jaeger|pyroscope|cleanup|restart}"
            echo ""
            echo "Actions:"
            echo "  deploy     Deploy Prometheus, Grafana, and Postgres Exporter (default)"
            echo "  jaeger     Deploy Jaeger (deploys base stack first if needed)"
            echo "  pyroscope  Deploy Pyroscope and Alloy (deploys base stack first if needed)"
            echo "  cleanup    Remove all monitoring resources"
            echo "  restart    Cleanup and redeploy the base stack"
            echo ""
            echo "Environment variables:"
            echo "  NAMESPACE            - Kubernetes namespace (default: porch-monitoring)"
            echo "  PORCH_NAMESPACE      - Porch workloads namespace (default: porch-system)"
            echo "  JAEGER_OTLP_ENDPOINT - Jaeger OTLP endpoint for porch trace export"
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
