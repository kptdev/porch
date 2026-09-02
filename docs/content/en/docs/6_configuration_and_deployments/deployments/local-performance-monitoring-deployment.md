---
title: "Local Performance Monitoring Deployment"
type: docs
weight: 4
description: Deploy Prometheus, Grafana, Grafana Alloy, Pyroscope and Jaeger for local Porch performance monitoring
---

This guide deploys a local observability stack against a Kind based Porch cluster. Use it to inspect API metrics, resource usage, traces, profiles, and [performance test]({{% relref "/docs/12_contributing/code-contribution/performance-tests" %}}) results.

{{% alert title="Note" color="primary" %}}
This stack is intended for local development and performance testing. For production telemetry, configure exporters directly on Porch components as described in [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}).
{{% /alert %}}

## Prerequisites

- A running Kind cluster with Porch deployed. See [Local Development Environment]({{% relref "local-dev-env-deployment" %}}).
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kpt CLI](https://kpt.dev/installation/)
- [make](https://www.gnu.org/software/make/)

Verify Porch is running before deploying monitoring:

```bash
kubectl get pods -n porch-system
```

## Deploy the Monitoring Stack

All make targets run from the repository root. Deployments are created in the `porch-monitoring` namespace. Port-forwarding starts automatically after a successful deploy.

### Base Stack

```bash
make deploy-monitoring
```

Creates `porch-monitoring` and deploys:

| Component | Purpose |
|-----------|---------|
| **Prometheus** | Scrapes Porch component metrics on port 9464, PostgreSQL metrics from postgres-exporter, cAdvisor container metrics, and performance-test metrics on host `172.17.0.1:9095` |
| **Grafana** | Pre-loaded with Porch dashboards (see [Grafana Dashboards](#grafana-dashboards)) |
| **Postgres Exporter** | Exports PostgreSQL metrics from `porch-postgresql` (used when Porch runs with [DB cache]({{% relref "/docs/6_configuration_and_deployments/configurations/cache" %}})) |

### Optional: Jaeger

```bash
make deploy-monitoring-jaeger
```

Deploys Jaeger and enables OTLP trace export on `porch-server`, `function-runner`, and `porch-controllers`. Trace environment variables are documented in [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}).

### Optional: Pyroscope and Alloy

```bash
make deploy-monitoring-pyroscope
```

Deploys the continuous-profiling stack:

| Component | Purpose |
|-----------|---------|
| **Pyroscope** | Stores profiles and serves the UI at http://localhost:4040 |
| **Grafana Alloy** | Discovers Porch pods, scrapes their pprof endpoints, and writes profiles into Pyroscope |

Alloy has no UI of its own. It watches pods in the cluster, keeps those that have `profiles.grafana.com/*` annotations, scrapes the named `pprof` container port, and forwards CPU, memory (heap), goroutine, block, and mutex profiles to Pyroscope at `http://pyroscope.porch-monitoring.svc.cluster.local:4040`.

`make deploy-monitoring-pyroscope` also enables pprof on `porch-server`, `function-runner`, and `porch-controllers` by setting `PORCH_PPROF_PORT` and adding the Alloy discovery annotations. The environment variable, annotations, and available profiles are documented in [Pprof Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry#pprof-configuration" %}}).

### Cleanup and Restart

```bash
make cleanup-monitoring          # remove monitoring resources and disable trace/pprof export
make restart-monitoring          # cleanup, then redeploy the base stack
```

## Access the UIs

| Service | URL | Notes |
|---------|-----|-------|
| Prometheus | http://localhost:9092 | Query raw metrics |
| Grafana | http://localhost:3001 | Dashboards listed below |
| Jaeger | http://localhost:16686 | After `make deploy-monitoring-jaeger` |
| Pyroscope | http://localhost:4040 | After `make deploy-monitoring-pyroscope`. Alloy scrapes Porch pods and writes profiles here; Alloy itself has no UI |

Grafana credentials are printed when the stack is deployed. The default username is `porch`. The password is stored in the `grafana-admin-creds` secret:

```bash
kubectl -n porch-monitoring get secret grafana-admin-creds \
  -o jsonpath='{.data.GF_SECURITY_ADMIN_PASSWORD}' | base64 -d
echo
```

## Grafana Dashboards

Open Grafana at http://localhost:3001 and select a dashboard. The following dashboards are loaded automatically:

| Dashboard | What it shows |
|-----------|----------------|
| **Porch API** | API call latency (p95, p99, average) for PackageRevision, PackageRevisionResources, and Approval operations; ExternalRepo FETCH/PUSH latency; request rates by user |
| **Porch Resource Usage** | CPU and memory for porch-server, function-runner, porch-controllers, PostgreSQL, and function pods; controller workqueue depth, reconcile rate, and p99 reconcile duration |
| **Porch PostgreSQL** | Query activity, row operations, QPS, cache hit ratio, connections, buffers, and conflicts/deadlocks (DB cache deployments) |
| **Porch File-system Resources** | Package revision resource sizes (`porch_package_size_bytes_total`) |
| **Porch Performance Test Dashboard** | Per-operation duration, count, and success rate from performance tests (`porch_perf_*` metrics) |
| **Pyroscope – Porch profiling** | Flame graphs for CPU, memory, goroutine, block, and mutex profiles that Alloy scraped from Porch pods |

Server-side metric names and attributes are listed in [Available Metrics]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry#available-metrics" %}}). Performance-test metric names are listed in [Performance Tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests#performance-test-metrics" %}}).

## What Prometheus Scrapes

| Target | Address | Metrics |
|--------|---------|---------|
| porch-server | `api.porch-system.svc.cluster.local:9464` | Porch API and package-size metrics |
| porch-controllers | `porch-controllers.porch-system.svc.cluster.local:9464` | Controller API metrics and controller-runtime metrics |
| function-runner | `function-runner.porch-system.svc.cluster.local:9464` | Function-runner metrics |
| postgres-exporter | `postgres-exporter.porch-monitoring.svc.cluster.local:9187` | PostgreSQL metrics |
| kubelet cAdvisor | Kubernetes node proxy | Container CPU and memory |
| Performance tests | `172.17.0.1:9095` | `porch_perf_*` metrics when tests run with `-enable-prometheus=true` |

The performance-test process listens on **port 9095** on the host. Prometheus inside Kind reaches it through the Docker bridge gateway (`172.17.0.1`). Only set `-enable-prometheus=true` when this stack is deployed; the test waits 15 seconds before shutting down its metrics server so Prometheus can scrape final values.

## Next Steps

- Configure exporters, pprof, and metric names: [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}})
- Run load and soak tests: [Performance Tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests" %}})
