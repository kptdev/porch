---
title: "Repository Sync Troubleshooting"
type: docs
weight: 9
description: Common repository sync issues and their solutions
---

## Common Problems & Solutions

### Repository Not Syncing

**Problem**: Repository shows as Ready but packages are not updating

**Solutions**:
```bash
# Check repository status
kubectl get repositories -n <namespace>

# Check repository conditions
kubectl describe repository <repo-name> -n <namespace>

# Verify sync configuration
kubectl get repository <repo-name> -n <namespace> -o yaml | grep -A5 sync

# Check repository synchronization logs
kubectl logs -n porch-system deployment/porch-controllers | grep "sync.*<repo-name>"

# Check for sync errors
kubectl logs -n porch-system deployment/porch-controllers | grep "<repo-name>.*error"
```

**Common causes:** invalid cron expression falls back to default frequency, repository authentication issues or network connectivity problems.

### Authentication Failures

**Problem**: Repository status shows authentication errors

**Error messages**:
- `"authentication required"`
- `"invalid credentials"`
- `"permission denied"`

**Solutions**:
```bash
# Check secret exists (secret name is referenced in repository spec)
kubectl get repository <repo-name> -n <namespace> -o jsonpath='{.spec.git.secretRef.name}'
kubectl get secret <secret-name> -n <namespace>

# Verify secret data
kubectl get secret <secret-name> -n <namespace> -o yaml

# Recreate secret with correct credentials
kubectl delete secret <secret-name> -n <namespace>
kubectl create secret generic <secret-name> \
  --namespace=<namespace> \
  --type=kubernetes.io/basic-auth \
  --from-literal=username=<user> \
  --from-literal=password=<token>

# Porch controller will automatically retry authentication based on error-specific intervals
# See error handling documentation for retry timing details

# For immediate retry, re-register the repository with correct credentials:
kubectl delete repository <repo-name> -n <namespace>
kubectl delete secret <secret-name> -n <namespace>  # repo reg will create a new secret
porchctl repo reg <repo-url> --name <repo-name> --repo-basic-username <user> --repo-basic-password <token>
```

### Invalid Cron Expression

**Problem**: CLI registration fails with cron validation error

**Error message**: `"invalid sync-schedule cron expression"`

**Solutions**:
```bash
# Valid cron formats (5 fields: minute hour day month weekday)
porchctl repo reg <repo> --sync-schedule "*/10 * * * *"  # Every 10 minutes
porchctl repo reg <repo> --sync-schedule "0 */2 * * *"   # Every 2 hours
porchctl repo reg <repo> --sync-schedule "0 9 * * 1-5"   # 9 AM weekdays

# Invalid examples to avoid:
# "10 * * * *"     # Missing minute field
# "* * * * * *"    # Too many fields (6 instead of 5)
```

### Repository Stuck in Reconciling

**Problem**: Repository condition shows `Reason: Reconciling` indefinitely

**Diagnostic steps**:
```bash
# Check Porch logs
kubectl logs -n porch-system deployment/porch-server

# Look for repository synchronization errors
kubectl logs -n porch-system deployment/porch-server | grep "repositorySync.*<repo-name>"

# Check repository accessibility
git ls-remote <repo-url>  # For Git repos
```

**Common causes**: large repository taking time to clone/sync, network timeouts or repository structure issues.

### Repository Rehoming Fails After URL Change

**Problem**: After changing a repository's Git URL (`spec.git.repo`), the repository reports `Ready=False`.

**Error message**:
- `repository hash "<recorded-hash>" registered on the repository is not present in the history of branch "<branch>" at the remote (branch head "<new-head>"); the git URL appears to point at a different repository`

**Cause**: Changing `spec.git.repo` *rehomes* the repository to a new remote. The change is accepted by the API immediately, but it is validated on the next sync: the commit Porch last synced (`status.gitCommitHash`) must still be present in the history of the configured branch on the new remote. The branch is allowed to have moved forward — the recorded commit does not need to be the new HEAD, only reachable from it. The error above means the recorded commit is absent from the new branch's history entirely, so the new URL points at an unrelated or divergent repository and Porch rejects it. The URL change persists in the spec, and the last-good `status.gitCommitHash` is left unchanged.

**Solutions**:
```bash
# Inspect the condition and the commit Porch expects to find in the history
kubectl describe repository <repo-name> -n <namespace>
kubectl get repository <repo-name> -n <namespace> -o jsonpath='{.status.gitCommitHash}'; echo

# Confirm whether the recorded commit exists in the new remote's branch history
git ls-remote <new-url> <branch>                 # current HEAD of the configured branch
git clone --branch <branch> <new-url> /tmp/check  # then: git -C /tmp/check cat-file -e <recorded-hash>

# Option A: point at a URL whose branch history still contains the recorded commit
# (e.g. a true mirror/fork of the original, or the original itself moved forward)
kubectl patch repository <repo-name> -n <namespace> --type merge \
  -p '{"spec":{"git":{"repo":"<matching-url>"}}}'

# Option B: revert to the original URL to clear the error
kubectl patch repository <repo-name> -n <namespace> --type merge \
  -p '{"spec":{"git":{"repo":"<original-url>"}}}'
```

