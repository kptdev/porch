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

# Core Go development tools

GOLANGCI_LINT_VERSION ?= 2.12.2
GOLANG_CI_ARGS ?= -v --fix --timeout=10m

##@ Go Development

.PHONY: fmt
fmt: fmt-api ## Run go fmt against the codebase
	go fmt ./...

.PHONY: vet
vet: vet-api ## Run go vet against the codebase
	go vet ./...

.PHONY: fix
fix: fix-api ## Run go fix against the codebase
	go fix -omitzero=false ./...

.PHONY: lint
lint: lint-api ## Run Go linter against the codebase
	@if command -v golangci-lint >/dev/null 2>&1 && [ "$$(golangci-lint version --short)" = "$(GOLANGCI_LINT_VERSION)" ]; then \
		golangci-lint run ./... $(GOLANG_CI_ARGS); \
	else \
		go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_LINT_VERSION) run ./... $(GOLANG_CI_ARGS); \
	fi

.PHONY: fix-all
fix-all: tidy fix vet fmt lint ## Fix headers, format code, and tidy modules

.PHONY: tidy-api fix-api vet-api fmt-api lint-api
tidy-api:
	make -C api tidy

fix-api:
	make -C api fix

vet-api:
	make -C api vet

fmt-api:
	make -C api fmt

lint-api:
	make -C api lint
