# Copyright 2019,2026 The kpt Authors
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

IMAGE_TAG  ?= latest
IMAGE_REPO ?= ghcr.io/kptdev
IMAGE_NAME ?= function-runner
WRAPPER_SERVER_IMAGE_NAME ?= wrapper-server
COMPILED_PROTO=proto/evaluator_grpc.pb.go proto/evaluator.pb.go

PORCHDIR = $(abspath $(CURDIR)/..)

all: $(COMPILED_PROTO)

$(COMPILED_PROTO): proto/evaluator.proto
	protoc \
	  -I ./proto \
	  --go_out=./proto --go_opt=paths=source_relative \
	  --go-grpc_out=./proto --go-grpc_opt=paths=source_relative \
	  ./proto/evaluator.proto

.PHONY: build-image docker-build
build-image docker-build:
	docker buildx build --load --tag $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG) \
      --build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
      --build-arg GOLANG_ALPINE_VERSION=$(GOLANG_ALPINE_VERSION) \
	  $$(if [ -n "${DOCKERHUB_MIRROR}" ]; then echo "--build-arg DOCKERHUB_MIRROR=${DOCKERHUB_MIRROR}"; fi) \
	  $$(if [ -n "${PORCH_GHCR_PREFIX_URL}" ]; then echo "--build-arg PORCH_GHCR_PREFIX_URL=${PORCH_GHCR_PREFIX_URL}"; fi) \
      -f ./Dockerfile "$(PORCHDIR)"
	docker buildx build --load --tag $(IMAGE_REPO)/$(WRAPPER_SERVER_IMAGE_NAME):$(IMAGE_TAG) \
      --build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
      --build-arg GOLANG_ALPINE_VERSION=$(GOLANG_ALPINE_VERSION) \
	  $$(if [ -n "${DOCKERHUB_MIRROR}" ]; then echo "--build-arg DOCKERHUB_MIRROR=${DOCKERHUB_MIRROR}"; fi) \
	  $$(if [ -n "${PORCH_GHCR_PREFIX_URL}" ]; then echo "--build-arg PORCH_GHCR_PREFIX_URL=${PORCH_GHCR_PREFIX_URL}"; fi) \
      -f ./Dockerfile-wrapperserver "$(PORCHDIR)"

.PHONY: push-image docker-push
push-image docker-push:
	docker buildx build --push --tag $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG) \
      --build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
      --build-arg GOLANG_ALPINE_VERSION=$(GOLANG_ALPINE_VERSION) \
	  $$(if [ -n "${DOCKERHUB_MIRROR}" ]; then echo "--build-arg DOCKERHUB_MIRROR=${DOCKERHUB_MIRROR}"; fi) \
	  $$(if [ -n "${PORCH_GHCR_PREFIX_URL}" ]; then echo "--build-arg PORCH_GHCR_PREFIX_URL=${PORCH_GHCR_PREFIX_URL}"; fi) \
      -f ./Dockerfile "$(PORCHDIR)"
	docker buildx build --push --tag $(IMAGE_REPO)/$(WRAPPER_SERVER_IMAGE_NAME):$(IMAGE_TAG) \
      --build-arg ALPINE_VERSION=$(ALPINE_VERSION) \
      --build-arg GOLANG_ALPINE_VERSION=$(GOLANG_ALPINE_VERSION) \
	  $$(if [ -n "${DOCKERHUB_MIRROR}" ]; then echo "--build-arg DOCKERHUB_MIRROR=${DOCKERHUB_MIRROR}"; fi) \
	  $$(if [ -n "${PORCH_GHCR_PREFIX_URL}" ]; then echo "--build-arg PORCH_GHCR_PREFIX_URL=${PORCH_GHCR_PREFIX_URL}"; fi) \
      -f ./Dockerfile-wrapperserver "$(PORCHDIR)"
