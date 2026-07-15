Copyright 2024 The Nephio Authors

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
- Kpt CLI
- Go development environment
- Access to GitHub repositories

## 1. Set Up Development Environment
Run the following script from the porch directory to set up the development environment:
```bash
./scripts/setup-dev-env.sh
```

## 2. Build and Deploy Porch
Execute the command below to build and deploy Porch in a Kubernetes Kind cluster:
```bash
make run-in-kind
```
Once deployment is complete, you should see the related pods running.

## 3. Deploy Monitoring Stack (Optional)

```bash
./scripts/deploy-monitoring.sh deploy
```

This will:
- Create a `porch-monitoring` namespace
- Deploy Prometheus on port 9090 inside the cluster
- Deploy Grafana on port 3000 inside the cluster
- Configure Prometheus as a Grafana datasource
- Load the Porch performance dashboard
- Setup port-forwarding: Prometheus on localhost:9092, Grafana on localhost:3001

**Important for kind clusters**: Prometheus is configured to scrape metrics from the host machine via the Docker gateway IP (172.17.0.1). The performance test exposes metrics on port 9095 on the host, and Prometheus running inside the kind cluster accesses them via `172.17.0.1:9095`.

### Access the Monitoring Tools

- **Prometheus**: http://localhost:9092 (via port-forward)
- **Grafana**: http://localhost:3001/dashboards

### Cleanup Monitoring Stack
```bash
./deploy-monitoring.sh cleanup
```
This will delete all monitoring resources and stop port forwarding.

### Restart Monitoring Stack
```bash
./deploy-monitoring.sh restart
```

## 4. Run Performance Tests
Navigate to the performance test directory:
```bash
cd test/performance/
```

There are two main performance tests available:

### Run Load Test
Creates a preset amount of repositories, packages, and revisions to simulate load on the Porch system.
```bash
LOAD_TEST=1 go test -v ./... -timeout 1h
```
Make sure to scale the timeout as needed and adjust test parameters as required.
Namely `-repos`, `-packages`, and `-revisions` to simulate the desired load and `-enable-prometheus` to enable visual metrics.

### Maximum Package Revisions Test
Tests the maximum number of package revisions that can be handled by Porch in a single repository.
```bash
MAX_PR_TEST=1 go test -v ./... -timeout 1h
```
Make sure to scale the timeout as needed and adjust test parameters as required -> recommended is 72h.

### Available Test Parameters:
- `-repos`: Number of repositories to test
- `-packages`: Number of packages per repository
- `-revisions`: Number of package revisions per package
- `-repo-parallelism`: Number of repositories to create in parallel
- `-package-parallelism`: Number of packages to create in parallel per repository
- `-enable-deletion`: Enable deletion of package revisions at the end of the test
- `-error-rate`: Maximum percentage of package revisions allowed to fail lifecycle transition
- `-enable-prometheus`: Enable Prometheus metrics server (default: false) -> **do not enable if monitoring stack is not deployed**
- `-metrics-log-prefix`: Prefix for the timestamped metrics log file
- `-results-file`: File name for test results
- `-detailed-log-file`: File name for detailed log
- `-repo-results-csv`: File name for repository results CSV
- `-operations-csv`: File name for operations details CSV
- `-deletion-csv`: File name for deletion operations CSV
- `-kptfile-path`: Path to the Kptfile
- `-package-resources-path`: Path to the package resources

All results are stored in logs files located in the `test/performance/` and `test/performance/logs` directories.

## 5. Sample Output

```bash
LOAD_TEST=1 go test -v ./test/performance -namespace=db-demo -repos=1 -packages=1 -revisions=3 -enable-prometheus=true -enable-deletion=true -timeout 1h
```

```
=== Consolidated Performance Test Results ===
Operation                              Min         Max         Avg         Total
------------------------------------------------------------------------------------
Create Gitea Repository   R0          272ms       272ms       272ms       272ms      
Create Porch Repository   R0          3ms         3ms         3ms         3ms        
Repository Ready Wait  R0             2.004s      2.004s      2.004s      2.004s     
Package Revision List v1              9ms         9ms         9ms         9ms        
Package Revision List v2              6ms         6ms         6ms         6ms        
Package Revision List v3              6ms         6ms         6ms         6ms        
Package Revision Create v1            10ms        10ms        10ms        10ms       
Package Revision Create v2            9ms         9ms         9ms         9ms        
Package Revision Create v3            9ms         9ms         9ms         9ms        
Package Revision Render v1            4ms         4ms         4ms         4ms        
Package Revision Render v2            14ms        14ms        14ms        14ms       
Package Revision Render v3            13ms        13ms        13ms        13ms       
Package Revision Get Resources v1     3ms         3ms         3ms         3ms        
Package Revision Get Resources v2     3ms         3ms         3ms         3ms        
Package Revision Get Resources v3     3ms         3ms         3ms         3ms        
Package Revision Update v1            10ms        10ms        10ms        10ms       
Package Revision Update v2            10ms        10ms        10ms        10ms       
Package Revision Update v3            9ms         9ms         9ms         9ms        
Package Revision Render v1            14ms        14ms        14ms        14ms       
Package Revision Render v2            10ms        10ms        10ms        10ms       
Package Revision Render v3            10ms        10ms        10ms        10ms       
Package Revision Get v1               2ms         2ms         2ms         2ms        
Package Revision Get v2               2ms         2ms         2ms         2ms        
Package Revision Get v3               2ms         2ms         2ms         2ms        
Package Revision Propose v1           11ms        11ms        11ms        11ms       
Package Revision Propose v2           9ms         9ms         9ms         9ms        
Package Revision Propose v3           10ms        10ms        10ms        10ms       
Package Revision Get (Proposed) v1    2ms         2ms         2ms         2ms        
Package Revision Get (Proposed) v2    2ms         2ms         2ms         2ms        
Package Revision Get (Proposed) v3    3ms         3ms         3ms         3ms        
Package Revision Approve/Publish v1   348ms       348ms       348ms       348ms      
Package Revision Approve/Publish v2   339ms       339ms       339ms       339ms      
Package Revision Approve/Publish v3   340ms       340ms       340ms       340ms      
Package Revision Propose Deletion v1  8ms         8ms         8ms         8ms        
Package Revision Propose Deletion v2  6ms         6ms         6ms         6ms        
Package Revision Propose Deletion v3  9ms         9ms         9ms         9ms        
Package Revision Delete v1            262ms       262ms       262ms       262ms      
Package Revision Delete v2            262ms       262ms       262ms       262ms      
Package Revision Delete v3            269ms       269ms       269ms       269ms  
```
