# Porch Performance Testing

Performance tests measure Porch package revision lifecycle latency and throughput under configurable load. They create Gitea and Porch repositories, run package revisions through the full lifecycle (create → update resources → propose → approve/publish), optionally delete them, and record per-operation timings plus optional Prometheus metrics.

## Prerequisites

- Docker
- Kubernetes CLI (`kubectl`)
- [kpt CLI](https://kpt.dev/installation/)
- Go development environment
- A running Porch deployment with Gitea (see setup below)

## 1. Set Up Development Environment

From the repository root, set up the Kind cluster, Gitea, and test repository:

```bash
make setup-dev-env
```

Or run the script directly:

```bash
./scripts/setup-dev-env.sh
```

## 2. Build and Deploy Porch

Build and deploy Porch into a Kind cluster. Choose the target that matches the API version and cache backend you want to test:

| Make target | Cache | v1alpha2 | Notes |
|---|---|---|---|
| `make run-in-kind` | CR | no | Default; uses v1alpha1 PackageRevision API |
| `make run-in-kind-db-cache` | DB (PostgreSQL) | no | Database-backed cache |
| `make run-in-kind-v1alpha2` | DB | yes | Required for `-api-version=v1alpha2` tests |
| `make run-in-kind-v1alpha2-no-controller` | DB | yes | Exposes function-runner; run controller locally |

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

## 3. Deploy Monitoring Stack (Optional)

Deploy the monitoring stack before running tests with `-enable-prometheus=true`. The test process exposes OTel metrics on **port 9095** on the host; Prometheus inside the Kind cluster scrapes them via the Docker gateway IP (`172.17.0.1:9095`).

### Base stack (Prometheus, Grafana, Postgres Exporter)

```bash
make deploy-monitoring
# or
./scripts/deploy-monitoring.sh deploy
```

This creates the `porch-monitoring` namespace and deploys:

- **Prometheus** — scrapes porch-server, porch-controllers, function-runner (port 9464), postgres-exporter, and perf-test metrics (host `172.17.0.1:9095`)
- **Grafana** — pre-loaded with the Porch performance dashboard
- **Postgres Exporter** — PostgreSQL metrics from `porch-postgresql`

Port-forwarding is started automatically:

| Service | URL |
|---|---|
| Prometheus | http://localhost:9092 |
| Grafana | http://localhost:3001 |

Grafana credentials are printed on deploy (default user: `porch`). They are also stored in the `grafana-admin-creds` secret in `porch-monitoring`.

### Optional: Jaeger (distributed tracing)

Deploys Jaeger and enables OTLP trace export on porch-server, function-runner, and porch-controllers:

```bash
make deploy-monitoring-jaeger
# or
./scripts/deploy-monitoring.sh jaeger
```

Jaeger UI: http://localhost:16686

### Optional: Pyroscope (continuous profiling)

Deploys Pyroscope and Grafana Alloy, which discover annotated Porch pods via `profiles.grafana.com/*` annotations:

```bash
make deploy-monitoring-pyroscope
# or
./scripts/deploy-monitoring.sh pyroscope
```

Pyroscope UI: http://localhost:4040

### Cleanup and restart

```bash
make cleanup-monitoring          # remove all monitoring resources
make restart-monitoring          # cleanup + redeploy base stack
# or
./scripts/deploy-monitoring.sh cleanup
./scripts/deploy-monitoring.sh restart
```

**Important:** Only enable `-enable-prometheus=true` when the monitoring stack is deployed. The test waits 15 seconds before shutting down its metrics server so Prometheus can scrape final values.

## 4. Test Packages

Three packages are available under `packages/`. Select one with `-package-path`:

| Package | Path | Description |
|---|---|---|
| **Small** (default) | `packages/small-package` | Single Deployment; `set-namespace` + `apply-setters` |
| **Complex** | `packages/complex-package` | 10 KRM resource files; 15 kpt catalog functions (mutators + validators) |
| **Large** | `packages/large-package` | 30 microservice Deployments in a single ~550 KB manifest; 15 kpt catalog functions |

### Large package requirements

The large package generates significantly larger request payloads than the default 6 MB `--max-request-body-size` (set on both porch-server and function-runner). **Only use `packages/large-package` when:**

1. **Porch server, function-runner, and porch-controllers have adequate CPU and memory requests/limits** for rendering and reconciling large packages with a full kpt pipeline (15 functions across 30 Deployments).
2. **`--max-request-body-size` is increased significantly** on both porch-server and function-runner (they must stay in sync). The default is `6291456` (6 MB) in `deployments/porch/3-porch-server.yaml` and `deployments/porch/2-function-runner.yaml`.

For reference, the default Kind deployment resource profiles are:

| Component | CPU request | CPU limit | Memory request | Memory limit |
|---|---|---|---|---|
| porch-server | 250m | — | 256Mi | 2Gi |
| function-runner | 125m | — | 64Mi | — |
| porch-controllers | 500m | 1000m | 512Mi | 1Gi |

These defaults are suitable for small and complex packages. Large-package workloads typically need higher limits across all three components.

## 5. Run Performance Tests

Tests are gated by environment variables and skipped otherwise. Run from the repository root or from `test/performance/`:

```bash
cd test/performance/
```

### Scale / load test (`TestPorchScalePerformance`)

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

### Maximum package revisions test (`TestIncreasePRsPerformance`)

Creates package revisions sequentially in a single repository until the error-rate threshold is exceeded. Designed for long-running soak tests.

```bash
MAX_PR_TEST=1 go test -v ./... -timeout 72h
```

This test uses `-error-rate` (default `0.1`, i.e. 0.1%) to decide when to stop: once the fraction of failed revisions reaches the threshold, the test ends. Use a long timeout (72h or more is recommended).

### API versions

| Version | Deploy target | Lifecycle driver |
|---|---|---|
| `v1alpha1` (default) | `make run-in-kind` or `make run-in-kind-db-cache` | Direct lifecycle transitions via API |
| `v1alpha2` | `make run-in-kind-v1alpha2` | Controller-reconciled lifecycle with wait steps |

v1alpha2 additionally records **Wait Ready**, **Wait Rendered**, and **Wait Published** operation timings while the controller reconciles each revision.

Select the API version with `-api-version=v1alpha1` or `-api-version=v1alpha2`.

### What each test does

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

### Test parameters

| Flag | Default | Description |
|---|---|---|
| `-namespace` | `porch-metrics` | Kubernetes namespace for test resources |
| `-api-version` | `v1alpha1` | Porch API version (`v1alpha1` or `v1alpha2`) |
| `-repos` | `1` | Number of repositories |
| `-packages` | `1` | Packages per repository |
| `-revisions` | `1` | Revisions per package (ignored by MAX_PR_TEST, which runs until error threshold) |
| `-repo-parallelism` | `1` | Repositories created in parallel |
| `-package-parallelism` | `1` | Packages created in parallel per repository |
| `-package-path` | `packages/small-package` | Path to package resources directory |
| `-padding-size` | `0` | Pad package resources with N MB of data (0 = disabled) |
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

The KRM function registry URL is read from the `PORCH_GHCR_PREFIX_URL` environment variable (or `.env`), defaulting to `gcr.io/kptdev/krm-functions-catalog`. Package `Kptfile` placeholders `CHANGE_NAMESPACE` and `CHANGE_IMAGE` are substituted at runtime.

## 6. Output Files

Results are written relative to the working directory (typically `test/performance/`):

| Output | Location | Contents |
|---|---|---|
| Timestamped test log | `logs/{prefix}-{api-version}-{timestamp}.log` | Consolidated results table |
| Results summary | `load_test_results.txt` (configurable) | Approved and deleted revision timings |
| Detailed log | `load_test.log` (configurable) | Per-operation log with configuration header |
| Lifecycle CSV | `load_test_lifecycle_results.csv` | Per package-revision total lifecycle duration |
| Operations CSV | `load_test_operations_results.csv` | Per-operation min/max/avg/total |
| Deletion CSV | `load_test_deletion_results.csv` | Deletion timings (when `-enable-deletion=true`) |

## 7. Sample Output

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
