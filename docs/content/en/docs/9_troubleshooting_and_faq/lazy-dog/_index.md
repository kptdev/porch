---
title: "Porch Lazy Dog (Lathund)"
type: docs
weight: 9
description: Tips, tricks, and shortcuts for Porch developers
---

![Lazy Dog](/images/porch/LazyDog.svg)

A quick reference [Lazy Dog/Lathund](https://watchingtheswedes.com/2021/09/05/swedish-expression-lazy-dog/) of tips,
tricks, workarounds, and shortcuts for developers working with Porch. Please raise a PR to add your Porch magic spells to the Lazy Dog.

## Debugging Starlark scripts

Debugging Starlark scripts can be difficult, especially when running mutation pipelines in Porch. One technique is to put `print` statements in the code to print the value of variables. For example:

```python
def set_package_name(resources, root_kptfile_path, root_package_name):
  for r in resources:
    resource_path = r["metadata"]["annotations"]["internal.config.kubernetes.io/package-path"]
    resource_name = r["metadata"]["annotations"]["config.kubernetes.io/path"]
    print("Resource Path: " + resource_path)
    print("Resource Name: " + resource_name)
```

But that's not enough in Porch.

To force output of the result of partial rendering of failing pipelines in kpt, you must set [the following annotation](https://kpt.dev/book/04-using-functions/#debugging-render-failures) on the `Kptfile`:

```yaml
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: wordpress
  annotations:
    kpt.dev/save-on-render-failure: "true"
```

You also must set an annotation on the Package Revision so that Porch will [push draft package revisions even when they
fail](https://docs.porch.nephio.org/docs/4_tutorials_and_how-tos/working_with_package_revisions/#troubleshooting).

```bash
kubectl annotate packagerevision <name> porch.kpt.dev/push-on-render-failure=true
```

If the mutation pipeline is passing, you won't get any output from your `print()` statement because Porch assumes
everything is OK and does not print any output. The easiest way to work around this is to put a deliberate runtime error
into your Starlark script, which will cause an error and trigger the output:

```python
  set_package_name(ctx.resource_list["items"], root_kptfile_path, root_package_name)

  i = 10/0 # Deliberate division by zero error
```

## Using the "porch.kpt.dev/v1alpha1" version of the Porch API to clone and upgrade independent subpackages

You can use the Porch Kubernetes API directly (via `kubectl` or `curl`) to clone an upstream package as an independent
subpackage into an existing Draft package revision, and to upgrade that subpackage later.

### Cloning a subpackage via the API

 To clone an upstream package into a subdirectory of an existing Draft package revision, update the parent `PackageRevision` by appending
 an additional task of type `clone` that includes `subpackageDir` (for example via `kubectl apply` / server-side apply, or
 a PATCH request). The parent must already exist in Draft state with exactly one task.

```json
{
  "kind": "PackageRevision",
  "apiVersion": "porch.kpt.dev/v1alpha1",
  "metadata": {
    "name": "porch-test.package-with-sub.first-draft",
    "namespace": "porch-demo",
    "resourceVersion": "WHATEVER_THE_RESOURCE_VERSION_IS"
  },
  "spec": {
    "tasks": [
      {
        "type": "init",
        "init": {
          "description": "sample description"
        }
      },
      {
        "type": "clone",
        "clone": {
          "upstreamRef": {
            "upstreamRef": {
              "name": "porch-test.upstream-function.alpha"
            }
          },
          "subpackageDir": "subpackages/subpackage1"
        }
      }
    ]
  }
}
```

Key points:

- The first task is the parent's original task (e.g., `init` or `clone`)
- The second task is the new `clone` task with `subpackageDir` set to the target subdirectory
- `upstreamRef.upstreamRef.name` identifies the published upstream package revision to clone from
- `resourceVersion` must match the current parent package revision (fetch it with `kubectl get packagerevision <name> -o json`)
- The `"clone"` task is removed from the `PackageRevision` resource once the clone operation has been executed.

### Upgrading a subpackage via the API

 To upgrade an existing independent subpackage, update the parent `PackageRevision` by appending
 an additional task of type `upgrade` that includes `subpackageDir` (for example via `kubectl apply` / server-side apply,
 or a PATCH request). The parent must be in Draft state with exactly one task.

```json
{
  "kind": "PackageRevision",
  "apiVersion": "porch.kpt.dev/v1alpha1",
  "metadata": {
    "name": "porch-test.package-with-sub.second-draft",
    "namespace": "porch-demo",
    "resourceVersion": "WHATEVER_THE_RESOURCE_VERSION_IS"
  },
  "spec": {
    "tasks": [
      {
        "type": "init",
        "init": {
          "description": "sample description"
        }
      },
      {
        "type": "upgrade",
        "upgrade": {
          "oldUpstreamRef": {
            "name": "porch-test.upstream-function.alpha"
          },
          "newUpstreamRef": {
            "name": "porch-test.upstream-function.beta"
          },
          "localPackageRevisionRef": {
            "name": "porch-test.package-with-sub.second-draft"
          },
          "strategy": "force-delete-replace",
          "subpackageDir": "subpackages/subpackage1"
        }
      }
    ]
  }
}
```

Key points:

- `oldUpstreamRef.name` is the published package revision the subpackage was originally cloned from
- `newUpstreamRef.name` is the new upstream published package revision to upgrade to
- `localPackageRevisionRef.name` is the parent draft package revision that contains the current subpackage contents (used as the local side of the 3-way merge)
- `strategy` controls the merge behaviour (e.g., `resource-merge`, `force-delete-replace`)
- `subpackageDir` identifies which subdirectory contains the independent subpackage to upgrade
- The `"upgrade"` task is removed from the `PackageRevision` resource once the upgrade operation has been executed.

### Typical workflow

1. Create or copy a parent package revision (it will be in Draft state with one task)
2. `kubectl get packagerevision <name> -n <namespace> -o json` to fetch the current `resourceVersion`
3. Append the clone or upgrade task to the `spec.tasks` array
4. `kubectl apply -f <file>.json` to trigger the operation
5. Verify with `porchctl rpkg pull <name> ./dir --namespace=<namespace>` to inspect the subpackage contents
6. Propose and approve the parent package revision as normal

## Dumping resources to disk while debugging rendering in Porch

It can be difficult to see what is happening with `PackageRevisionResources` during rendering,
especially if a mutation pipeline is buggy. During debugging of rendering in Porch it can be
convenient to dump the resources to disk so that regular comparison tools can be used to
spot inconsistencies.

For example, the code fragment below calls a render:

```go
		resources, _, err = th.renderMutation(draftMeta.GetNamespace()).apply(ctx, resources)
		if err != nil {
			klog.Error(err)
			return renderError(err)
		}
```

You can temporarily add a call to the `WriteResourcesToFS()` function to dump the "before" and "after" resources to disk for comparison.

```go
		_, err = repository.WriteResourcesToFS(filesys.MakeFsOnDisk(), "/tmp/before", resources.Contents)
		if err != nil {
			klog.Error(err)
			return renderError(err)
		}

		resources, _, err = th.renderMutation(draftMeta.GetNamespace()).apply(ctx, resources)
		if err != nil {
			klog.Error(err)
			return renderError(err)
		}
		_, err = repository.WriteResourcesToFS(filesys.MakeFsOnDisk(), "/tmp/after", resources.Contents)
		if err != nil {
			klog.Error(err)
			return renderError(err)
		}
```
