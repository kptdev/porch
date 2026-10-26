---
title: "Repository Sync"
type: docs
weight: 1
description: "Configure repository synchronization for Porch Repositories"
---

{{% alert title="Note" color="primary" %}}
This documentation is currently located in the system configuration section but should be moved to a more logical location as it's about configuring individual Repository resources, not system-wide settings. This may be relocated in future documentation updates.
{{% /alert %}}

## Sync Configuration Fields

The `spec.sync` field in a Repository CR controls synchronization behavior with the external repository. Repositories without sync configuration use the system default for periodic synchronization (default 1 hour, configurable via `--repositories.full-sync-frequency` flag - see [Repository Controller Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-controllers-config.md#repository-controller-configuration" %}}).

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: example-repo
  namespace: default
spec:
  sync:
    schedule: "*/10 * * * *"           # Periodic sync using cron expression
    runOnceAt: "2024-01-15T10:30:00Z"  # One-time sync at specific time
```

### Schedule Field

The `schedule` field accepts standard [cron expressions](https://en.wikipedia.org/wiki/Cron) for periodic synchronization:

- **Format**: Standard 5-field cron expression (`minute hour day month weekday`)
- **Examples**:
  - `"*/10 * * * *"` - Every 10 minutes
  - `"0 */2 * * *"` - Every 2 hours
  - `"0 9 * * 1-5"` - 9 AM on weekdays
  - `"0 0 * * 0"` - Weekly on Sunday at midnight

### RunOnceAt Field

The `runOnceAt` field schedules a one-time sync at a specific timestamp:

- **Format**: RFC3339 timestamp (`metav1.Time`)
- **Examples**:
  - `"2025-01-15T14:30:00Z"` - Sync at 2:30 PM UTC on January 15, 2025
  - `"2025-12-25T00:00:00Z"` - Sync at midnight UTC on Christmas Day
  - `"2025-06-01T09:15:30Z"` - Sync at 9:15:30 AM UTC on June 1st
  - `"2025-12-10T15:45:00-05:00"` - Sync at 3:45 PM EST (UTC-5) on March 10th
- **Behavior**: The action executes once at the specified time and is ignored if the timestamp is in the past. It is independent of the
periodic schedule and can be updated to reschedule.

{{% alert title="Note" color="primary" %}}
One-time syncs should only be used when discrepancies are found between the external repository and Porch cache. Under normal
conditions, rely on periodic syncs for regular synchronization.
{{% /alert %}}

## Complete Examples

### Git Repository with Periodic Sync

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: blueprints
  namespace: default
spec:
  description: Blueprints with hourly sync
  type: git
  sync:
    schedule: "0 * * * *"  # Every hour
  git:
    repo: https://github.com/example/blueprints.git
    branch: main
    directory: packages
```

### Combined Periodic and One-time Sync

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: combined-sync
  namespace: default
spec:
  type: git
  sync:
    schedule: "0 */6 * * *"            # Every 6 hours
    runOnceAt: "2024-01-15T09:00:00Z"  # Sync once
  git:
    repo: https://github.com/example/repo.git
    branch: main
```

## Sync Behavior

Repository synchronization is managed by the Repository Controller using a dual sync strategy:

**Health Checks** (default 5 minutes):
- Lightweight connectivity validation
- Only when repository is healthy

**Full Sync** (default 1 hour or cron schedule):
- Complete package discovery
- Git fetch + validation
- Asynchronous execution

## Status Updates
Repository sync status is reflected in the Repository CR conditions:

```yaml
status:
  conditions:
  - type: RepositoryReady
    status: "True"
    reason: Ready
    message: "Repository Ready"
    lastTransitionTime: "2025-01-30T11:00:00Z"
  lastFullSyncTime: "2025-01-30T11:00:00Z"
  nextFullSyncTime: "2025-01-30T12:00:00Z"
  observedGeneration: 5
  observedRunOnceAt: null
  packageCount: 42
  gitCommitHash: "abc123def456..."
```

**Status Fields:**
- `lastFullSyncTime`: Timestamp of last successful full sync (used for cron schedule calculation)
- `nextFullSyncTime`: Timestamp when next full sync is scheduled
- `observedGeneration`: Generation of spec that was last reconciled (detects spec changes)
- `observedRunOnceAt`: (Transient) Tracks last observed runOnceAt value (prevents redundant syncs)
- `packageCount`: Number of package revisions discovered in repository
- `gitCommitHash`: Commit hash of configured branch at sync

**Implementation Details:**
- Status fields are updated after each successful full sync
- `lastFullSyncTime` is used for cron schedule calculation (not condition timestamps)
- `nextFullSyncTime` provides explicit visibility into sync schedule
- `observedRunOnceAt` prevents duplicate syncs when runOnceAt is cleared
- `gitCommitHash` provides GitOps observability for tracking upstream changes
- All fields are optional and populated after first successful sync

## Troubleshooting

### Common Issues

1. **Invalid Cron Expression**:
   - Check controller logs for parsing errors
   - Verify 5-field format
   - Repository falls back to default frequency

2. **Past RunOnceAt Time**:
   - One-time sync is skipped
   - Update to future timestamp
   - Check controller logs for details

3. **Sync Failures**:
   - Check repository conditions
   - Verify authentication credentials
   - Review repository accessibility
   - Check controller logs for detailed error information

### Monitoring
- Repository conditions show sync status
- Status fields provide structured sync information
- Check controller logs: `kubectl logs -n porch-system deployment/porch-controllers -f`
- View status fields: `kubectl get repo my-repo -o yaml`
- Query next sync time: `kubectl get repo my-repo -o jsonpath='{.status.nextFullSyncTime}'`

## CLI Commands

For repository registration and sync commands, see the [porchctl repo CLI guide]({{% relref "/docs/7_cli_api/porchctl.md#repo" %}}):

---

{{% alert title="Note" color="primary" %}}
OCI repository support is experimental and may not have full feature parity with Git repositories.
{{% /alert %}}