#!/usr/bin/env bash
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

# Local development environment management

set -e

BUILDDIR=${BUILDDIR:-$(pwd)/.build}
CACHEDIR=${CACHEDIR:-$(pwd)/.cache}
PORCH=${PORCH:-$(pwd)/.build/porch}

usage() {
    echo "Usage: $0 <command>"
    echo "Commands:"
    echo "  start     - Start complete local environment"
    echo "  stop      - Stop all local services"
    echo "  network   - Create docker network"
    echo "  etcd      - Start etcd"
    echo "  apiserver - Start kube-apiserver"
    echo "  function  - Start function runner"
    echo "  porch     - Run porch locally"
    echo "  jaeger    - Start jaeger"
}

create_network() {
    echo "Creating docker network..."
    docker network create --subnet 192.168.8.0/24 porch 2>/dev/null || true
}

stop_all() {
    echo "Stopping local development environment..."
    docker stop kube-apiserver etcd function-runner jaeger 2>/dev/null || true
    docker rm kube-apiserver etcd function-runner jaeger 2>/dev/null || true
    docker network rm porch 2>/dev/null || true
    echo "Local environment stopped."
}

start_etcd() {
    echo "Starting etcd..."
    docker buildx build -t etcd --output=type=docker -f ./build/Dockerfile.etcd ./build
    rm -rf ${BUILDDIR}/data/etcd || true
    mkdir -p ${BUILDDIR}/data/etcd
    docker stop etcd 2>/dev/null || true
    docker rm etcd 2>/dev/null || true
    docker run --detach --user `id -u`:`id -g` \
      --network=porch --ip 192.168.8.200 --name etcd \
      -v ${BUILDDIR}/data/etcd:/data \
      etcd --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://127.0.0.1:2379
}

start_apiserver() {
    echo "Starting kube-apiserver..."
    docker buildx build -t kube-apiserver --output=type=docker -f ./build/Dockerfile.apiserver ./build
    docker stop kube-apiserver 2>/dev/null || true
    docker rm kube-apiserver 2>/dev/null || true
    deployments/local/makekeys.sh
    docker run --detach --user `id -u`:`id -g` \
      --network=porch --ip 192.168.8.201 --name kube-apiserver \
      -v ${BUILDDIR}/pki:/pki --add-host host.docker.internal:host-gateway \
      kube-apiserver --etcd-servers http://etcd:2379 --secure-port 9444 \
      --service-account-issuer=https://kubernetes.default.svc.cluster.local \
      --service-account-key-file=/pki/service-account.pub \
      --service-account-signing-key-file=/pki/service-account.key \
      --cert-dir=/pki --authorization-mode=RBAC --anonymous-auth=false \
      --client-ca-file=/pki/ca.crt
}

start_function_runner() {
    echo "Starting function runner..."
    IMAGE_NAME="${PORCH_FUNCTION_RUNNER_IMAGE}" make -C ./func build-image
    docker stop function-runner 2>/dev/null || true
    docker rm function-runner 2>/dev/null || true
    docker run --detach --network=porch --ip 192.168.8.202 --name function-runner \
      ${IMAGE_REPO}/${PORCH_FUNCTION_RUNNER_IMAGE}:${IMAGE_TAG} -disable-runtimes pod
}

run_porch() {
    echo "Running porch locally..."
    KUBECONFIG=$(pwd)/deployments/local/kubeconfig kubectl apply -f deployments/local/localconfig.yaml
    KUBECONFIG=$(pwd)/deployments/local/kubeconfig kubectl apply -f controllers/repositories/api/v1alpha1/
    KUBECONFIG=$(pwd)/deployments/local/kubeconfig kubectl apply -f internal/api/porchinternal/v1alpha1/
    ${PORCH} --secure-port 9443 --standalone-debug-mode \
      --kubeconfig="$(pwd)/deployments/local/kubeconfig" \
      --cache-directory="${CACHEDIR}" --function-runner 192.168.8.202:9445 \
      --repo-sync-frequency=3m --max-request-body-size=6291456
}

start_jaeger() {
    echo "Starting jaeger..."
    docker stop jaeger 2>/dev/null || true
    docker rm jaeger 2>/dev/null || true
    docker run --network=porch --name jaeger -d \
      -p 4317:4317 -p 6831:6831/udp -p 16686:16686 \
      jaegertracing/opentelemetry-all-in-one:latest
}

case "${1:-}" in
    start)
        create_network
        start_etcd
        start_apiserver
        start_function_runner
        run_porch
        ;;
    stop) stop_all ;;
    network) create_network ;;
    etcd) start_etcd ;;
    apiserver) start_apiserver ;;
    function) start_function_runner ;;
    porch) run_porch ;;
    jaeger) start_jaeger ;;
    *) usage; exit 1 ;;
esac