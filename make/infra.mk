#  Copyright 2023-2025 The kpt Authors
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

# Infrastructure tooling (Docker, Help, Container Runtime Detection)

GIT_ROOT_DIR ?= $(dir $(lastword $(MAKEFILE_LIST)))/../

# Container runtime detection
ifeq ($(CONTAINER_RUNTIME),)
ifeq ($(shell command -v podman > /dev/null 2>&1; echo $$?), 0)
CONTAINER_RUNTIME=podman
else
CONTAINER_RUNTIME=docker
endif
endif

CONTAINER_RUNNABLE ?= $(shell command -v $(CONTAINER_RUNTIME) > /dev/null 2>&1; echo $$?)
RUN_CONTAINER_COMMAND ?= $(CONTAINER_RUNTIME) run -it --rm -v "$(abspath $(GIT_ROOT_DIR)):$(abspath $(GIT_ROOT_DIR))" -w "$(CURDIR)" --security-opt label=disable

##@ Container Images

.PHONY: docker-build
docker-build:  ## Build a container image from the local Dockerfile
	$(CONTAINER_RUNTIME) buildx build --load --tag  ${IMG} -f ./Dockerfile "$(GIT_ROOT_DIR)"

.PHONY: docker-push
docker-push: docker-build ## Build and push the container image
	$(CONTAINER_RUNTIME) push ${IMG}

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[ a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)