#  Copyright 2025-2026 The kpt Authors
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

# Build targets and configuration

MYGOBIN := $(shell go env GOPATH)/bin
PORCHCTL_VERSION := $(shell date '+development-%Y-%m-%dT%H:%M:%S')
YEAR_GEN := $(shell date '+%Y')

PORCH = $(BUILDDIR)/porch
PORCHCTL = $(BUILDDIR)/porchctl

# API Modules
API_MODULES = \
 api \
 internal/api \
 controllers \

##@ Build

.PHONY: generate-api
generate-api:
	KUBE_VERBOSE=2 $(CURDIR)/scripts/generate-api.sh

.PHONY: generate
generate: generate-api ## Generate CRDs, other K8s manifests and helper go code
	@for f in $(API_MODULES); do (cd $$f; echo "Generating for $$f ..."; YEAR_GEN=$(YEAR_GEN) go generate -v ./...) || exit 1; done

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: porch
porch:
	go build -o $(PORCH) ./cmd/porch

.PHONY: porchctl
porchctl:
	go build -ldflags="-X github.com/kptdev/porch/cmd/porchctl/run.version=$(PORCHCTL_VERSION)" -o $(PORCHCTL) ./cmd/porchctl

.PHONY: build-images
build-images:
	ALPINE_VERSION="$(ALPINE_VERSION)" GOLANG_BOOKWORM_VERSION="$(GOLANG_BOOKWORM_VERSION)" IMAGE_NAME="$(PORCH_SERVER_IMAGE)" make -C build/ build-image
	ALPINE_VERSION="$(ALPINE_VERSION)" GOLANG_BOOKWORM_VERSION="$(GOLANG_BOOKWORM_VERSION)" IMAGE_NAME="$(PORCH_CONTROLLERS_IMAGE)" make -C controllers/ build-image
	ALPINE_VERSION="$(ALPINE_VERSION)" GOLANG_ALPINE_VERSION="$(GOLANG_ALPINE_VERSION)" IMAGE_NAME="$(PORCH_FUNCTION_RUNNER_IMAGE)" WRAPPER_SERVER_IMAGE_NAME="$(PORCH_WRAPPER_SERVER_IMAGE)" make -C func/ build-image
	GOLANG_BOOKWORM_VERSION="$(GOLANG_BOOKWORM_VERSION)" IMAGE_NAME="$(TEST_GIT_SERVER_IMAGE)" make -C test/ build-image

.PHONY: push-images
push-images:
	ALPINE_VERSION="$(ALPINE_VERSION)" GOLANG_BOOKWORM_VERSION="$(GOLANG_BOOKWORM_VERSION)" IMAGE_NAME="$(PORCH_SERVER_IMAGE)" make -C build/ push-image
	ALPINE_VERSION="$(ALPINE_VERSION)" GOLANG_BOOKWORM_VERSION="$(GOLANG_BOOKWORM_VERSION)" IMAGE_NAME="$(PORCH_CONTROLLERS_IMAGE)" make -C controllers/ push-image
	ALPINE_VERSION="$(ALPINE_VERSION)" GOLANG_ALPINE_VERSION="$(GOLANG_ALPINE_VERSION)" IMAGE_NAME="$(PORCH_FUNCTION_RUNNER_IMAGE)" WRAPPER_SERVER_IMAGE_NAME="$(PORCH_WRAPPER_SERVER_IMAGE)" make -C func/ push-image
	GOLANG_BOOKWORM_VERSION="$(GOLANG_BOOKWORM_VERSION)" IMAGE_NAME="$(TEST_GIT_SERVER_IMAGE)" make -C test/ push-image

.PHONY: dev-server
dev-server:
	ALPINE_VERSION="$(ALPINE_VERSION)" IMAGE_NAME="$(PORCH_SERVER_IMAGE)" make -C build/ push-image
	kubectl set image -n porch-system deployment/porch-server porch-server=$(IMAGE_REPO)/$(PORCH_SERVER_IMAGE):${IMAGE_TAG}

.PHONY: update-kube-apiserver-vendoring
update-kube-apiserver-vendoring:
	./scripts/update-kube-apiserver-vendoring.sh
