---
title: "OTEL E2E Testing"
type: docs
weight: 5
description: Run OTEL E2E tests to validate trace and metrics export from all Porch components
---

This guide explains how to run the automated OTEL E2E test locally and in CI to validate that Porch components (porch-server, porch-controllers, porch-function-runner) correctly export traces and metrics to observability backends.

## Overview

The OTEL E2E testing validates:
- ✅ All Porch components export traces to Jaeger
- ✅ All components export metrics to Prometheus
- ✅ OTEL SDK initializes without errors
- ✅ Real Porch operations are properly instrumented

**Scope**: Single unified v1alpha2 CRD test suite
- Both v1alpha1 and v1alpha2 APIs use the same underlying server and controller components
- No separate v1alpha1 test needed; a single v1alpha2 suite validates telemetry export from shared components
- Uses lightweight "Init" specs that exercise core operations without heavy rendering overhead

## Prerequisites

- Running Kind cluster with Porch deployed (see [Local Development Environment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-dev-env-deployment" %}}))
- kubectl configured to access the cluster
- `jq` command-line JSON processor (for validation output)

## Local Testing

### Run OTEL E2E Test

Single command deploys Porch, monitoring stack, runs E2E test, and validates OTEL:

```bash
make test-e2e-otel
```

This:
1. Deploys v1alpha2 stack with DB cache
2. Deploys monitoring (Prometheus, Grafana, Jaeger)
3. Runs lightweight E2E test (~20-30 sec) to exercise components
4. Validates traces/metrics from all 3 components
5. **Leaves deployment running** for manual debugging (see [Access Observability UIs](#access-observability-uis))

**Total time**: ~25-30 minutes

### Validation Steps

The `validate-otel.sh` script checks:
1. ✅ Jaeger pod is running
2. ✅ porch-server metrics endpoint responds (port 9464)
3. ✅ OTEL initialized in logs (no critical errors)
4. ✅ Traces exist in Jaeger (with retries)
5. ✅ Prometheus has scraped all 3 components
6. ✅ Lists all components exporting traces

Example output:
```
=== OTEL Validation Checks ===
=== 1. Checking Jaeger availability ===
✓ Jaeger pod found: jaeger-66f5c97cb8-skllc
=== 2. Checking porch-server metrics endpoint ===
✓ Metrics endpoint responding
=== 6. Services reporting traces to Jaeger ===
✓ Porch components exporting traces:
  - porch-server
  - porch-controllers
  - porch-function-runner
```

## Access Observability UIs

While the test stack is running, access services via port-forward:

### Jaeger (Distributed Tracing)

```bash
# Terminal 1
kubectl port-forward -n porch-monitoring deployment/jaeger 16686:16686

# Terminal 2 - visit http://localhost:16686
# Query service: "porch-server", "porch-controllers", or "porch-function-runner"
```

### Prometheus (Metrics)

```bash
# Terminal 1
kubectl port-forward -n porch-monitoring deployment/prometheus 9092:9090

# Terminal 2 - visit http://localhost:9092
# Query: http_server_requests_total, grpc_server_requests_total
```

### Grafana (Dashboards)

```bash
# Terminal 1
kubectl port-forward -n porch-monitoring deployment/grafana 3001:3000

# Terminal 2 - visit http://localhost:3001
# User: porch
# Password: check logs or extract from secret
```

## CI Integration

### GitHub Actions Workflow

The OTEL E2E tests run automatically in GitHub Actions.

**File**: `.github/workflows/porch-e2e-otel-weekly.yaml`

**Triggers**:
- **Scheduled**: Every Sunday at 2 AM UTC
- **Manual**: Trigger via GitHub Actions UI (`workflow_dispatch`)

**What it does**:
1. Builds Porch images (parallel)
2. Deploys v1alpha2 stack
3. Deploys monitoring
4. Runs lightweight E2E test
5. Validates OTEL infrastructure
6. Reports which components export traces/metrics

**Duration**: ~45 minutes total

## Test Selection

**Init tests** (v1alpha2 CRD):
- **Operations**: Create and initialize packages
- **Why**: Lightweight, exercises core package operations, no rendering overhead
- **Duration**: ~20-30 seconds
- **Traces generated**: API calls, package initialization, status updates
- **Function-runner coverage**: While Init tests don't invoke KRM functions, function-runner still exports traces/metrics from its server (always running, responding to readiness/liveness probes)

This single test suite is sufficient to verify that:
- All components are healthy and receiving traffic
- All components export traces and metrics
- OTEL pipeline works end-to-end
- Server-level instrumentation is working (even if KRM functions aren't invoked)

## Make Targets

```bash
# Deploy + test + validate + leave running for debugging
make test-e2e-otel
```

## Troubleshooting

### No traces in Jaeger

1. **Check Porch components are running**:
   ```bash
   kubectl get pods -n porch-system
   ```

2. **Check OTEL env vars are set**:
   ```bash
   kubectl get deployment porch-server -n porch-system \
     -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")].value}'
   ```
   Should output: `http://jaeger-otlp.porch-monitoring.svc.cluster.local:4317`

3. **Check Jaeger OTLP endpoint is reachable**:
   ```bash
   kubectl logs -n porch-system deployment/porch-server | grep -i "jaeger\|otlp\|export"
   ```

### Metrics endpoint not responding

1. **Check porch-server pod is healthy**:
   ```bash
   kubectl get pod -n porch-system -l app=porch-server
   kubectl logs -n porch-system -l app=porch-server | grep "ERROR\|error"
   ```

2. **Verify port-forward is working**:
   ```bash
   kubectl port-forward -n porch-system deployment/porch-server 9464:9464 &
   curl http://localhost:9464/metrics | head -10
   ```

### Validation script fails

- **Prometheus metrics query fails**: Metrics may still be collecting. Check manually at `http://localhost:9092` (Prometheus UI)
- **No services in Jaeger**: E2E test may not have exercised components. Check test logs for errors
- **Export errors in logs**: Usually transient (connection timeout during startup). Check if components eventually recover

## Cleanup

```bash
# Remove monitoring stack
./scripts/monitoring/deploy-monitoring.sh remove

# Destroy Porch deployment
make destroy
```

## Next Steps

- Configure OTEL exporters: [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}})
- Deploy monitoring stack: [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}})
- Run performance tests: [Performance Tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests" %}})
