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

MOCKERY_VERSION=3.6.4

OS_ARCH ?= $(shell uname -m)
OS ?= $(shell uname)

##@ Mocking

.PHONY: install-mockery
install-mockery:
ifeq ($(CONTAINER_RUNNABLE), 0)
		$(CONTAINER_RUNTIME) pull docker.io/vektra/mockery:v${MOCKERY_VERSION}
else
		wget -qO- https://github.com/vektra/mockery/releases/download/v${MOCKERY_VERSION}/mockery_${MOCKERY_VERSION}_${OS}_${OS_ARCH}.tar.gz | sudo tar -xvzf - -C /usr/local/bin
endif

.PHONY: generate-mocks
generate-mocks: clean-mocks
ifeq ($(CONTAINER_RUNNABLE), 0)
		find . -name .mockery.yaml \
			-exec echo generating mocks specified in {} . . . \; \
			-execdir $(CONTAINER_RUNTIME) run --rm --security-opt label=disable -v  .:/src -w /src docker.io/vektra/mockery:v${MOCKERY_VERSION} \; \
			-exec echo generated mocks specified in {} \;
else
		find . -name .mockery.yaml \
			-exec echo generating mocks specified in {} . . . \; \
			-execdir mockery \; \
			-exec echo generated mocks specified in {} \;
endif

.PHONY: clean-mocks
clean-mocks:
	rm -fr test/mockery/mocks