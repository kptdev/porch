---
title: "Troubleshooting & FAQ"
type: docs
weight: 9
description: Common issues, solutions, and frequently asked questions for Porch
---

This section provides guidance for diagnosing and resolving common issues with Porch, along with answers to frequently asked questions.

## What's Covered

This troubleshooting guide addresses:

- **Common problems and their solutions** - Step-by-step fixes for typical issues
- **Error messages and diagnostic steps** - Understanding and resolving error conditions
- **Debugging tips and tools** - Techniques for investigating problems
- **Disaster recovery scenarios** - Recovering from data loss or system failures
- **FAQ** - Answers to questions frequently asked by Porch users

## Troubleshooting Topics

### [Repository Sync Troubleshooting]({{% relref "repository-sync" %}})

Common issues with repository synchronization:
- Repositories not syncing or updating
- Authentication failures with Git repositories
- Invalid cron expressions for sync schedules
- Repositories stuck in reconciling state
- One-time sync not triggering

### [Disaster Recovery]({{% relref "disaster-recovery" %}})

Recovering from data loss scenarios:
- Complete disaster (cluster, Git, and cache loss)
- Kubernetes cluster loss
- Porch microservice restarts
- DB cache loss with and without backups
- Backup strategies for Porch components

### [Porch Lazy Dog (Lathund)]({{% relref "lazy-dog" %}})

A quick reference of tips, tricks, workarounds, and shortcuts for developers working with Porch. Please raise a PR to
add your Porch magic spells to the Lazy Dog.

## Getting Help

If you encounter issues not covered in this guide:

1. **Check Porch logs**: Most issues leave traces in the Porch server logs
   ```bash
   kubectl logs -n porch-system deployment/porch-server
   ```

2. **Verify resource status**: Check the status of Porch resources
   ```bash
   kubectl get repositories -A
   kubectl describe repository <name> -n <namespace>
   ```

3. **Search existing issues**: Check the [Porch GitHub issues](https://github.com/kptdev/porch/issues)

4. **Ask the community**: 
   - Open a [GitHub discussion](https://github.com/kptdev/kpt/discussions)
   - Join the Kubernetes Slack #kpt channel

5. **Report a bug**: If you've found a bug, [open an issue](https://github.com/kptdev/porch/issues/new) with:
   - Porch version
   - Steps to reproduce
   - Expected vs actual behavior
   - Relevant logs and resource definitions

## Quick Diagnostic Commands

Useful commands for initial troubleshooting:

```bash
# Check Porch components are running
kubectl get pods -n porch-system

# Check API service status
kubectl get apiservice v1alpha1.porch.kpt.dev

# List all repositories
kubectl get repositories -A

# Check repository details
kubectl describe repository <name> -n <namespace>

# View Porch server logs
kubectl logs -n porch-system deployment/porch-server --tail=100

# View controller logs
kubectl logs -n porch-system deployment/porch-controllers --tail=100

# List package revisions
kubectl get packagerevisions -A
```
