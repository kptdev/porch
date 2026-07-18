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

# Deployment targets and configuration

# Skip flags
export SKIP_IMG_BUILD ?= false
export SKIP_PORCHSERVER_BUILD ?= false
export SKIP_CONTROLLER_BUILD ?= false


# Porch cache type: CR (Custom Resource) || DB (Database)
export PORCH_CACHE_TYPE ?= DB

# Function runner warm-up pod cache
export FN_RUNNER_WARM_UP_POD_CACHE ?= true

# Enable v1alpha2 PackageRevision support (CRD install + controller flag + reconciler)
export CREATE_V1ALPHA2_RPKG ?= false

# Reconciler configuration
ALL_RECONCILERS=packagevariants,packagevariantsets,repositories
ifndef RECONCILERS
  ENABLED_RECONCILERS=$(ALL_RECONCILERS)
else
  ifeq ($(RECONCILERS),*)
    ENABLED_RECONCILERS=$(ALL_RECONCILERS)
  else
    ENABLED_RECONCILERS=$(RECONCILERS)
  endif
endif

##@ Build and deploy porch for development and testing

.PHONY: run-in-kind
run-in-kind: IMAGE_REPO=porch-kind## Build and deploy porch into a kind cluster with CR cache
run-in-kind: PORCH_CACHE_TYPE=CR
run-in-kind: load-images-to-kind deployment-config deploy-current-config

.PHONY: run-in-kind-v1alpha2
run-in-kind-v1alpha2: IMAGE_REPO=porch-kind## Build and deploy porch into a kind cluster with DB cache and v1alpha2 PackageRevision CRD creation
run-in-kind-v1alpha2: PORCH_CACHE_TYPE=DB
run-in-kind-v1alpha2: CREATE_V1ALPHA2_RPKG=true
run-in-kind-v1alpha2: load-images-to-kind deployment-config deploy-current-config

.PHONY: run-in-kind-v1alpha2-no-controller
run-in-kind-v1alpha2-no-controller: IMAGE_REPO=porch-kind## Build and deploy porch with DB cache, v1alpha2, fn-runner exposed, no controller (run locally)
run-in-kind-v1alpha2-no-controller: SKIP_CONTROLLER_BUILD=true
run-in-kind-v1alpha2-no-controller: PORCH_CACHE_TYPE=DB
run-in-kind-v1alpha2-no-controller: CREATE_V1ALPHA2_RPKG=true
run-in-kind-v1alpha2-no-controller: load-images-to-kind deployment-config-no-controller deploy-current-config

.PHONY: run-in-kind-db-cache
run-in-kind-db-cache: IMAGE_REPO=porch-kind## Build and deploy porch into a kind cluster with postgres backend
run-in-kind-db-cache: PORCH_CACHE_TYPE=DB
run-in-kind-db-cache: load-images-to-kind deployment-config deploy-current-config

.PHONY: run-in-kind-no-server
run-in-kind-no-server: IMAGE_REPO=porch-kind## Build and deploy porch without the porch-server into a kind cluster
run-in-kind-no-server: SKIP_PORCHSERVER_BUILD=true
run-in-kind-no-server: PORCH_CACHE_TYPE=CR
run-in-kind-no-server: load-images-to-kind deployment-config-no-server deploy-current-config

.PHONY: run-in-kind-db-cache-no-server
run-in-kind-db-cache-no-server: IMAGE_REPO=porch-kind## Build and deploy porch into a kind cluster with postgres backend without the porch-server
run-in-kind-db-cache-no-server: SKIP_PORCHSERVER_BUILD=true
run-in-kind-db-cache-no-server: PORCH_CACHE_TYPE=DB
run-in-kind-db-cache-no-server: load-images-to-kind deployment-config-no-server deploy-current-config

.PHONY: run-in-kind-no-controller
run-in-kind-no-controller: IMAGE_REPO=porch-kind## Build and deploy porch without the controllers into a kind cluster (WARNING: repositories will not sync without controller)
run-in-kind-no-controller: SKIP_CONTROLLER_BUILD=true
run-in-kind-no-controller: PORCH_CACHE_TYPE=CR
run-in-kind-no-controller: load-images-to-kind deployment-config-no-controller deploy-current-config

.PHONY: run-in-kind-db-cache-no-controller
run-in-kind-db-cache-no-controller: IMAGE_REPO=porch-kind## Build and deploy porch without the controllers into a kind cluster with postgres backend (WARNING: repositories will not sync without controller)
run-in-kind-db-cache-no-controller: SKIP_CONTROLLER_BUILD=true
run-in-kind-db-cache-no-controller: PORCH_CACHE_TYPE=DB
run-in-kind-db-cache-no-controller: load-images-to-kind deployment-config-no-controller deploy-current-config

.PHONY: run-in-kind-db-cache-push-drafts
run-in-kind-db-cache-push-drafts: IMAGE_REPO=porch-kind## Build and deploy porch into a kind cluster with postgres backend and db-push-drafts-to-git=true (for E2E tests)
run-in-kind-db-cache-push-drafts: PORCH_CACHE_TYPE=DB
run-in-kind-db-cache-push-drafts: DB_PUSH_DRAFTS_TO_GIT=true
run-in-kind-db-cache-push-drafts: load-images-to-kind deployment-config deploy-current-config

