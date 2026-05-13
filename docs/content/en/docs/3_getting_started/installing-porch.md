---
title: "Installing Porch"
type: docs
weight: 2
description: Install guide for the Porch system on a Kubernetes cluster.
---

## Deploying Porch on a cluster

Create a new directory for the kpt package and navigate into it:

```bash
mkdir porch-{{% params "latestTag" %}} && cd porch-{{% params "latestTag" %}}
```

Download the latest Porch kpt package blueprint:

```bash
curl -LO "https://github.com/kptdev/porch/releases/download/v{{% params "latestTag" %}}/porch_blueprint.tar.gz"
```

Extract the Porch kpt package contents:

```bash
tar -xzf porch_blueprint.tar.gz
```

Initialize and apply the Porch kpt package:

```bash
kpt live init && kpt live apply
```

## Verify Installation

Check that Porch components are running:

```bash
kubectl get all -n porch-system
```

A healthy Porch installation should show:

```bash
NAME                                   READY   STATUS    RESTARTS   AGE
pod/function-runner-567ddc76d-7k8sj    1/1     Running   0          4m3s
pod/function-runner-567ddc76d-x75lv    1/1     Running   0          4m3s
pod/porch-controllers-d8dfccb4-8lc6j   1/1     Running   0          4m3s
pod/porch-server-7dc5d7cd4f-smhf5      1/1     Running   0          4m3s

NAME                      TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)            AGE
service/api               ClusterIP   10.96.108.221   <none>        443/TCP,8443/TCP   4m3s
service/function-runner   ClusterIP   10.96.237.108   <none>        9445/TCP           4m3s

NAME                                READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/function-runner     2/2     2            2           4m3s
deployment.apps/porch-controllers   1/1     1            1           4m3s
deployment.apps/porch-server        1/1     1            1           4m3s

NAME                                         DESIRED   CURRENT   READY   AGE
replicaset.apps/function-runner-567ddc76d    2         2         2       4m3s
replicaset.apps/porch-controllers-d8dfccb4   1         1         1       4m3s
replicaset.apps/porch-server-7dc5d7cd4f      1         1         1       4m3s
```

Verify the Porch API is accessible:

```bash
kubectl api-resources | grep porch
```

You should see Porch API resources:

```bash
packagerevs                                      config.porch.kpt.dev/v1alpha1     true         PackageRev
packagevariants                                  config.porch.kpt.dev/v1alpha1     true         PackageVariant
packagevariantsets                               config.porch.kpt.dev/v1alpha2     true         PackageVariantSet
repositories                                     config.porch.kpt.dev/v1alpha1     true         Repository
packagerevisionresources                         porch.kpt.dev/v1alpha1            true         PackageRevisionResources
packagerevisions                                 porch.kpt.dev/v1alpha1            true         PackageRevision
packages                                         porch.kpt.dev/v1alpha1            true         PorchPackage
```

## Troubleshooting

### Pods not starting

If pods are in `Pending` or `ImagePullBackOff` state:

```bash
kubectl describe pod -n porch-system <pod-name>
```

**Common causes:**
- **No internet access**: Cluster cannot pull images from GitHub Container Registry. Pre-pull images or use a local registry.
- **Insufficient resources**: Cluster doesn't have enough CPU/memory. Check node resources with `kubectl top nodes`.
- **Image pull secrets**: If using a private registry, ensure image pull secrets are configured.

### kpt live apply fails

If `kpt live apply` fails with errors:

**"resource mapping not found"**: CRDs may not be installed yet. Wait a few seconds and retry:
```bash
kpt live apply
```

**"context deadline exceeded"**: Cluster is slow to respond. Increase timeout or check cluster health:
```bash
kubectl get nodes
kubectl get pods -A
```

### API resources not appearing

If `kubectl api-resources | grep porch` shows nothing:

1. Check porch-server pod logs:
   ```bash
   kubectl logs -n porch-system deployment/porch-server
   ```

2. Verify API service registration:
   ```bash
   kubectl get apiservice | grep porch
   ```

3. Check aggregated API server connectivity:
   ```bash
   kubectl get apiservice v1alpha1.porch.kpt.dev -o yaml
   ```

### Function runner issues

If function execution fails:

1. Check function-runner logs:
   ```bash
   kubectl logs -n porch-system deployment/function-runner
   ```

2. Verify function-runner service:
   ```bash
   kubectl get svc -n porch-system function-runner
   ```

## Next Steps

Porch is now installed. To start using it:

1. See [Tutorials and How-Tos]({{% relref "/docs/4_tutorials_and_how-tos" %}}) to learn how to:
   - Register a Git repository
   - Create and manage packages

2. If you need to remove Porch, see [Uninstalling Porch]({{% relref "uninstalling-porch" %}}).
