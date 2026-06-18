# Copyright 2022-2026 The kpt Authors
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

# Core configuration
BUILDDIR=$(CURDIR)/.build
CACHEDIR=$(CURDIR)/.cache
export PORCHDIR=$(abspath $(CURDIR))

# Base image versions
export ALPINE_VERSION ?= 3.23.3
export GOLANG_BOOKWORM_VERSION ?= 1.26.3-bookworm
export GOLANG_ALPINE_VERSION ?= 1.26.3-alpine

export DEPLOYPORCHCONFIGDIR ?= $(BUILDDIR)/deploy
DEPLOYKPTCONFIGDIR=$(BUILDDIR)/kpt_pkgs

# Image configuration
export IMAGE_REPO ?= ghcr.io/kptdev
export USER ?= porch

export IMAGE_TAG
ifndef IMAGE_TAG
  git_tag := $(shell git rev-parse --short HEAD || "latest" )
  $(shell git diff --quiet)
  ifneq ($(.SHELLSTATUS), 0)
    git_tag := $(git_tag)-dirty
  endif
  IMAGE_TAG=$(USER)-$(git_tag)
endif

# Image names
export PORCH_SERVER_IMAGE ?= porch-server
export PORCH_FUNCTION_RUNNER_IMAGE ?= porch-function-runner
export PORCH_CONTROLLERS_IMAGE ?= porch-controllers
export PORCH_WRAPPER_SERVER_IMAGE ?= porch-wrapper-server
export TEST_GIT_SERVER_IMAGE ?= test-git-server

# Kind configuration
KIND_CONTEXT_NAME ?= porch-test

# Include environment file if it exists
ifneq ("$(wildcard .env)", "")
    include .env
    export
endif

export PORCH_GHCR_PREFIX_URL ?= ghcr.io/kptdev/krm-functions-catalog
# remove '/' suffix
export PORCH_GHCR_PREFIX_URL := $(patsubst %/,%,$(PORCH_GHCR_PREFIX_URL))
export DOCKERHUB_MIRROR ?=
# remove '/' suffix
export DOCKERHUB_MIRROR := $(patsubst %/,%,$(DOCKERHUB_MIRROR))

# Include module makefiles
include make/build.mk        # generate, tidy, porch, porchctl, build-images, push-images
include make/deploy.mk       # deploy, run-in-kind*, destroy, deployment-config*, load-images-to-kind, reload-*
include make/local-dev.mk    # all, network, stop, start-etcd, start-kube-apiserver, start-function-runner, run-local
include make/infra.mk        # docker-build, docker-push, help (+ container runtime detection)
include make/go.mk           # fmt, vet, lint, fix-headers, fix-all
include make/testing.mk      # test, unit, unit-clean, vulncheck, test-e2e*
include make/security.mk     # gosec, gosec-sarif
include make/mocks.mk        # generate-mocks, clean-mocks

.DEFAULT_GOAL := help

##@ Common Workflows

.PHONY: build
build: porch porchctl ## Build all binaries

.PHONY: check
check: fmt vet lint test ## Run all code quality checks

.PHONY: clean
clean: unit-clean clean-mocks ## Clean all build artifacts

.PHONY: dev
dev: build check ## Full development cycle (build + check)

.PHONY: quick-test
quick-test: fmt vet test ## Quick development test cycle