The spec change triggers an immediate re-sync, so recovery does not wait for the error-retry backoff. Once the configured branch's history contains the recorded commit again, the condition returns to `Ready=True`.

### One-time Sync Not Triggering

**Problem**: `porchctl repo sync` command succeeds but sync does not happen

**Diagnostic steps**:
```bash
# Check runOnceAt field was set
kubectl get repository <repo-name> -n <namespace> -o jsonpath='{.spec.sync.runOnceAt}'

# Verify timestamp is in future
date -u  # Compare with runOnceAt value

# Check one-time synchronization logs
kubectl logs -n porch-system deployment/porch-controllers | grep "runOnceAt"
```

**Solutions**: Ensure the timestamp is at least 1 minute in the future and verify that the namespace is correct.

## Error Messages & Diagnostic Steps

### "repository is required positional argument"
**Command**: `porchctl repo reg`
**Solution**: Provide repository URL as argument
```bash
porchctl repo reg https://github.com/example/repo.git
```

### "both username/password and workload identity specified"
**Command**: `porchctl repo reg`
**Solution**: Use only one authentication method
```bash
# Use only one authentication method during registration
# Either basic auth OR workload identity, not both
```

### "no repositories found in namespace"
**Command**: `porchctl repo sync --all`
**Solution**: Check namespace and repository existence
```bash
kubectl get repositories -n <namespace>
kubectl get repositories --all-namespaces
```

### "Scheduled time is within 1 minute or in the past"
**Command**: `porchctl repo sync --run-once`
**Solution**: Use future timestamp or longer duration
```bash
porchctl repo sync <repo> --run-once 5m
porchctl repo sync <repo> --run-once "2024-12-01T15:00:00Z"
```

## Debugging Tips & Tools

### Enable Verbose Logging
```bash
# Increase controller log level
kubectl patch deployment porch-controllers -n porch-system -p '{"spec":{"template":{"spec":{"containers":[{"name":"porch-controllers","args":["--v=2"]}]}}}}'
```

### Monitor Repository Events
```bash
# Watch repository changes
kubectl get repositories -w -n <namespace>

# Monitor events
kubectl get events -n <namespace> --field-selector involvedObject.kind=Repository
```

### Check Repository Synchronization Status
```bash
# Repository sync logs
kubectl logs -n porch-system deployment/porch-controllers | grep "sync.*<repo-name>"

# Next sync time from status
kubectl get repository <repo-name> -n <namespace> -o jsonpath='{.status.nextFullSyncTime}'
```

### Validate Repository Structure
```bash
# For Git repositories
git clone <repo-url>
find . -name "Kptfile" -type f  # Should find package directories

# Check branch exists
git branch -r | grep <branch-name>
```

## FAQ

### Q: How often do repositories sync by default?
**A**: Without a custom sync schedule, repositories use the system default frequency of 1 hour for full syncs and 5 minutes for health checks. These defaults can be customized using controller flags:

```bash
--repositories.full-sync-frequency=1h
--repositories.health-check-frequency=5m
```

See [Repository Controller Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-controllers-config.md#repository-controller-configuration" %}}) for details.

### Q: Can I have both periodic and one-time sync?
**A**: Yes, periodic scheduling and one-time sync work independently. One-time synchronization executes regardless of the periodic schedule.

### Q: Why is my cron expression not working?
**A**: Porch uses standard 5-field cron format. Common mistakes include using 6 fields (seconds not supported), missing fields, or invalid ranges or values.

### Q: How do I stop repository syncing?
**A**: Repository synchronization cannot be completely stopped. Porch continuously monitors repositories for changes. You can only modify the sync frequency by updating the sync schedule configuration or remove custom schedules to use the default frequency.

### Q: Can I sync repositories across namespaces?
**A**: Use `--all-namespaces` flag:
```bash
porchctl repo sync --all --all-namespaces
```

### Q: What happens if repository is deleted during sync?
**A**: The synchronization system gracefully handles repository deletion and stops sync operations for that repository.

### Q: Can I change a repository's Git URL?
**A**: Yes. `spec.git.repo` is mutable — changing it *rehomes* the repository to a new remote without deleting and recreating it. The change is accepted immediately, but it only takes effect if the commit Porch last synced (`status.gitCommitHash`) is still present in the history of the configured branch on the new remote (the branch may have moved forward; the commit just has to be reachable). Otherwise the repository reports `Ready=False` with a history-mismatch error. See [Repository Rehoming Fails After URL Change](#repository-rehoming-fails-after-url-change) and the [Repositories concept page]({{% relref "/docs/2_concepts/repositories#rehoming-a-repository" %}}). Note that `spec.git.branch`, `spec.git.directory`, and `spec.git.createBranch` remain immutable.

### Q: How do I check if authentication is working?
**A**: Repository condition will show `Ready: True` if authentication succeeds. Check `kubectl describe repository` for detailed status.

---

{{% alert title="Note" color="primary" %}}
OCI repository support is experimental and may not have full feature parity with Git repositories.
{{% /alert %}}