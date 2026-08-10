#  Copyright 2026 The kpt Authors
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

GOLANG_CI_VER ?= 2.12.2
GOLANG_CI_ARGS ?= -v --fix --timeout=10m

##@ API Go Development

.PHONY: fmt
fmt: ## Run go fmt against the module
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against the module
	go vet ./...

.PHONY: fix
fix: ## Run go fix against the module
	go fix -omitzero=false ./...

.PHONY: tidy
tidy: ## Run go tidy against the module
	go mod tidy

.PHONY: lint
lint: ## Run Go linter against the module
	@if command -v golangci-lint >/dev/null 2>&1 && [ "$$(golangci-lint version --short)" = "$(GOLANG_CI_VER)" ]; then \
		golangci-lint run ./... $(GOLANG_CI_ARGS); \
	else \
		go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANG_CI_VER) run ./... $(GOLANG_CI_ARGS); \
	fi

.PHONY: fix-all
fix-all: tidy fix vet fmt lint ## Run go mod tidy, fix, vet, fmt and then lint the module
