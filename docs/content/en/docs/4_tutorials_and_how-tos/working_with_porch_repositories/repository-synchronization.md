---
title: "Synchronizing Repositories"
type: docs
weight: 5
description: "Synchronizing repositories guide in Porch"
---

## Repository Synchronization

Porch periodically synchronizes with registered repositories to discover new packages and updates. You can also trigger manual synchronization when you need immediate updates.

{{% alert title="Note" color="primary" %}}
**Sync Schedule Format:** Cron expressions follow the format `minute hour day month weekday`. For example, `*/10 * * * *` means "every 10 minutes".
{{% /alert %}}

### Trigger Manual Sync

Force an immediate synchronization of a repository:

```bash
porchctl repo sync porch-test --namespace default
```

This command schedules a one-time sync (minimum 1-minute delay), updates packages from the repository. This sync is independent of the periodic sync schedule.

**Example output:**

```bash
Repository porch-test sync scheduled
```

---

### Sync Multiple Repositories

Sync several repositories at once:

```bash
porchctl repo sync repo1 repo2 repo3 --namespace default
```

---

### Sync All Repositories

Sync all repositories in a namespace:

```bash
porchctl repo sync --all --namespace default
```

Sync across all namespaces:

```bash
porchctl repo sync --all --all-namespaces
```

---

### Schedule Delayed Sync

Schedule sync with custom delay:

```bash
# Sync in 5 minutes
porchctl repo sync porch-test --namespace default --run-once 5m

# Sync in 2 hours 30 minutes
porchctl repo sync porch-test --namespace default --run-once 2h30m

# Sync at specific time
porchctl repo sync porch-test --namespace default --run-once "2024-01-15T14:30:00Z"
```

{{% alert title="Note" color="primary" %}}
The minimum delay is 1 minute from command execution and the `spec.sync.runOnceAt` field is updated in the Repository CR. This sync is independent of the existing periodic sync schedule and past timestamps are automatically adjusted to minimum delay.
{{% /alert %}}

---

### Syncs After a Rehome

Changing `spec.git.repo` triggers a full sync, without any command. The sync checks that the
recorded commit is still in the new remote's history. If it is not, the repository reports
`Ready=False`. For the full procedure, see
[Rehoming Repositories]({{% relref "/docs/4_tutorials_and_how-tos/working_with_porch_repositories/repository-rehoming" %}}).

---