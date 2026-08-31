---
title: "Performance Tests"
type: docs
weight: 2
description: Run Porch package revision lifecycle performance and load tests
---

Performance tests measure Porch package revision lifecycle latency and throughput under configurable load. They create Gitea and Porch repositories, run package revisions through the full lifecycle (create → update resources → propose → approve/publish), optionally delete them, and record per-operation timings plus optional Prometheus metrics.

For the local Prometheus, Grafana, Jaeger, Pyroscope, and Grafana Alloy stack used with these tests, see [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}). Server-side metrics and pprof configuration are documented in [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}).

## Prerequisites

- Docker
- Kubernetes CLI (`kubectl`)
- [kpt CLI](https://kpt.dev/installation/)
- Go development environment
- A running Porch deployment with Gitea (see [Set Up the Development Environment](#set-up-the-development-environment))

## Set Up the Development Environment

From the repository root, set up the Kind cluster, Gitea, and test repository:

```bash
make setup-dev-env
```

This matches the [Local Development Environment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-dev-env-deployment" %}}) Kind setup.

## Build and Deploy Porch

Build and deploy Porch into a Kind cluster. Choose the target that matches the API version and cache backend you want to test:

| Make target | Cache | v1alpha2 | Notes |
|-------------|-------|----------|-------|
| `make run-in-kind` | CR | no | Default; uses v1alpha1 PackageRevision API |
| `make run-in-kind-db-cache` | DB (PostgreSQL) | no | Database-backed cache |
| `make run-in-kind-v1alpha2` | DB | yes | Required for `-api-version=v1alpha2` tests |
| `make run-in-kind-v1alpha2-no-controller` | DB | yes | Exposes function-runner; run the controller locally |

```bash
# v1alpha1 (default)
make run-in-kind

# v1alpha2 with DB cache
make run-in-kind-v1alpha2
```

Verify pods are running in `porch-system` before starting tests.

To tear down:

```bash
make destroy
```

## Deploy Monitoring (Optional)

Deploy the monitoring stack before running tests with `-enable-prometheus=true`. See [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}) for make targets, UI URLs, and Grafana dashboards.

The test process exposes OpenTelemetry metrics on **port 9095** on the host. Prometheus inside Kind scrapes them via the Docker gateway IP (`172.17.0.1:9095`).

{{% alert title="Warning" color="warning" %}}
Only enable `-enable-prometheus=true` when the monitoring stack is deployed. The test waits 15 seconds before shutting down its metrics server so Prometheus can scrape final values.
{{% /alert %}}

## Test Packages

Three packages are available under `test/performance/packages/`. Select one with `-package-path`:

| Package | Path | Description |
|---------|------|-------------|
| **Small** (default) | `packages/small-package` | Single Deployment; `set-namespace` + `apply-setters` |
| **Complex** | `packages/complex-package` | 10 KRM resource files; 15 kpt catalog functions (mutators + validators) |
| **Large** | `packages/large-package` | 30 microservice Deployments in a single ~550 KB manifest; 15 kpt catalog functions |

### Large Package Requirements

The large package generates significantly larger request payloads than the default 6 MB `--max-request-body-size` (set on both porch-server and function-runner). **Only use `packages/large-package` when:**

1. **Porch server, function-runner, and porch-controllers have adequate CPU and memory requests/limits** for rendering and reconciling large packages with a full kpt pipeline (15 functions across 30 Deployments).
2. **`--max-request-body-size` is increased significantly** on both porch-server and function-runner (they must stay in sync). The default is `6291456` (6 MB) in `deployments/porch/3-porch-server.yaml` and `deployments/porch/2-function-runner.yaml`.

Default Kind deployment resource profiles:

| Component | CPU request | CPU limit | Memory request | Memory limit |
|-----------|-------------|-----------|----------------|--------------|
| porch-server | 250m | — | 256Mi | 512Mi |
| function-runner | 125m | — | 64Mi | — |
| porch-controllers | 500m | 1000m | 512Mi | 1Gi |

These defaults are suitable for small and complex packages. Large-package workloads typically need higher limits across all three components.

## Run Performance Tests

Tests are gated by environment variables and skipped otherwise. Run from the repository root or from `test/performance/`:

```bash
cd test/performance/
```

### Scale / Load Test (`TestPorchScalePerformance`)

Creates a configured number of repositories, packages, and revisions to simulate load. Runs repositories and packages in parallel up to the configured parallelism limits.

```bash
LOAD_TEST=1 go test -v ./... -timeout 1h
```

Example with custom parameters:

```bash
LOAD_TEST=1 go test -v ./... \
  -namespace=porch-metrics \
  -api-version=v1alpha1 \
  -repos=2 \
  -packages=3 \
  -revisions=5 \
  -repo-parallelism=2 \
  -package-parallelism=2 \
  -package-path=packages/complex-package \
  -enable-prometheus=true \
  -enable-deletion=true \
  -timeout 2h
```

### Maximum Package Revisions Test (`TestIncreasePRsPerformance`)

Creates package revisions sequentially in a single repository until the error-rate threshold is exceeded. Designed for long-running soak tests.

```bash
MAX_PR_TEST=1 go test -v ./... -timeout 72h
```

This test uses `-error-rate` (default `0.1`, i.e. 0.1%) to decide when to stop: once the fraction of failed revisions reaches the threshold, the test ends. Use a long timeout (72h or more is recommended).

### API Versions

| Version | Deploy target | Lifecycle driver |
|---------|---------------|------------------|
| `v1alpha1` (default) | `make run-in-kind` or `make run-in-kind-db-cache` | Direct lifecycle transitions via API |
| `v1alpha2` | `make run-in-kind-v1alpha2` | Controller-reconciled lifecycle with wait steps |

v1alpha2 additionally records **Wait Ready**, **Wait Rendered**, and **Wait Published** operation timings while the controller reconciles each revision.

Select the API version with `-api-version=v1alpha1` or `-api-version=v1alpha2`.

### What Each Test Does

For every repository (`{namespace}-test-{N}`):

1. Create a Gitea repository
2. Create a Porch `Repository` CR and wait for Ready
3. For each package (`network-function-{N}`) and revision:
   - List existing package revisions
   - Create a new package revision (init on v1, copy-from-published on subsequent revisions)
   - Update package revision resources from the selected test package
   - Propose → approve/publish (v1alpha2 waits for controller reconciliation between steps)
4. Optionally delete all created package revisions (`-enable-deletion=true`)

Tests handle `SIGINT`/`SIGTERM` gracefully: in-flight work stops and results collected so far are written.

### Test Parameters

| Flag | Default | Description |
|------|---------|-------------|
| `-namespace` | `porch-metrics` | Kubernetes namespace for test resources |
| `-api-version` | `v1alpha1` | Porch API version (`v1alpha1` or `v1alpha2`) |
| `-repos` | `1` | Number of repositories |
| `-packages` | `1` | Packages per repository |
| `-revisions` | `1` | Revisions per package (ignored by MAX_PR_TEST, which runs until error threshold) |
| `-repo-parallelism` | `1` | Repositories created in parallel |
| `-package-parallelism` | `1` | Packages created in parallel per repository |
| `-package-path` | `packages/small-package` | Path to package resources directory |
| `-error-rate` | `0.1` | Max failure percentage before MAX_PR_TEST stops (0.1 = 0.1%) |
| `-enable-deletion` | `false` | Delete all package revisions after lifecycle test |
| `-enable-prometheus` | `false` | Expose OTel metrics on host port 9095 |
| `-metrics-log-prefix` | `porch-metrics` | Prefix for timestamped log in `logs/` |
| `-results-file` | `load_test_results.txt` | Approved/deleted revision summary |
| `-detailed-log-file` | `load_test.log` | Per-operation detailed log |
| `-repo-results-csv` | `load_test_lifecycle_results.csv` | Per-revision lifecycle duration CSV |
| `-operations-csv` | `load_test_operations_results.csv` | Per-operation timing CSV |
| `-deletion-csv` | `load_test_deletion_results.csv` | Deletion operation CSV |
| `-gitea-url` | `http://localhost:3000` | Gitea API base URL |
| `-gitea-username` | `porch` | Gitea username |
| `-gitea-password` | `secret` | Gitea password |

The KRM function registry URL is configured via `PORCH_GHCR_PREFIX_URL` in the repo root `.env` file. It is applied at deploy time to porch-server, function-runner, and porch-controllers (`make run-in-kind`, `make run-in-kind-db-cache`, and `make run-in-kind-v1alpha2` all read `.env` automatically via `make deployment-config`). Package `Kptfile` images use short names (for example `set-namespace:v0.4.1`); porch-server and function-runner resolve them with `--default-image-prefix`, and controllers use the `DEFAULT_IMAGE_PREFIX` environment variable. The `CHANGE_NAMESPACE` placeholder in Kptfiles is substituted at test runtime.

## Output Files

Results are written relative to the working directory (typically `test/performance/`):

| Output | Location | Contents |
|--------|----------|----------|
| Timestamped test log | `logs/{prefix}-{api-version}-{timestamp}.log` | Consolidated results table |
| Results summary | `load_test_results.txt` (configurable) | Approved and deleted revision timings |
| Detailed log | `load_test.log` (configurable) | Per-operation log with configuration header |
| Lifecycle CSV | `load_test_lifecycle_results.csv` | Per package-revision total lifecycle duration |
| Operations CSV | `load_test_operations_results.csv` | Per-operation min/max/avg/total |
| Deletion CSV | `load_test_deletion_results.csv` | Deletion timings (when `-enable-deletion=true`) |

## Sample Output

```bash
LOAD_TEST=1 go test -v ./... \
  -namespace=porch-metrics \
  -repos=1 -packages=1 -revisions=3 \
  -enable-prometheus=true \
  -enable-deletion=true \
  -timeout 1h
```

```
=== Consolidated Performance Test Results (v1alpha1) ===
Operation                              Min         Max         Avg         Total
------------------------------------------------------------------------------------
Create Gitea Repository   R0          272ms       272ms       272ms       272ms
Create Porch Repository   R0          3ms         3ms         3ms         3ms
Repository Ready Wait  R0             2.004s      2.004s      2.004s      2.004s
Package Revision List v1              9ms         9ms         9ms         9ms
Package Revision Create v1            10ms        10ms        10ms        10ms
Package Revision Get Resources v1     3ms         3ms         3ms         3ms
Package Revision Update v1            10ms        10ms        10ms        10ms
Package Revision Get v1               2ms         2ms         2ms         2ms
Package Revision Propose v1           11ms        11ms        11ms        11ms
Package Revision Get (Proposed) v1    2ms         2ms         2ms         2ms
Package Revision Approve/Publish v1   348ms       348ms       348ms       348ms
Package Revision Propose Deletion v1  8ms         8ms         8ms         8ms
Package Revision Delete v1            262ms       262ms       262ms       262ms
...
Total lifecycle duration for all operations: 12.5s
Tests completed!
```

With `-api-version=v1alpha2`, additional rows appear for **Wait Ready**, **Wait Rendered**, and **Wait Published** operations.

## Performance Test Metrics

When `-enable-prometheus=true`, the test process exports the following OpenTelemetry metrics on `localhost:9095/metrics`. They appear on the **Porch Performance Test Dashboard** in Grafana.

### Instruments

| Metric name | Type | Unit | Description |
|-------------|------|------|-------------|
| `porch_perf_operation_duration_seconds` | Histogram | seconds | Duration of each performance-test operation |
| `porch_perf_operations_total` | Counter | — | Total number of performance-test operations |
| `porch_perf_repositories_created_total` | Counter | — | Repositories created during the test |
| `porch_perf_packages_created_total` | Counter | — | Packages created during the test |
| `porch_perf_package_revisions_total` | Counter | — | Package revisions created during the test |
| `porch_perf_lifecycle_transition_duration_seconds` | Histogram | seconds | Duration of package lifecycle transitions |
| `porch_perf_test_run_info` | Gauge | — | Current test-run metadata (value is always `1`) |
| `porch_perf_active_operations` | UpDownCounter | — | Number of currently in-flight operations |

### Attributes

| Metric | Attributes |
|--------|------------|
| `porch_perf_operation_duration_seconds`, `porch_perf_operations_total` | `operation`, `api_version`, `repository`, `package`, `status` (`success` or `error`) |
| `porch_perf_package_revisions_total` | `operation`, `status` |
| `porch_perf_lifecycle_transition_duration_seconds` | `from_state`, `to_state`, `api_version`, `repository`, `package`, `status` |
| `porch_perf_test_run_info` | `test_name`, `namespace`, `api_version`, `start_time` |
| `porch_perf_active_operations` | `operation`, `api_version` |
| `porch_perf_repositories_created_total`, `porch_perf_packages_created_total` | none |

### Operation Labels

The `operation` attribute uses these keys:

| Key | When recorded |
|-----|----------------|
| `GITEA-REPO-CREATE` | Create Gitea repository |
| `PORCH-REPO-CREATE` | Create Porch `Repository` CR |
| `REPO-WAIT` | Wait for repository Ready |
| `LIST` | List package revisions |
| `CREATE` | Create package revision |
| `GET-RESOURCES` | Get package revision resources |
| `UPDATE` | Update package revision resources |
| `GET` | Get package revision |
| `PROPOSE` | Propose package revision |
| `GET-PROPOSED` | Get package revision after propose |
| `APPROVE` | Approve / publish package revision |
| `PROPOSE-DELETION` | Propose deletion (when `-enable-deletion=true`) |
| `DELETE` | Delete package revision (when `-enable-deletion=true`) |
| `WAIT-READY` | v1alpha2: wait until the controller marks the revision Ready |
| `WAIT-RENDERED` | v1alpha2: wait until the controller finishes rendering |
| `WAIT-PUBLISHED` | v1alpha2: wait until the controller publishes the revision |

Histogram bucket boundaries:

- `porch_perf_operation_duration_seconds`: 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120 seconds
- `porch_perf_lifecycle_transition_duration_seconds`: 0.1, 0.5, 1, 2, 5, 10, 30, 60 seconds

### Server-Side Metrics During a Test Run

While tests run, Prometheus also scrapes Porch components on port 9464. Those series (`porch_api_call_duration_seconds`, `porch_api_requests_by_user`, `porch_package_size_bytes`, and `porch_package_size_bytes_total`) are described in [Available Metrics]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry#available-metrics" %}}). Use the **Porch API** and **Porch File-system Resources** Grafana dashboards alongside the performance-test dashboard.

## See Also

- [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}) — Prometheus, Grafana, Jaeger, Pyroscope, and Grafana Alloy
- [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}) — exporters, pprof, and server metrics
- [Development Environment]({{% relref "development-environment" %}}) — local Kind setup and debugging
