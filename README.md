

# Kubernetes Pod Right-sizer

A Kubernetes custom controller that automatically analyzes and optimizes pod resource requests and limits based on historical usage patterns. This tool provides intelligent resource recommendations to improve cluster efficiency and reduce costs.

## Features

* **Historical Analysis**: Analyze pod resource usage over configurable time windows
* **Smart Recommendations**: Generate CPU/memory recommendations based on percentile analysis
* **Multiple Update Strategies**: Supports immediate, gradual, and manual recommendation application
* **Prometheus Integration**: Collect metrics from Prometheus for production-grade analysis
* **Metrics Server Fallback**: Works with Kubernetes metrics-server for basic functionality
* **Safety Controls**: Configurable safety margins, min/max constraints, and confidence scoring
* **Workload Awareness**: Supports Deployments, StatefulSets, DaemonSets, and Jobs
* **Real-time Azure Pricing**: Automatically fetches current Azure VM pricing using Azure Retail Prices API
* **Node SKU Detection**: Discovers Azure VM SKU information from cluster nodes
* **Cost Breakdown by SKU**: Detailed savings analysis per Azure VM SKU type
* **Dry-run Mode**: Generate recommendations without applying changes
* **Validation Webhooks**: Comprehensive validation for configuration correctness

## Quick Start

### Prerequisites

* Kubernetes cluster (v1.20+)
* Go 1.21+ (for development)
* kubectl configured
* Prometheus (recommended) or Kubernetes Metrics Server

### Installation

1. **Install CRDs and Controller**:

   ```bash
   # Clone the repository
   git clone https://github.com/your-org/k8s-pod-rightsizer
   cd k8s-pod-rightsizer

   # Install CRDs
   make install

   # Deploy the controller
   make deploy IMG=your-registry/pod-rightsizer:latest
   ```

2. **Verify Installation**:

   ```bash
   kubectl get pods -n pod-rightsizer-system
   kubectl get crd podrightsizings.rightsizing.k8s-rightsizer.io
   ```

## Basic Usage Examples

### Example 1: Simple Dry-Run Analysis

```yaml
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: webapp-analysis
  namespace: default
spec:
  target:
    namespace: default
    labelSelector:
      matchLabels:
        app: webapp
  analysisWindow: "24h"
  dryRun: true
  thresholds:
    cpuUtilizationPercentile: 95
    memoryUtilizationPercentile: 95
    safetyMargin: 20
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-server.monitoring.svc.cluster.local:9090"
```

### Example 2: Automated Gradual Updates

```yaml
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: production-optimizer
  namespace: prod
spec:
  target:
    namespace: prod
    includeWorkloadTypes:
    - "Deployment"
    - "StatefulSet"
  analysisWindow: "168h"  # 7 days
  schedule: "0 2 * * 1"   # Every Monday at 2 AM
  dryRun: false
  updatePolicy:
    strategy: gradual
    maxUnavailable: "25%"
    minStabilityPeriod: "5m"
  thresholds:
    cpuUtilizationPercentile: 90
    memoryUtilizationPercentile: 95
    safetyMargin: 30
    minChangeThreshold: 15
    minCpu: "10m"
    minMemory: "64Mi"
    maxCpu: "4"
    maxMemory: "8Gi"
```

### Example 3: Multi-Namespace with Custom Metrics

```yaml
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: multi-namespace-analysis
  namespace: rightsizing-system
spec:
  target:
    namespaceSelector:
      matchLabels:
        rightsizing: "enabled"
    excludeNamespaces:
    - "kube-system"
    - "kube-public"
  analysisWindow: "72h"
  schedule: "0 3 * * *"
  updatePolicy:
    strategy: manual
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "https://prometheus.example.com:9090"
      authConfig:
        type: bearer
        secretRef:
          name: prometheus-token
          namespace: rightsizing-system
```

### Example 4: Development Environment

```yaml
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: dev-quick-optimize
  namespace: development
spec:
  target:
    namespace: development
    labelSelector:
      matchLabels:
        environment: dev
  analysisWindow: "2h"
  updatePolicy:
    strategy: immediate
  thresholds:
    cpuUtilizationPercentile: 80
    memoryUtilizationPercentile: 85
    safetyMargin: 10
    minChangeThreshold: 5
  metricsSource:
    type: metrics-server
```

## Viewing Recommendations

```bash
# List all PodRightSizing resources
kubectl get podrightsizing -A

# Get detailed status
kubectl describe podrightsizing webapp-analysis

# View recommendations in JSON format
kubectl get podrightsizing webapp-analysis -o json | jq '.status.recommendations'
```

### Sample Recommendation Output

```json
{
  "status": {
    "phase": "Completed",
    "targetedPods": 3,
    "updatedPods": 0,
    "lastAnalysisTime": "2025-01-15T10:30:00Z",
    "recommendations": [
      {
        "podReference": {
          "name": "webapp-deployment-abc123",
          "namespace": "default",
          "workloadType": "Deployment",
          "workloadName": "webapp-deployment"
        },
        "currentResources": {
          "requests": {
            "cpu": "100m",
            "memory": "256Mi"
          },
          "limits": {
            "cpu": "500m",
            "memory": "512Mi"
          }
        },
        "recommendedResources": {
          "requests": {
            "cpu": "80m",
            "memory": "180Mi"
          },
          "limits": {
            "cpu": "200m",
            "memory": "300Mi"
          }
        },
        "confidence": 85,
        "reason": "Based on 95th percentile of 144 data points. Applied 20% safety margin.",
        "potentialSavings": {
          "cpuSavings": "20m",
          "memorySavings": "76Mi",
          "costSavings": "$12.50/month"
        }
      }
    ],
    "nodeSKUBreakdown": {
      "Standard_D2s_v3": {
        "skuName": "Standard_D2s_v3",
        "nodeCount": 2,
        "totalMonthlyCost": 140.16,
        "potentialSavings": 25.30,
        "recommendationCount": 5,
        "cpuCostPerCore": 35.04,
        "memoryCostPerGB": 8.76
      },
      "Standard_D4s_v3": {
        "skuName": "Standard_D4s_v3", 
        "nodeCount": 1,
        "totalMonthlyCost": 140.16,
        "potentialSavings": 18.75,
        "recommendationCount": 3,
        "cpuCostPerCore": 35.04,
        "memoryCostPerGB": 8.76
      }
    },
    "usingRealPricing": true,
    "pricingDataAge": "2.3 hours ago"
  }
}
```

