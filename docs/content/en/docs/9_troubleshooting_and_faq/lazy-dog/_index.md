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
