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

# Mock generation tools

MOCKERY_VERSION=3.7.0

##@ Mocking

.PHONY: generate-mocks
generate-mocks: clean-mocks
	@if command -v mockery >/dev/null 2>&1 && [ "$$(mockery version)" = "v$(MOCKERY_VERSION)" ]; then \
		mockery; \
	else \
		go run github.com/vektra/mockery/v3@v$(MOCKERY_VERSION); \
	fi

.PHONY: clean-mocks
clean-mocks:
	rm -fr test/mockery/mocks