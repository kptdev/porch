Copyright 2024, 2026 The kpt Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

      http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.


# Porch Performance Testing Setup Guide

## Prerequisites
- Docker
- Kubernetes CLI (kubectl)
- Go development environment
- Access to GitHub repositories

## 1. Set Up Development Environment
Run the following script to set up the development environment:
```bash
https://github.com/kptdev/porch/blob/main/scripts/setup-dev-env.sh
```

## 2. Build and Deploy Porch
Execute the command below to build and deploy Porch in a Kubernetes Kind cluster:
```bash
make run-in-kind
```
Once deployment is complete, you should see the related pods running.

## 3. Create a Demo Namespace
Create a namespace for the demo setup:
```bash
kubectl create namespace porch-demo
```

## 4. Define Gitea Repositories in Porch
Apply the repository configuration file:
```bash
kubectl apply -f examples/tutorials/starting-with-porch/porch-repositories.yaml
```

Note: The Gitea credentials secret is automatically created when running the tests. The configuration can be found in `gitea-secret.yaml`.

## 5. Verify Repository Setup
Check the status of repositories in the porch-demo namespace:
```bash
kubectl get repositories -n porch-demo
```

## 6. Run Performance Tests
Navigate to the performance test directory:
```bash
cd test/performance/
```

Execute the performance test command:
```bash
E2E=1 go test -v ./... -repos=4 -packages=4 -timeout 60m
```

Parameters:
- `repos`: Number of repositories to test
- `packages`: Number of package revisions in each repository

Once the tests begin, the logs can be observed in logs dir a sample is shared below.
```
=== Iteration 0 Results ===
Operation                  Duration    Status
--------------------------------------------------
Create Gitea Repository   331ms      Success
Create Porch Repository   7ms        Success
Wait for Porch Repository Ready 2.008s     Success

=== Iteration 1 Results ===
Operation                  Duration    Status
--------------------------------------------------
Create PackageRevision    457ms      Success
Update to Proposed        561ms      Success
Update to Published       699ms      Success
Delete PackageRevision    3.171s     Success

=== Iteration 1 Results ===
Operation                  Duration    Status
--------------------------------------------------
Delete Repository         5ms        Success

=== Consolidated Performance Test Results ===
Operation                  Min         Max         Avg         Total
------------------------------------------------------------------------
Update to Published       699ms       699ms       699ms       699ms
Delete PackageRevision    3.171s      3.171s      3.171s      3.171s
Create Gitea Repository   331ms       331ms       331ms       331ms
Create Porch Repository   7ms         7ms         7ms         7ms
Wait for Porch Repository Ready 2.008s      2.008s      2.008s      2.008s
Create PackageRevision    457ms       457ms       457ms       457ms
Update to Proposed        561ms       561ms       561ms       561ms
```

## 7. Verify Prometheus Targets
Check the Prometheus targets:
```
http://localhost:9090/targets
```
You should be able to run PromQL queries here.

## 8. Check Metrics Output
Retrieve the captured metrics using:
```bash
curl -k http://localhost:2113/metrics
```
This should return all the collected performance metrics from the test execution.

## Prometheus Setup

The performance testing framework uses a Kubernetes-based Prometheus setup. The configuration is stored in `prometheus-manifests.yaml` and includes:

- ConfigMap with Prometheus configuration
- Deployment for the Prometheus pod
- Service to expose Prometheus

The Prometheus instance is automatically deployed in the `porch-demo` namespace when running the tests. You can access the Prometheus UI at:
```
http://localhost:9090
```

The configuration can be modified by editing `prometheus-manifests.yaml`. Key settings include:
- Scrape interval: 1s
- Resource limits: 512Mi memory, 200m CPU
- NodePort: 30090