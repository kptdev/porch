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

##@ API make target proxies

.PHONY: api/tidy api/fix api/vet api/fmt api/lint
api/tidy: ## tidy
	make -C api tidy

api/fix: ## fix
	make -C api fix

api/vet: ## vet
	make -C api vet

api/fmt: ## fmt
	make -C api fmt

api/lint: ## lint
	make -C api lint

api/test: ## test
	make -C api test

api/unit-clean: ## unit-clean
	make -C api unit-clean
