---
title: "API Reference Generation"
type: docs
weight: 6
description: Generating Porch API Reference Documentation
---

Porch uses `crd-ref-docs` to generate API reference documentation from Go source code.

{{% alert color="primary" title="Note" %}}
Only regenerate documentation when API types in `api/porch/v1alpha1` are modified.
{{% /alert %}}

## Prerequisites

- Go 1.25+ installed
- Access to the Porch repository

## Generate Documentation

From the `docs/` directory:

```bash
make generate-api-docs-markdown
```

This runs `scripts/codegen/generate-api-reference-md.sh` which:
- Installs `crd-ref-docs` (v2.0.0) if not present
- Generates API reference from `api/porch/v1alpha1`
- Outputs to `docs/content/en/docs/7_cli_api/api-ref.md`

## Configuration

**Config:** `docs/crd-ref-docs/config.yaml`
- Excludes OCI types (not supported)
- Excludes standard Kubernetes metadata fields
- Uses Kubernetes v1.34 for API links

**Templates:** `docs/crd-ref-docs/markdown-templates/`
- Custom markdown templates for output formatting

## Resources

- [crd-ref-docs](https://github.com/elastic/crd-ref-docs)