.PHONY: run-in-kind-db-cache-no-git-push-drafts
run-in-kind-db-cache-no-git-push-drafts: IMAGE_REPO=porch-kind## Build and deploy porch into a kind cluster with postgres backend without mock git server and db-push-drafts-to-git=true (for E2E tests)
run-in-kind-db-cache-no-git-push-drafts: SKIP_LOCAL_GIT=true
run-in-kind-db-cache-no-git-push-drafts: PORCH_CACHE_TYPE=DB
run-in-kind-db-cache-no-git-push-drafts: DB_PUSH_DRAFTS_TO_GIT=true
run-in-kind-db-cache-no-git-push-drafts: load-images-to-kind deployment-config deploy-current-config

.PHONY: destroy
destroy:## Deletes all porch resources installed by the last run-in-kind-* command
	@if [ -d "$(DEPLOYPORCHCONFIGDIR)-post" ]; then \
		kubectl delete -f "$(DEPLOYPORCHCONFIGDIR)-post/" --ignore-not-found; \
	fi
	kpt live destroy $(DEPLOYPORCHCONFIGDIR)

.PHONY: deployment-config 
deployment-config:## Generate a porch deployment kpt package into $(DEPLOYPORCHCONFIGDIR)
	./scripts/create-deployment-config.sh

.PHONY: deployment-config-no-server
deployment-config-no-server: deployment-config## Generate a deployment kpt package that contains all of porch except the porch-server into $(DEPLOYPORCHCONFIGDIR)
	./scripts/remove-porch-server-from-deployment-config.sh

.PHONY: deployment-config-no-controller
deployment-config-no-controller: deployment-config## Generate a deployment kpt package that contains all of porch except the controllers into $(DEPLOYPORCHCONFIGDIR)
	./scripts/remove-controller-from-deployment-config.sh

.PHONY: load-images-to-kind
load-images-to-kind:## Build porch images and load them into a kind cluster
	./scripts/load-images-to-kind.sh

.PHONY: deploy-current-config
deploy-current-config:## Deploy the configuration that is currently in $(DEPLOYPORCHCONFIGDIR)
	kpt live init $(DEPLOYPORCHCONFIGDIR) --name porch --namespace porch-system --inventory-id porch || true
	./scripts/run-with-timeout.sh 300 kpt live apply --inventory-policy=adopt --server-side --force-conflicts $(DEPLOYPORCHCONFIGDIR)
	kubectl rollout status deployment function-runner --namespace porch-system --timeout=180s
	@if [ "$(PORCH_CACHE_TYPE)" = "DB" ]; then \
		kubectl rollout status statefulset porch-postgresql --namespace porch-system --timeout=180s; \
	fi
	@if [ "$(SKIP_PORCHSERVER_BUILD)" != "true" ]; then \
		kubectl rollout status deployment porch-server --namespace porch-system --timeout=180s; \
	fi
	@if [ "$(SKIP_CONTROLLER_BUILD)" != "true" ]; then \
		kubectl rollout status deployment porch-controllers --namespace porch-system --timeout=180s; \
	fi
	@if [ -d "$(DEPLOYPORCHCONFIGDIR)-post" ]; then \
		kubectl apply --server-side --force-conflicts -f "$(DEPLOYPORCHCONFIGDIR)-post/"; \
	fi
	@echo "Done."

.PHONY: reload-function-runner
reload-function-runner: IMAGE_REPO=porch-kind## Rebuild and reload function-runner in kind cluster
reload-function-runner:
	./scripts/reload-component.sh function-runner

.PHONY: reload-server
reload-server: IMAGE_REPO=porch-kind## Rebuild and reload porch-server in kind cluster
reload-server:
	./scripts/reload-component.sh server

.PHONY: reload-controllers
reload-controllers: IMAGE_REPO=porch-kind## Rebuild and reload porch-controllers in kind cluster
reload-controllers:
	./scripts/reload-component.sh controllers

PKG=gitea-dev
.PHONY: deploy-gitea-dev-pkg
deploy-gitea-dev-pkg:## Deploy gitea development package
	PKG=gitea-dev
	rm -rf $(DEPLOYKPTCONFIGDIR)/${PKG} || true
	mkdir -p $(DEPLOYKPTCONFIGDIR)/${PKG}
	./scripts/install-local-kpt-pkg.sh \
	  --destination $(DEPLOYKPTCONFIGDIR) \
	  --pkg ${PKG} \
	  --kubeconfig $(KUBECONFIG)

.PHONY: setup-dev-env
setup-dev-env: PORCH_TEST_CLUSTER=porch-test
setup-dev-env: GIT_REPO_NAME=porch-test
setup-dev-env: ## Setup gitea, Metallb and test repository in kind cluster
	./scripts/setup-dev-env.sh

##@ Monitoring

.PHONY: deploy-monitoring
deploy-monitoring:## Deploy Prometheus, Grafana, and Postgres Exporter
	./scripts/deploy-monitoring.sh deploy

.PHONY: deploy-monitoring-jaeger
deploy-monitoring-jaeger:## Deploy Jaeger and enable trace export from porch components
	./scripts/deploy-monitoring.sh jaeger

.PHONY: deploy-monitoring-pyroscope
deploy-monitoring-pyroscope:## Deploy Pyroscope and Alloy profiling stack
	./scripts/deploy-monitoring.sh pyroscope

.PHONY: cleanup-monitoring
cleanup-monitoring:## Remove monitoring stack and disable porch trace export
	./scripts/deploy-monitoring.sh cleanup

.PHONY: restart-monitoring
restart-monitoring:## Restart the base monitoring stack
	./scripts/deploy-monitoring.sh restart
