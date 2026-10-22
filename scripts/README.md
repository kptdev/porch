# Porch Scripts Organization

This directory contains utility scripts organized by functional category. Most scripts are called via Makefile targets (see root `Makefile` for convenience targets).

## Directory Structure

### `lib/` - Shared Libraries
Reusable functions and utilities sourced by other scripts.

- `common.sh` - Environment configuration and defaults (IMAGE_REPO, PORCH_CACHE_TYPE, etc.)
- `get-kind-metallb-subnet.sh` - Kind cluster network utilities
- `webhook-utils.sh` - Webhook certificate generation and management
- `_trap` - Signal handling and timing utilities

### `deploy/` - Deployment Orchestration
Scripts for building, configuring, and deploying Porch to kind clusters.

- `create-deployment-config.sh` - Main entry point for deployment configuration
- `create-deployment-blueprint.sh` - Generates the complete deployment package with kpt
- `load-images-to-kind.sh` - Builds and loads Docker images into kind cluster
- `reload-component.sh` - Rebuilds and reloads a single component (server, controllers, function-runner)
- `remove-porch-server-from-deployment-config.sh` - Removes porch-server, routes to local instance
- `remove-controller-from-deployment-config.sh` - Removes controllers for local development

### `dev/` - Local Development
Scripts for setting up local development environments.

- `setup-dev-env.sh` - Main entry point: creates kind cluster, installs MetalLB, deploys Gitea, generates PKI
- `local-dev.sh` - Manages local services: etcd, kube-apiserver, function-runner, porch-server, jaeger
- `install-dev-gitea-setup.sh` - Deploys and configures Gitea Git server
- `install-local-kpt-pkg.sh` - Installs kpt packages into local environment
- `set-push-drafts.sh` - Shows or sets push-drafts-to-git on the running porch-server and controllers, without a redeploy

### `monitoring/` - Observability
Scripts for deploying monitoring and tracing infrastructure.

- `deploy-monitoring.sh` - Deploys Prometheus, Grafana, Jaeger, Pyroscope; manages observability stack

### `testing/` - E2E Testing
Scripts for running end-to-end tests.

- `clean-e2e-test.sh` - Runs v1alpha1 E2E tests in a fresh kind cluster
- `clean-e2e-test-crd.sh` - Runs v1alpha2 CRD E2E tests in a fresh kind cluster
- `run-compat-cli-test.sh` - Tests porchctl CLI compatibility across versions
- `run-load-test.sh` - Load testing
- `cleanup-after-tests.sh` - Cleans up test artifacts (finalizers, namespaces, cache)

### `codegen/` - Code Generation
Scripts for generating code and documentation.

- `generate-api.sh` - Generates CRDs, deepcopy, conversions, and OpenAPI specs
- `generate-api-reference-md.sh` - Generates API reference documentation
- `build-versioned-docs.sh` - Builds versioned documentation site (used by Netlify)

### `util/` - Miscellaneous Utilities
One-off scripts for specific tasks.

- `run-with-timeout.sh` - Executes command with timeout
- `verify-release-artifacts.sh` - Validates release artifacts
- `update-kube-apiserver-vendoring.sh` - Updates kube-apiserver vendoring
- `verify-fix-all.sh` - Verifies all formatting and linting checks pass
- `create-test-repo.sh` - Creates test Git repositories
- `tar-test-repo.sh` - Archives test repositories
- `modify-gitea-test-blueprints.sh` - Modifies test blueprint configurations
- `create-deployment-kpt.sh` - Legacy deployment script (deprecated)
- `create-deployment-package.sh` - Legacy deployment script (deprecated)
- `apply-dev-config.sh` - Applies development configuration
- `check-versions.sh` - Checks version consistency between source files and `docs/config.toml` (supports `--fix`)

### Root-level Templates
- `boilerplate.go.txt` - Copyright header for Go files
- `boilerplate.yaml.txt` - Copyright header for YAML and script files

## Common Workflows

### Development Setup
```bash
make setup-dev-env           # One-time cluster + infrastructure setup
make run-in-kind             # Deploy porch to the cluster
make run-local               # Run porch locally against cluster
```

### Testing
```bash
make test-e2e-clean          # v1alpha1 E2E tests in fresh cluster
make test-e2e-crd-clean      # v1alpha2 CRD E2E tests in fresh cluster
```

### Monitoring
```bash
make deploy-monitoring       # Deploy Prometheus, Grafana
make deploy-monitoring-jaeger # Deploy Jaeger tracing
make cleanup-monitoring      # Tear down monitoring stack
```

### Component Development
```bash
make reload-server           # Rebuild and reload porch-server
make reload-controllers      # Rebuild and reload porch-controllers
make reload-function-runner  # Rebuild and reload function-runner
```

### Push Drafts Mode
`run-in-kind-v1alpha2` deploys with `push-drafts-to-git=true`. To deploy with it off, set `DB_PUSH_DRAFTS_TO_GIT` in the environment, in `.env` or on the command line. To flip it on a running cluster without a redeploy:
```bash
make run-in-kind-v1alpha2 DB_PUSH_DRAFTS_TO_GIT=false   # at deploy time
make set-push-drafts-to-git VALUE=false                 # on the running cluster (reverted by the next deploy)
make set-push-drafts-to-git                             # show the current value
```

## Script Dependencies

Scripts use relative paths for sourcing libraries. When moving a script, update:
- `source "$(dirname "$0")/../lib/common.sh"` for lib scripts
- `source "$(dirname "$0")/../../lib/common.sh"` for scripts in subdirectories

## Notes

- All scripts should be executable: `chmod +x scripts/**/*.sh`
- Scripts use strict error handling: `set -e -u -o pipefail`
- Configuration via environment variables or `.env` file (see `lib/common.sh`)
- Script-to-script calls use relative paths to remain portable
