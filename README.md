# TiDB Cloud Operator

> ⚠️ **BETA SOFTWARE** - This project is in active development and is not yet production ready. Features may change, and breaking changes may occur between releases.

A Kubernetes Operator for managing TiDB Cloud clusters with automated scaling and scheduling capabilities.

## Features

- **Automated Scaling**: Schedule TiDB Cloud cluster scaling operations based on cron expressions
- **Cost Optimization**: Automatically suspend and resume clusters during off-hours or weekends
- **Timezone Support**: Schedule operations in any timezone (IANA format)
- **Multi-Schedule Support**: Configure multiple schedules per cluster for different operational patterns

## Description

The TiDB Cloud Operator provides Kubernetes-native management of TiDB Cloud Dedicated clusters through Custom Resource Definitions (CRDs). It enables you to:

- Schedule automatic cluster scaling operations (scale up during business hours, scale down during off-hours)
- Implement cost optimization strategies by suspending clusters when not needed
- Configure complex scheduling patterns with timezone awareness
- Manage multiple clusters with different scheduling requirements

## Prerequisites

- Go version v1.21+
- Docker version 17.03+
- kubectl version v1.11.3+
- Access to a Kubernetes v1.11.3+ cluster
- TiDB Cloud account with API credentials

## Quick Start

### 1. Install the CRDs

```sh
make install
```

### 2. Configure TiDB Cloud Credentials

Create a Kubernetes secret with your TiDB Cloud API credentials:

```sh
kubectl create secret generic tidbcloud-credentials \
  --from-literal=public-key=YOUR_PUBLIC_KEY \
  --from-literal=private-key=YOUR_PRIVATE_KEY
```

### 3. Deploy the Operator

```sh
make deploy
```

### 4. Create a TiDBClusterScheduler

Apply the sample configuration:

```sh
kubectl apply -f config/samples/scheduler_v1_tidbclusterscheduler.yaml
```

Make sure to update the sample with your actual:
- Project ID
- Cluster ID
- Cluster Name

## Configuration Example

```yaml
apiVersion: scheduler.tsurai.jp/v1
kind: TiDBClusterScheduler
metadata:
  name: my-cluster-scheduler
spec:
  clusterRef:
    projectId: "YOUR_PROJECT_ID"
    clusterId: "YOUR_CLUSTER_ID"
    clusterName: "your-cluster-name"
    credentialsRef:
      name: tidbcloud-credentials

  timezone: "Asia/Tokyo"

  schedules:
    # Scale up for business hours (9 AM JST, Monday-Friday)
    - schedule: "0 9 * * 1-5"
      description: "Scale up for business hours"
      scale:
        tidb:
          nodeCount: 4
          nodeSize: "8C16G"
        tikv:
          nodeCount: 6
          nodeSize: "16C64G"
          storage: 1000

    # Scale down for night time (11 PM JST, Monday-Friday)
    - schedule: "0 23 * * 1-5"
      description: "Scale down for night time"
      scale:
        tidb:
          nodeCount: 2
          nodeSize: "4C8G"
        tikv:
          nodeCount: 3
          nodeSize: "8C16G"
          storage: 500

    # Weekend suspension (Saturday 2 AM JST)
    - schedule: "0 2 * * 6"
      description: "Weekend cost optimization - suspend"
      suspend: true
      settings:
        force: false
        gracePeriod: "10m"
        skipIfScaledDown: true

    # Resume on Monday (8 AM JST)
    - schedule: "0 8 * * 1"
      description: "Resume for weekday operations"
      resume: true

  settings:
    concurrencyLimit: 2
    operationTimeout: "30m"
    retryConfig:
      maxRetries: 3
      initialDelay: "1m"
      backoffMultiplier: 200
      maxDelay: "16m"
```

## Node Size Options

TiDB Cloud supports the following node sizes:

- `2C8G` - 2 vCPU, 8GB RAM
- `4C8G` - 4 vCPU, 8GB RAM
- `4C16G` - 4 vCPU, 16GB RAM
- `8C16G` - 8 vCPU, 16GB RAM
- `8C32G` - 8 vCPU, 32GB RAM
- `16C32G` - 16 vCPU, 32GB RAM
- `16C64G` - 16 vCPU, 64GB RAM
- And more...

## Development

### Run Tests

```sh
make test
```

### Run Locally

```sh
make run
```

### Build and Deploy Custom Image

```sh
make docker-build docker-push IMG=<your-registry>/tidbcloud-operator:tag
make deploy IMG=<your-registry>/tidbcloud-operator:tag
```

## Uninstall

```sh
# Delete all scheduler instances
kubectl delete -k config/samples/

# Remove the operator
make undeploy

# Remove CRDs
make uninstall
```

## Troubleshooting

Check operator logs:
```sh
kubectl logs -n tidbcloud-operator-system deployment/tidbcloud-operator-controller-manager
```

Check scheduler status:
```sh
kubectl describe tidbclusterscheduler <scheduler-name>
```

## Contributing

This project is in beta. Please report issues and feature requests through GitHub issues.

## License

Licensed under the Apache License, Version 2.0. See LICENSE file for details.

---

> **Note**: This is beta software. Use in production environments at your own risk.