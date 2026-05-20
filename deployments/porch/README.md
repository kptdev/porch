# porch

## Description
Porch deployment


### Checkout the repository
```
git clone https://github.com/kptdev/porch.git
cd porch
```

### Cache Types
Porch supports two cache types:

#### CR Cache (default)
Uses Kubernetes Custom Resources for caching. Lightweight, no external dependencies.

#### Database Cache
Uses PostgreSQL for caching. Requires database setup.

**Note:** The included PostgreSQL deployment is for development purposes only. For production use, configure an external PostgreSQL instance.

### Generate deployment configuration
The `make deployment-config` target creates a ready-to-deploy Kubernetes package in `.build/deploy/` by:
- Copying deployment manifests from `deployments/porch/`
- Setting image tags to match your configuration
- Configuring cache type and reconcilers
- Running kpt functions to process the package
- Removing build-time configuration from the final output

```
make deployment-config
```

#### Available flags
- `PORCH_CACHE_TYPE`: Set cache type (`DB` or `CR`) (default: `CR`)
- `IMAGE_TAG`: Set image tag (default: `{USER}-{git-hash}`)
- `IMAGE_REPO`: Set image repository (default: `ghcr.io/kptdev`)
- `ENABLED_RECONCILERS`: Comma-separated list of reconcilers (default: `packagevariants,packagevariantsets,repositories`)
- `FN_RUNNER_WARM_UP_POD_CACHE`: Enable/disable pod cache warm-up (default: `true`)

Examples:

**Database Cache:**
```
make deployment-config PORCH_CACHE_TYPE=DB IMAGE_TAG=latest FN_RUNNER_WARM_UP_POD_CACHE=false
```
Deploys:
- Porch server with PostgreSQL cache backend
- PostgreSQL StatefulSet for development
- PackageVariant (PV) and PackageVariantSet (PVS) reconcilers enabled
- Function runner with pod cache warm-up disabled
- All images tagged as `latest`

**CR Cache:**
```
make deployment-config PORCH_CACHE_TYPE=CR IMAGE_TAG=v1.5.3
```
Deploys:
- Porch server with Custom Resource cache backend
- No database components (lightweight deployment)
- PV and PVS reconcilers enabled
- Function runner with pod cache warm-up enabled
- All images tagged as `v1.5.3`

### Apply the package

#### Method 1: Using kpt directly
```
kpt live init .build/deploy
kpt live apply .build/deploy --reconcile-timeout=2m --output=table
```
Details: https://kpt.dev/reference/cli/live/


#### Method 2: Using make target
```
make deploy-current-config
```

### Remove the deployment
```
make destroy
```

This removes all Porch components from the cluster using the current configuration in `.build/deploy/`.
