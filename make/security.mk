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

# Security scanning tools

GOSEC_VERSION ?= 2.23.0
# Gosec exclusions:
# G401,G501,G505: Weak crypto (MD5/SHA1) - used for non-security purposes (git hashes, etags)
# G304: File path from variable - unavoidable in file operations
GOSEC_EXCLUDES := G401,G501,G505,G304
GOSEC_ARGS ?= -stdout -verbose=text \
	-exclude-dir=generated \
	-exclude-dir=test \
	-exclude-dir=third_party \
	-exclude-dir=examples \
	-exclude-dir=internal/kpt \
	-exclude-generated \
	-severity=medium \
	-exclude=$(GOSEC_EXCLUDES)

##@ Security

.PHONY: gosec
gosec: ## Inspect the source code for security problems by scanning the Go Abstract Syntax Tree
	@if command -v gosec >/dev/null 2>&1; then \
		gosec -fmt=html -out=gosec-results.html $(GOSEC_ARGS) ./...; \
	else \
		go run github.com/securego/gosec/v2/cmd/gosec@v$(GOSEC_VERSION) -fmt=html -out=gosec-results.html $(GOSEC_ARGS) ./...; \
	fi

.PHONY: gosec-sarif
gosec-sarif: ## Generate SARIF security report
	@if command -v gosec >/dev/null 2>&1; then \
		gosec -fmt=sarif -out=gosec-results.sarif $(GOSEC_ARGS) ./...; \
	else \
		go run github.com/securego/gosec/v2/cmd/gosec@v$(GOSEC_VERSION) -fmt=sarif -out=gosec-results.sarif $(GOSEC_ARGS) ./...; \
	fi

.PHONY: install-gosec
install-gosec: ## Install the version of gosec used by the CI locally
	go install github.com/securego/gosec/v2/cmd/gosec@v$(GOSEC_VERSION)
