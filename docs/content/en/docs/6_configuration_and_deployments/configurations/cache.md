---
title: "Cache Configuration"
type: docs
weight: 2
description: "Configure Porch caching mechanisms"
---

Porch supports two caching mechanisms for storing package metadata. Choose based on your deployment scale and infrastructure.

## Cache Types

### CR Cache (Default)

Uses Custom Resources for metadata tracking while package content is served through Porch's aggregated API server. Suitable for development and smaller deployments.

**Characteristics:**
- No external database required
- Metadata stored in Kubernetes etcd via Custom Resources
- Package content served through aggregated API server
- Automatic backup with cluster backups
- Simple operational model

**Best For:**
- Development and testing environments
- Small-scale deployments (<100 repositories)
- Environments without database infrastructure
- Getting started with Porch

### Database Cache

Uses PostgreSQL to store package metadata and content while still serving through Porch's aggregated API server. Recommended for production deployments.

**Characteristics:**
- Better performance at scale
- Advanced querying capabilities
- Separate scaling from Kubernetes cluster
- Production-grade reliability

**Best For:**
- Production deployments
- Large-scale environments (>100 repositories)
- High-performance requirements
- Environments requiring analytics

## Configuration

### CR Cache Setup

CR Cache is enabled by default. No additional configuration required.

**API Server:**
```yaml
args:
  - --cache-type=CR
```

**Repository Controller:**
```yaml
args:
  - --reconcilers=repositories
  - --repositories.cache-type=CR
```

### Database Cache Setup

#### Prerequisites

- PostgreSQL database (v12+) running and accessible
- Database credentials and connection details
- Database initialized with Porch schema (`api/sql/porch-db.sql`)

{{% alert title="Warning" color="warning" %}}
Before configuring database cache:
1. Ensure PostgreSQL is running and accessible
2. Initialize the database schema using `api/sql/porch-db.sql`
3. Porch will fail to start if it cannot connect or if the schema is not initialized
{{% /alert %}}

#### Configuration Steps

**1. Initialize Database Schema:**

```bash
# From Porch repository root
PGPASSWORD=your_password psql -h postgresql.example.com -U porch_user -d porch -f api/sql/porch-db.sql
```

**2. Create Database Secret:**

```bash
kubectl create secret generic porch-db-config \
  --namespace=porch-system \
  --from-literal=host=postgresql.example.com \
  --from-literal=port=5432 \
  --from-literal=database=porch \
  --from-literal=username=porch_user \
  --from-literal=password=your_password
```

**3. Configure Porch Server:**

```yaml
spec:
  template:
    spec:
      containers:
      - name: porch-server
        args:
        - --cache-type=DB
        env:
        - name: DB_DRIVER
          value: "pgx"
        - name: DB_HOST
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: host
        - name: DB_PORT
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: port
        - name: DB_NAME
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: database
        - name: DB_USER
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: username
        - name: DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: password
```

**4. Configure Repository Controller:**

```yaml
spec:
  template:
    spec:
      containers:
      - name: controller
        args:
        - --reconcilers=repositories
        - --repositories.cache-type=DB
        - --repositories.max-concurrent-reconciles=100
        - --repositories.max-concurrent-syncs=50
        env:
        - name: DB_HOST
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: host
        - name: DB_NAME
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: database
        - name: DB_USER
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: username
        - name: DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: porch-db-config
              key: password
```

**5. Apply Configuration:**

```bash
kubectl apply -f porch-server-deployment.yaml
kubectl apply -f porch-controllers-deployment.yaml
```

### Draft Push Mode (Optional)

By default, DB Cache stores Draft and Proposed package revisions in PostgreSQL only and pushes to Git on publish. To also push Draft and Proposed revisions to Git during background sync, enable draft push mode on both components:

```yaml
# porch-server
args:
- --cache-type=DB
- --db-push-drafts-to-git=true

# repository controller
args:
- --reconcilers=repositories
- --repositories.cache-type=DB
- --repositories.push-drafts-to-git=true
```

Draft create and update operations remain database-only; Git is updated during sync when database content has changed. See [Database Cache — Configurable Git Push Behavior]({{% relref "/docs/5_architecture_and_components/package-cache/db-cache.md#configurable-git-push-behavior" %}}) for behavior details.

## Switching Between Cache Types

### From CR Cache to Database Cache

**1. Deploy and initialize PostgreSQL:**
```bash
# Deploy PostgreSQL
kubectl apply -f deployments/porch/3-porch-postgres-bundle.yaml

# Initialize schema
PGPASSWORD=porch psql -h <db-host> -U porch -d porch -f api/sql/porch-db.sql
```

**2. Create database secret** (see Configuration Steps above)

**3. Update Porch Server deployment:**
- Change `--cache-type=CR` to `--cache-type=DB`
- Add database environment variables (DB_HOST, DB_NAME, DB_USER, DB_PASSWORD, DB_DRIVER)

**4. Update Repository Controller deployment:**
- Change `--repositories.cache-type=CR` to `--repositories.cache-type=DB`
- Add database environment variables (DB_HOST, DB_NAME, DB_USER, DB_PASSWORD)

**5. Restart components:**
```bash
kubectl rollout restart deployment/porch-server -n porch-system
kubectl rollout restart deployment/porch-controllers -n porch-system
```

**6. Verify:**
```bash
# Check pods are running
kubectl get pods -n porch-system

# Check repositories are syncing
kubectl get repositories -A
```

## Related Documentation

- [Repository Controller Architecture]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}) - How cache modes work
- [Repository Sync Configuration]({{% relref "repository-sync" %}}) - Sync scheduling and tuning
- [Porch Controllers Configuration]({{% relref "components/porch-controllers-config" %}}) - Controller flags and settings
