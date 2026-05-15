---
title: "Development Environment"
type: docs
weight: 1
description: Setting up a local Porch development environment
---

This guide walks you through setting up a local Porch development environment with a kind cluster, enabling you to debug the Porch server and controllers in VS Code with various configurations.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Go](https://go.dev/doc/install) (version specified [in Porch's go.mod](https://github.com/kptdev/porch/blob/main/go.mod#L3))
- [VS Code](https://code.visualstudio.com/) with [Go extension](https://marketplace.visualstudio.com/items?itemName=golang.go)
- [make](https://www.gnu.org/software/make/)

### MacOS Users

The deployment scripts require bash 4.x or later. MacOS ships with bash 3.x:

1. Install bash 4.x+ via Homebrew: `brew install bash`
2. Ensure `/opt/homebrew/bin` appears before `/bin` and `/usr/bin` in your PATH

{{% alert title="Warning" color="warning" %}}
This permanently changes your default bash version system-wide.
{{% /alert %}}

## Automated Setup

The quickest way to set up your development environment - from the root directory of the Porch repository, run:

```bash
make setup-dev-env
```

This script:

1. Creates a kind cluster (name from `PORCH_TEST_CLUSTER` env var, defaults to `porch-test`)
2. Installs MetalLB load balancer for LoadBalancer services
3. Installs Gitea git server (accessible at http://localhost:3000/porch, credentials: porch/secret)
4. Generates PKI resources for testing
5. Builds the porchctl CLI binary at `.build/porchctl`

The script is idempotent and can be rerun safely.

### Post-Setup

Add porchctl to your PATH:

```bash
export PATH="$PATH:$(pwd)/.build"
```

## Deployment Configurations

Porch can be deployed in different configurations for various debugging scenarios. All VS Code launch configurations are defined in `.vscode/launch.json`.

### Full Deployment (All Components in Kind)

#### Option 1: CR Cache (Default)

Deploy all Porch components to the kind cluster:

```bash
make run-in-kind
```

#### Option 2: DB Cache (PostgreSQL)

Deploy all Porch components with PostgreSQL backend for the package revision cache:

```bash
make run-in-kind-db-cache
```

**Verify the deployment:**

```bash
# Check APIService
kubectl get apiservice v1alpha1.porch.kpt.dev

# Check API resources
kubectl api-resources | grep porch
```

**Expected Output:**

```bash
packagerevs                                      config.porch.kpt.dev/v1alpha1     true         PackageRev
packagevariants                                  config.porch.kpt.dev/v1alpha1     true         PackageVariant
packagevariantsets                               config.porch.kpt.dev/v1alpha2     true         PackageVariantSet
repositories                                     config.porch.kpt.dev/v1alpha1     true         Repository
packagerevisionresources                         porch.kpt.dev/v1alpha1            true         PackageRevisionResources
packagerevisions                                 porch.kpt.dev/v1alpha1            true         PackageRevision
packages                                         porch.kpt.dev/v1alpha1            true         PorchPackage
```

### Debug Server (Server in VS Code, Controllers in Kind)

Two configurations available depending on cache backend:

#### Option 1: CR Cache (Default)

**Make target:**
```bash
make run-in-kind-no-server
```

**VS Code launch configuration:** `Launch Server`

1. Open `porch.code-workspace` in VS Code
2. Ensure your `KUBECONFIG` points to the porch-test cluster
3. Select **Run and Debug** → **Launch Server**

This deploys all Porch components except the server, which runs locally using CR-based cache.

**Verify the server:**
```bash
curl https://localhost:4443/apis/porch.kpt.dev/v1alpha1 -k
```

#### Option 2: DB Cache (PostgreSQL)

**Make target:**
```bash
make run-in-kind-db-cache-no-server
```

**VS Code launch configuration:** `Launch Server with DB cache`

1. Ensure PostgreSQL is deployed (the make target handles this)
2. Open `porch.code-workspace` in VS Code
3. Select **Run and Debug** → **Launch Server with DB cache**

This deploys all Porch components including PostgreSQL, except the server which runs locally using DB cache.

**Default database connection details:**
```bash
DB_DRIVER=pgx
DB_HOST=<postgres-lb-ip>  # Discover with: kubectl get svc -n porch-system porch-postgresql-lb -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
DB_PORT=5432
DB_NAME=porch
DB_USER=porch
DB_PASSWORD=porch
```

### Debug Controllers (Controllers in VS Code, Server in Kind)

The porch-controllers binary includes three controllers: PackageVariant, PackageVariantSet, and Repository controllers. Two configurations are available depending on cache backend:

#### Option 1: CR Cache (Default)

**Make target:**
```bash
make run-in-kind-no-controller
```

**VS Code launch configuration:** `Launch Controllers`

1. Select **Run and Debug** → **Launch Controllers**

This deploys all Porch components except the controllers, which run locally.

#### Option 2: DB Cache (PostgreSQL)

**Make target:**
```bash
make run-in-kind-db-cache-no-controller
```

**VS Code launch configuration:** `Launch Controllers`

1. Select **Run and Debug** → **Launch Controllers**

This deploys all Porch components with PostgreSQL backend, except the controllers which run locally.

### All Available Make Targets

| Make Target | Description | VS Code Config |
|-------------|-------------|----------------|
| `make run-in-kind` | Full deployment (CR cache) | N/A |
| `make run-in-kind-db-cache` | Full deployment (DB cache) | N/A |
| `make run-in-kind-no-server` | Debug server (CR cache) | Launch Server |
| `make run-in-kind-db-cache-no-server` | Debug server (DB cache) | Launch Server with DB cache |
| `make run-in-kind-no-controller` | Debug controllers (CR cache) | Launch Controllers |
| `make run-in-kind-db-cache-no-controller` | Debug controllers (DB cache) | Launch Controllers |

### Switching Between Configurations

You can switch between any configuration without cleanup:

```bash
# Example: Switch from full deployment to debug server
make run-in-kind
make run-in-kind-no-server  # Then launch server in VS Code

# Example: Switch cache backends
make run-in-kind-no-server
make run-in-kind-db-cache-no-server  # Then relaunch server in VS Code
```

The current deployment configuration is stored in `.build/deploy`.

### Cleanup

Remove all Porch resources from the cluster:

```bash
make destroy
```

## Understanding Cache Backends

Porch supports two cache backends:

**[CR Cache (Default)]({{% relref "../../5_architecture_and_components/package-cache/cr-cache" %}}):** Stores package data as Kubernetes Custom Resources. Simpler setup, suitable for development.

**[DB Cache (PostgreSQL)]({{% relref "../../5_architecture_and_components/package-cache/db-cache" %}}):** Stores package data in PostgreSQL. Better performance with large repositories, closer to production setup.

The make targets with `db-cache` in the name automatically deploy and configure PostgreSQL in addition to Porch. The corresponding VS Code launch configurations include the necessary database connection parameters.

## Testing Your Setup

### Create Test Repositories

{{% alert title="Note" color="primary" %}}
Please Note that these Repositories must already exist on the Git side. if they dont please create them before pointing the porch repository resources at them.
{{% /alert %}}

Create a `porch-repositories.yaml`:

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: external-blueprints
  namespace: porch-demo
spec:
  type: git
  content: Package
  git:
    repo: https://github.com/nephio-project/free5gc-packages.git
---
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: management
  namespace: porch-demo
spec:
  type: git
  content: Package
  git:
repo: http://localhost:3000/porch/management.git
or
repo: http://gitea.gitea.svc.cluster.local:3000/porch/management.git
```

Apply:

```bash
kubectl create namespace porch-demo
kubectl apply -f porch-repositories.yaml
```

Verify:

```bash
porchctl repo get -A
kubectl get repositories -n porch-demo
```

## Running Tests

### Unit Tests

```bash
make test
```

### End-to-End Tests

```bash
# Against current cluster
make test-e2e
# Cli tests against current cluster
make test-e2e-cli
# With clean deployment
make test-e2e-clean
```

### Repository Controller Tests

The Repository Controller has its own test infrastructure in `controllers/repositories/`:

```bash
# Run all repository controller tests
cd controllers/repositories
make test-all

# Run only unit tests
make test-unit

# Run only integration tests
make test-integration
```

### Single Test Case

Run specific tests from the command line:

```bash
# API test
E2E=1 go test -v ./test/e2e -run TestE2E/PorchSuite/TestPackageRevisionInMultipleNamespaces

# CLI test
E2E=1 go test -v ./test/e2e/cli -run TestPorch/rpkg-lifecycle
```

### Debug E2E Tests in VS Code

Debug individual test cases with breakpoints:

**API Tests:** Use the **Launch E2E test** configuration

1. Open `.vscode/launch.json`
2. Update the test name in the `Launch E2E test` configuration:
   ```json
   "args": [
       "-test.v",
       "-test.run",
       "TestE2E/PorchSuite/TestPackageRevisionInMultipleNamespaces"
   ]
   ```
3. Select **Run and Debug** → **Launch E2E test**

**CLI Tests:** Use the **Launch E2E CLI tests** configuration

1. Open `.vscode/launch.json`
2. Update the test name in the `Launch E2E CLI tests` configuration:
   ```json
   "args": [
       "-test.v",
       "-test.failfast",
       "-test.run",
       "TestPorch/rpkg-clone"
   ]
   ```
3. Select **Run and Debug** → **Launch E2E CLI tests**

## Troubleshooting

### Server Not Connecting to Function Runner

Set the correct function runner IP in your launch configuration:

```bash
# Get the function runner IP
kubectl get svc -n porch-system function-runner -o jsonpath='{.status.loadBalancer.ingress[0].ip}'

# Set the FUNCTION_RUNNER_IP environment variable (used by launch.json)
export FUNCTION_RUNNER_IP=$(kubectl get svc -n porch-system function-runner -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
```

### E2E Tests Failing

Clear the git cache before running tests:

```bash
rm -rf .cache/git/*
go clean -cache
```

### Clean Slate Restart

If your cluster becomes unstable:

```bash
# Delete cluster
kind delete cluster --name porch-test

# Recreate
make setup-dev-env

# Redeploy Porch
make run-in-kind
```

## Advanced Debugging

### Debug Porchctl Commands

Debug porchctl CLI commands with breakpoints:

**VS Code launch configuration:** `Run Porchctl command`

1. Open `.vscode/launch.json`
2. Update the `Run Porchctl command` configuration with your desired command:
   ```json
   "args": [
       "rpkg", "init", "my-package",
       "--workspace=v1",
       "--namespace=porch-demo",
       "--repository=management"
   ]
   ```
3. Select **Run and Debug** → **Run Porchctl command**

This allows you to step through porchctl code execution and debug CLI behavior.

{{% alert title="Note" color="primary" %}}
Please Note that for this to work as you are debugging the CLI commands themselfs and not the server's internal workings when the command occurs you must use the debugger in `pkg/cli/commands` and apply breakpoints there instead of in the server's code.
{{% /alert %}}

### Enable Race Condition Detection

The `Launch Server` configuration supports Go's race detector for finding concurrency issues:

1. Install build tools (Linux: `sudo apt install build-essential`)
2. Uncomment in `.vscode/launch.json`:
   ```json
   "env": {
       "CGO_ENABLED": "1"
   },
   "buildFlags": "-race"
   ```

{{% alert title="Note" color="primary" %}}
Race detection significantly slows down Porch operations.
{{% /alert %}}
