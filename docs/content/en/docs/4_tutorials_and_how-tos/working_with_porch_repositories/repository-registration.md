---
title: "Registering Repositories"
type: docs
weight: 3
description: "Registering Repositories guide in Porch"
---

Registering a repository connects Porch to your Git storage backend, allowing it to discover and manage packages. You can register repositories with various authentication methods and configuration options.

### Register a Git Repository

Register a Git repository with Porch:

```bash
porchctl repo register https://github.com/example/porch-test.git \
  --namespace default \
  --name=porch-test \
  --description="Blueprint packages" \
  --branch=main
```

This command registers the Git repository with Porch, creates a Repository resource in Kubernetes and begins synchronizing packages from the repository.

**Example output:**

```bash
porch-test created
```

**Verify registration:**

Check that the repository was registered successfully:

```bash
porchctl repo get porch-test --namespace default
```

Look for `READY: True` in the output to confirm the repository is accessible and synchronized.

```bash
NAME         TYPE   CONTENT   SYNC SCHEDULE   DEPLOYMENT   READY   ADDRESS
porch-test   git    Package                                True    https://github.com/example-repo/porch-test.git
```

**If READY shows False:**

Inspect the detailed status to see the error message:

```bash
porchctl repo get porch-test --namespace default -o yaml
```

Check the `status.conditions` section for the error details:

```yaml
status:
  conditions:
  - type: Ready
    status: "False"
    reason: Error
    message: 'failed to list remote refs: repository not found: Repository not found.'
    lastTransitionTime: "2025-11-27T14:32:20Z"
```

**Common error messages:**

- `failed to list remote refs: repository not found` - Repository URL is incorrect or repository doesn't exist
- `failed to resolve credentials: cannot resolve credentials in a secret <namespace>/<secret-name>: secrets "<secret-name>" not found` - Authentication secret doesn't exist or name is misspelled
- `failed to resolve credentials: resolved credentials are invalid` - Credentials in the secret are invalid or malformed
- `branch "<branch-name>" not found in repository` - Specified branch doesn't exist in the repository
- `repository URL is empty` - Repository URL not specified in configuration
- `target branch is empty` - Branch name not specified in configuration

---

### Register with Authentication

For private repositories, provide authentication credentials:

**Using Basic Auth:**

```bash
porchctl repo register https://github.com/example/private-repo.git \
  --namespace default \
  --name=private-repo \
  --repo-basic-username=myusername \
  --repo-basic-password=mytoken
```


{{% alert title="Note" color="primary" %}}
For production environments, use secret management solutions (external secret stores, sealed-secrets) rather than embedding credentials in commands.

See [Git Authentication]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config/git-authentication" %}}) for detailed authentication configuration.
{{% /alert %}}

---

### Register with Advanced Options

Configure additional repository settings:

```bash
porchctl repo register https://github.com/nephio-project/catalog \
  --namespace default \
  --name=infra \
  --directory=infra \
  --deployment=true \
  --sync-schedule="*/10 * * * *" \
  --description="Infrastructure packages"
```

**Common flags:**

- `--name`: Repository name in Kubernetes (defaults to last segment of URL)
- `--description`: Brief description of the repository
- `--branch`: Git branch to use (defaults to `main`)
- `--directory`: Subdirectory within repository containing packages. Use `/` for repository root, or specify a path like `/blueprints` or `infra/packages`. Leading slash is optional.
- `--deployment`: Mark as deployment repository (packages are deployment-ready)
- `--sync-schedule`: Cron expression for periodic sync (e.g., `*/10 * * * *` for every 10 minutes). Format: `minute hour day month weekday`.
- `--repo-basic-username`: Username for basic authentication
- `--repo-basic-password`: Password/token for basic authentication

{{% alert title="Note" color="primary" %}}
For complete command syntax and all available flags, see the [Porchctl CLI Guide]({{% relref "/docs/7_cli_api/porchctl.md" %}}).
{{% /alert %}}

---

### Rehoming an Existing Repository

You can point an already-registered repository at a different Git URL (for example, after migrating a repository to a new host) without deleting and recreating it. This is called *rehoming*. Edit `spec.git.repo` on the Repository resource:

```bash
kubectl patch repository porch-test --namespace default --type merge \
  -p '{"spec":{"git":{"repo":"https://new-host.example.com/example/porch-test.git"}}}'
```

The URL change is accepted immediately, but it is validated on the next sync rather than at admission time. Rehoming only succeeds if the commit Porch last synced (`status.gitCommitHash`) is still present in the history of the configured branch on the new remote — typically a mirror or fork of the original. The branch may have advanced since; the commit just has to remain reachable. If the recorded commit is absent from the new branch's history, the change persists in the spec but the repository reports `READY: False` with a history-mismatch error; revert to a URL whose branch still contains that commit to recover.

Note that `--branch`, `--directory`, and the `createBranch` setting cannot be changed after registration. See [Rehoming a Repository]({{% relref "/docs/2_concepts/repositories#rehoming-a-repository" %}}) for the full behavior.

---
