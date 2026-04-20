# CRD (v1alpha2) E2E Tests

Ginkgo-based end-to-end tests for the Porch v1alpha2 CRD API.

## Prerequisites

- A running Porch cluster (kind or otherwise) with Gitea deployed
- `E2E=1` environment variable set
- `kubeconfig` pointing at the target cluster

## Running the Suite

### Core tests (excludes migration)
```bash
make test-e2e-crd
```

### Migration tests only
```bash
make test-e2e-crd-migration
```

### All tests
```bash
make test-e2e-crd-all
```

### Clean run (fresh kind cluster + deploy + core tests)
```bash
make test-e2e-crd-clean
```

## Selecting Tests

### Run a single Describe block (e.g. Lifecycle)
```bash
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.focus="Lifecycle"
```

### Run a specific It spec
```bash
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.focus="should clone from upstream ref"
```

### Run specs matching a regex
```bash
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.focus="Clone.*Upgrade"
```

### Skip specs matching a regex
```bash
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.skip="Metrics"
```

### Combine focus and skip
```bash
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.focus="Lifecycle" -ginkgo.skip="subfolder"
```

## Useful Ginkgo Options

| Flag | Description |
|------|-------------|
| `-ginkgo.v` | Verbose output (prints each spec name) |
| `-ginkgo.vv` | Very verbose (includes GinkgoWriter output) |
| `-ginkgo.focus="regex"` | Only run specs matching the regex |
| `-ginkgo.skip="regex"` | Skip specs matching the regex |
| `-ginkgo.fail-fast` | Stop on first failure |
| `-ginkgo.dry-run` | List specs without running them |
| `-ginkgo.label-filter="expr"` | Filter by Ginkgo labels |
| `-ginkgo.timeout=10m` | Override suite timeout |
| `-ginkgo.seed=N` | Set randomization seed (0 = sequential) |
| `-ginkgo.no-color` | Disable colored output |

## Test Structure

The suite uses a shared `BeforeSuite` that:
1. Cleans up stale namespaces from prior runs (`crd-e2e-*`)
2. Creates a unique namespace per run
3. Registers `porch-test` and `test-blueprints` Gitea repos

All `Describe` blocks are `Ordered`, so specs within each block run sequentially.

### Available Describe Blocks

| Block | File | Label | Description |
|-------|------|-------|-------------|
| Clone | `clone_test.go` | `lifecycle` | Clone from upstream, git URL, bearer token, upgrades |
| Copy | `copy_test.go` | `lifecycle` | Copy packages between workspaces |
| Init | `init_test.go` | `lifecycle` | Package initialization |
| Lifecycle | `lifecycle_test.go` | `lifecycle` | Draft → Proposed → Published transitions, deletion |
| Push | `push_test.go` | `content` | PRR resource updates, render pipelines |
| Render | `render_test.go` | `content` | Render failures, recovery, stale detection |
| PRR Edge Cases | `prr_edge_cases_test.go` | `content` | File deletion, bad Kptfile handling |
| Concurrency | `concurrent_test.go` | `concurrency` | Optimistic locking, concurrent PRR pushes |
| Repository | `repository_test.go` | `infra` | Registration, sync, discovery, isolation |
| Metadata | `metadata_test.go` | `infra` | Labels, annotations, field selectors, readiness gates, finalizers |
| Metrics | `metrics_test.go` | `infra` | Prometheus metrics exposure |
| Validation | `validation_test.go` | `infra` | Webhook validation rules |
| Resilience | `resilience_test.go` | `infra` | Controller restart recovery |
| Webhook Validation | `resilience_test.go` | `infra` | Placeholder specs for Issue 9 (propose-during-render, immutability) |
| Migration | `migration_test.go` | `migration` | v1alpha1→v1alpha2 annotation flip, rollback, orphan cleanup |
| SideBySide | `side_by_side_test.go` | `migration` | API isolation, shared PRR, cross-version rejection, deletion isolation |

> **Note**: Migration and SideBySide tests are labelled `migration` and excluded from the default
> `make test-e2e-crd` run because they register v1alpha1 repos alongside v1alpha2 repos.
> Run them separately with `make test-e2e-crd-migration`, or run everything with `make test-e2e-crd-all`.

## Filtering by Label

```bash
# Run a single label group
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='lifecycle'
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='content'
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='infra'
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='concurrency'
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='migration'

# Combine labels
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='lifecycle || content'

# Exclude a label
E2E=1 go test -v ./test/e2e/crd -ginkgo.v -ginkgo.label-filter='!migration'

# Everything (no filter)
E2E=1 go test -v ./test/e2e/crd -ginkgo.v
```

## Running Locally Against a Remote Cluster

If porch-server or controllers are running locally (not in-cluster), set:
```bash
export RUN_E2E_LOCALLY=true
```
This tells the suite to reach Gitea via the LoadBalancer IP instead of cluster-internal DNS.

## List All Specs (Dry Run)
```bash
E2E=1 go test -v ./test/e2e/crd -ginkgo.dry-run -ginkgo.v
```