## Configuration Reference

### Target Specification

| Field                  | Type          | Description                                  |
| ---------------------- | ------------- | -------------------------------------------- |
| `namespace`            | string        | Target specific namespace                    |
| `labelSelector`        | LabelSelector | Target pods matching labels                  |
| `namespaceSelector`    | LabelSelector | Target pods in labeled namespaces            |
| `excludeNamespaces`    | \[]string     | Namespaces to exclude                        |
| `includeWorkloadTypes` | \[]string     | Workload types to include (Deployment, etc.) |

### Threshold Configuration

| Field                         | Default | Description                                   |
| ----------------------------- | ------- | --------------------------------------------- |
| `cpuUtilizationPercentile`    | 95      | CPU percentile to target (0-100)              |
| `memoryUtilizationPercentile` | 95      | Memory percentile to target (0-100)           |
| `safetyMargin`                | 20      | Safety margin percentage                      |
| `minChangeThreshold`          | 10      | Minimum change required to trigger update (%) |
| `minCpu`                      | -       | Minimum CPU request                           |
| `maxCpu`                      | -       | Maximum CPU request                           |
| `minMemory`                   | -       | Minimum memory request                        |
| `maxMemory`                   | -       | Maximum memory request                        |

### Update Strategies

| Strategy    | Description                      | Use Case                                   |
| ----------- | -------------------------------- | ------------------------------------------ |
| `manual`    | Generate recommendations only    | Production environments requiring approval |
| `gradual`   | Rolling updates with constraints | Production with automatic updates          |
| `immediate` | Apply all changes at once        | Development/testing                        |

## Advanced Configuration

### Azure Real-time Pricing

The controller automatically detects Azure VM SKUs and fetches current pricing data from the Azure Retail Prices API. This provides accurate cost calculations based on your actual Azure VM types and regional pricing.

#### Features:
- **Automatic SKU Detection**: Discovers VM SKU from node labels (`node.kubernetes.io/instance-type`)
- **Regional Pricing**: Uses region-specific pricing from node topology labels
- **24-hour Caching**: Caches pricing data to reduce API calls
- **Fallback to Defaults**: Uses conservative estimates if pricing API is unavailable

#### Supported Azure VM Series:
- **D-series** (General purpose): D2s_v3, D4s_v3, D8s_v3, etc.
- **E-series** (Memory optimized): E2s_v3, E4s_v3, E8s_v3, etc.
- **F-series** (Compute optimized): F2s_v2, F4s_v2, F8s_v2, etc.
- **B-series** (Burstable): B1ms, B2s, B4ms, etc.

### Custom Prometheus Queries

```yaml
env:
- name: PROMETHEUS_CPU_QUERY
  value: 'rate(container_cpu_usage_seconds_total[5m])'
- name: PROMETHEUS_MEMORY_QUERY  
  value: 'container_memory_working_set_bytes'
```

### Scaling the Controller

```yaml
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: manager
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: 500m
            memory: 512Mi
        env:
        - name: MAX_CONCURRENT_RECONCILES
          value: "10"
```

## Troubleshooting

### Common Issues

1. **No metrics available**:

   ```bash
   kubectl logs -n pod-rightsizer-system deployment/pod-rightsizer-controller-manager
   kubectl top nodes
   ```

2. **Insufficient permissions**:

   ```bash
   kubectl auth can-i get pods --as=system:serviceaccount:pod-rightsizer-system:pod-rightsizer-controller-manager
   ```

3. **Webhook validation errors**:

   ```bash
   kubectl logs -n pod-rightsizer-system deployment/pod-rightsizer-controller-manager | grep webhook
   ```

### Debug Mode

```bash
kubectl patch deployment -n pod-rightsizer-system pod-rightsizer-controller-manager \
  --type='json' \
  -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--zap-log-level=debug"}]'
```

## Monitoring

The controller exposes Prometheus metrics:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: pod-rightsizer-metrics
spec:
  selector:
    matchLabels:
      control-plane: controller-manager
  endpoints:
  - port: https
```

### Key Metrics

* `pod_rightsizing_recommendations_total` – Total recommendations generated
* `pod_rightsizing_updates_total` – Total resource updates applied
* `pod_rightsizing_analysis_duration_seconds` – Time taken for analysis

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/awesome-feature`)
3. Make changes and add tests
4. Run tests (`make test`)
5. Commit changes (`git commit -m 'Add awesome feature'`)
6. Push to branch (`git push origin feature/awesome-feature`)
7. Create a Pull Request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

* Built with [Kubebuilder](https://kubebuilder.io/)
* Uses [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)

## Resources

* [Kubernetes Resource Management](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)
* [Prometheus Metrics](https://prometheus.io/docs/concepts/metric_types/)
* [Kubebuilder Book](https://book.kubebuilder.io/)
