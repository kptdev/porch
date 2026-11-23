# Controller sharding

Runs the Porch controllers (Repository + PackageRevision) as a sharded
`StatefulSet`, partitioned by repository. The default deployment
(`deployments/porch/9-controllers.yaml`) is a singleton and unaffected.

## How it works

Sharding is automatic and zero-config:

- **Shard count** = `spec.replicas`, discovered at runtime from the StatefulSet (no flag needed).
- **Shard ID** = derived from the pod ordinal (`porch-controllers-0` → shard 0, `porch-controllers-1` → shard 1).
- **Namespace** = auto-discovered from the ServiceAccount token injected into every pod.

Scale by changing replicas alone; every pod re-derives the shard count and namespace at startup:

```bash
kubectl -n porch-system scale statefulset porch-controllers --replicas=N
```

If the StatefulSet is replaced with an older Deployment, sharding gracefully degrades: namespace discovery fails, sharding disables, and the controller runs as a single non-sharded instance.

PackageVariant/PackageVariantSet are not enabled on these pods (not
repository-keyed; run them separately if needed).

## Usage

```bash
make run-in-kind-v1alpha2   # base stack
make deploy-sharding-poc    # build + deploy the 2-shard StatefulSet

# verify partitioning / co-location (+ optional throughput)
E2E=1 go test ./test/e2e/crd -ginkgo.focus="Sharding" -ginkgo.v
E2E=1 SHARDING_THROUGHPUT=1 go test ./test/e2e/crd -ginkgo.focus="Sharding" -ginkgo.v
```
