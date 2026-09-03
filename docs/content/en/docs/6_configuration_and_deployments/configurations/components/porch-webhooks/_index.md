---
title: "Porch Webhooks"
type: docs
weight: 3
description: "Overview of Porch validating webhooks"
---

Porch uses Kubernetes [validating webhooks](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#validatingadmissionwebhook) to enforce validation at admission time. These webhooks run as part of the porch-controllers pod and intercept Create, Update, and Delete operations before they reach etcd. They provide fail-closed guarantees. If webhooks become unavailable, operations on those resources are rejected.

## Overview

**PackageRevision Webhook** (`porch.kpt.dev/v1alpha2`) validates CREATE, UPDATE and DELETE on PackageRevision resources. It enforces lifecycle transitions, immutable fields, render race prevention, and upstream reference protection. The endpoint is `/validate-porch-kpt-dev-v1alpha2-packagerevision`.

**Repository Webhook** (`config.porch.kpt.dev/v1alpha1`) validates CREATE and UPDATE on Repository resources. It enforces git repository conflict detection, prevents duplicate git locations, and blocks directory nesting. The endpoint is `/validate-repository`.

See [Validation Rules](./validation-rules.md) for details on each check.

## How It Works

When you apply or modify a resource, Kubernetes routes the request to the ValidatingWebhookConfiguration, which calls the webhook in the porch-controllers pod. The webhook runs the validation and either admits the request (continues to etcd write) or denies it (returns error to user immediately).

Both webhooks use `failurePolicy: Fail`, meaning if the webhook is unavailable, times out, or returns an error, the operation is rejected. This fail-closed approach prevents invalid resources from being created. If webhooks become unavailable, operations on those resources cannot proceed until webhooks are restored.

## Certificate Setup

Webhooks require TLS certificates for secure communication. By default, Porch generates self-signed certificates at deployment time. For production, use cert-manager for automatic provisioning and rotation.

See [Webhook Certificate Management](./cert-manager-webhooks.md) for configuration options.

## Troubleshooting

See [Webhook Certificate Management - Troubleshooting](./cert-manager-webhooks.md#troubleshooting) for common issues and solutions. For validation-specific errors, see [Validation Rules](./validation-rules.md).
