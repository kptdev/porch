#  Copyright 2025 The kpt Authors
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

# Local development environment targets

##@ Local Development

.PHONY: network
network:
	./scripts/local-dev.sh network

.PHONY: stop
stop:
	./scripts/local-dev.sh stop

.PHONY: start-etcd
start-etcd:
	./scripts/local-dev.sh etcd

.PHONY: start-kube-apiserver
start-kube-apiserver:
	./scripts/local-dev.sh apiserver

.PHONY: start-function-runner
start-function-runner:
	./scripts/local-dev.sh function

.PHONY: run-local
run-local: porch
	./scripts/local-dev.sh porch

.PHONY: run-jaeger
run-jaeger:
	./scripts/local-dev.sh jaeger

.PHONY: all
all: stop network start-etcd start-kube-apiserver start-function-runner run-local ## Start complete local development environment