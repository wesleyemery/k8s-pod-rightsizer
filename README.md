# Kubernetes Pod Right-sizer

A custom Kubernetes controller built with controller-runtime that automatically analyzes and optimizes pod resource requests and limits based on historical usage patterns. This tool replicates core functionality of commercial solutions like ScaleOps, providing in-house resource optimization for your AKS clusters.

## 🚀 Features

- **Historical Analysis**: Analyzes pod resource usage over configurable time windows (7-30 days)
- **Smart Recommendations**: Generates CPU/memory recommendations based on percentile analysis
- **Multiple Update Strategies**: Supports immediate, gradual, and manual recommendation application
- **Prometheus Integration**: Collects metrics from Prometheus for production-grade analysis
- **Metrics Server Fallback**: Works with Kubernetes metrics-server for basic functionality
- **Safety Controls**: Configurable safety margins, min/max constraints, and confidence scoring
- **Workload Awareness**: Supports Deployments, StatefulSets, DaemonSets, and Jobs
- **Dry-run Mode**: Generate recommendations without applying changes

## 📋 Prerequisites

- Go 1.21+
- Docker
- kubectl
- Access to a Kubernetes cluster (AKS recommended)
- Prometheus (recommended) or Kubernetes Metrics Server
- kubebuilder (for development)

## 🛠️ Installation

### Step 1: Setup Development Environment

1. **Install kubebuilder**:
```bash
curl -L -o kubebuilder https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)
chmod +x kubebuilder && sudo mv kubebuilder /usr/local/bin/
```

2. **Clone and setup the project**:
```bash
git clone <your-repo-url>
cd k8s-pod-rightsizer

# Add required dependencies
go mod tidy
```

### Step 2: Deploy to Your Cluster

1. **Install CRDs**:
```bash
make install
```

2. **Build and deploy the controller**:
```bash
# For local testing
make run

# For cluster deployment
make docker-build IMG=your-registry/pod-rightsizer:v0.1.0
make docker-push IMG=your-registry/pod-rightsizer:v0.1.0
make deploy IMG=your-registry/pod-rightsizer:v0.1.0
```

## 🧪 Testing Guide

### Test Environment Setup

#### Option 1: Local Kind Cluster with Sample Workload

1. **Create a Kind cluster**:
```bash
kind create cluster --name rightsizer-test
```

2. **Deploy sample application**:
```bash
# Create a test deployment
kubectl create deployment test-app --image=nginx:latest --replicas=3
kubectl set resources deployment test-app --requests=cpu=100m,memory=128Mi --limits=cpu=500m,memory=512Mi

# Add labels for targeting
kubectl label deployment test-app app=test-webapp
```

3. **Install Prometheus (simplified)**:
```bash
# Add Prometheus Helm repo
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Install Prometheus with minimal config
helm install prometheus prometheus-community/kube-prometheus-stack \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.retention=7d
```

4. **Wait for metrics collection** (5-10 minutes for initial data).

#### Option 2: Existing AKS Cluster

1. **Ensure Prometheus is available**:
```bash
# Check if Prometheus is running
kubectl get svc -n monitoring prometheus-server

# If using Azure Monitor, configure the metrics source accordingly
```

2. **Deploy test workload** (if needed):
```bash
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: resource-test-app
  labels:
    app: test-webapp
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-webapp
  template:
    metadata:
      labels:
        app: test-webapp
    spec:
      containers:
      - name: app
        image: nginx:alpine
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 200m
            memory: 256Mi
        # Add some CPU/memory load
        command: ["/bin/sh"]
        args:
        - -c
        - |
          # Start nginx in background
          nginx -g 'daemon off;' &
          # Generate some CPU load
          while true; do
            dd if=/dev/zero of=/dev/null bs=1024 count=1000 2>/dev/null
            sleep 10
          done
EOF
```

### Step-by-Step Testing

#### Test 1: Dry-Run Analysis

1. **Create a dry-run PodRightSizing resource**:
```bash
kubectl apply -f - <<EOF
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: test-dryrun
  namespace: default
spec:
  target:
    namespace: default
    labelSelector:
      matchLabels:
        app: test-webapp
  analysisWindow: "1h"  # Short window for testing
  dryRun: true
  thresholds:
    cpuUtilizationPercentile: 90
    memoryUtilizationPercentile: 90
    safetyMargin: 20
    minCpu: 10m
    minMemory: 32Mi
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-server.monitoring.svc.cluster.local"
EOF
```

2. **Monitor the analysis**:
```bash
# Watch the PodRightSizing resource
kubectl get podrightsizing test-dryrun -w

# Check detailed status
kubectl describe podrightsizing test-dryrun

# View controller logs
kubectl logs -n pod-rightsizer-system deployment/pod-rightsizer-controller-manager -f
```

3. **Examine recommendations**:
```bash
kubectl get podrightsizing test-dryrun -o yaml
```

#### Test 2: Gradual Update Strategy

1. **Create a gradual update configuration**:
```bash
kubectl apply -f - <<EOF
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: test-gradual
  namespace: default
spec:
  target:
    namespace: default
    labelSelector:
      matchLabels:
        app: test-webapp
  analysisWindow: "2h"
  dryRun: false
  updatePolicy:
    strategy: gradual
    maxUnavailable: "50%"
    minStabilityPeriod: "2m"
  thresholds:
    cpuUtilizationPercentile: 95
    memoryUtilizationPercentile: 95
    safetyMargin: 25
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-server.monitoring.svc.cluster.local"
EOF
```

2. **Monitor the update process**:
```bash
# Watch pods for updates
kubectl get pods -l app=test-webapp -w

# Monitor the right-sizing progress
kubectl get podrightsizing test-gradual -o yaml | grep -A 20 status
```

#### Test 3: Manual Strategy with Multiple Workloads

1. **Create multiple test workloads**:
```bash
# CPU-intensive workload
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cpu-intensive
  labels:
    workload-type: compute
spec:
  replicas: 2
  selector:
    matchLabels:
      app: cpu-app
  template:
    metadata:
      labels:
        app: cpu-app
        workload-type: compute
    spec:
      containers:
      - name: cpu-worker
        image: alpine:latest
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 256Mi
        command: ["/bin/sh", "-c"]
        args: ["while true; do dd if=/dev/zero of=/dev/null bs=1M count=100; sleep 5; done"]
EOF

# Memory-intensive workload  
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: memory-intensive
  labels:
    workload-type: memory
spec:
  replicas: 1
  selector:
    matchLabels:
      app: memory-app
  template:
    metadata:
      labels:
        app: memory-app
        workload-type: memory
    spec:
      containers:
      - name: memory-worker
        image: alpine:latest
        resources:
          requests:
            cpu: 50m
            memory: 256Mi
          limits:
            cpu: 200m
            memory: 1Gi
        command: ["/bin/sh", "-c"]
        args: ["dd if=/dev/zero of=/tmp/memory.tmp bs=1M count=500; sleep 3600"]
EOF
```

2. **Create comprehensive right-sizing policy**:
```bash
kubectl apply -f - <<EOF
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: comprehensive-test
  namespace: default
spec:
  target:
    namespaceSelector:
      matchLabels:
        rightsizing: "enabled"
    includeWorkloadTypes:
    - "Deployment"
    - "StatefulSet"
    excludeNamespaces:
    - "kube-system"
    - "kube-public"
  analysisWindow: "24h"
  schedule: "0 */6 * * *"  # Every 6 hours
  dryRun: false
  updatePolicy:
    strategy: manual  # Generate recommendations only
  thresholds:
    cpuUtilizationPercentile: 90
    memoryUtilizationPercentile: 95
    safetyMargin: 15
    minChangeThreshold: 20
    minCpu: 10m
    minMemory: 64Mi
    maxCpu: 8
    maxMemory: 16Gi
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-server.monitoring.svc.cluster.local"
EOF

# Label the namespace for targeting
kubectl label namespace default rightsizing=enabled
```

## 📊 Monitoring and Observability

### View Recommendations

1. **List all right-sizing resources**:
```bash
kubectl get podrightsizing -A
```

2. **Get detailed recommendations**:
```bash
kubectl get podrightsizing <name> -o jsonpath='{.status.recommendations}' | jq '.'
```

3. **Check resource utilization**:
```bash
# Current resource requests
kubectl describe pods -l app=test-webapp | grep -A 5 "Requests"

# Compare with recommendations
kubectl get podrightsizing test-dryrun -o jsonpath='{.status.recommendations[0].recommendedResources}' | jq '.'
```

### Controller Metrics

The controller exposes metrics that can be monitored:

```bash
# Port-forward to access metrics
kubectl port-forward -n pod-rightsizer-system svc/pod-rightsizer-controller-manager-metrics-service 8443:8443

# Query metrics (with proper authentication)
curl -k https://localhost:8443/metrics
```

## 🔧 Troubleshooting

### Common Issues

1. **No metrics available**:
```bash
# Check Prometheus connectivity
kubectl exec -n pod-rightsizer-system deployment/pod-rightsizer-controller-manager -- \
  curl -v http://prometheus-server.monitoring.svc.cluster.local:80/api/v1/query?query=up

# Verify metrics-server is running
kubectl get deployment metrics-server -n kube-system
```

2. **Insufficient data points**:
```bash
# Check if pods have been running long enough
kubectl get pods -l app=test-webapp -o wide

# Verify Prometheus is scraping metrics
kubectl port-forward -n monitoring svc/prometheus-server 9090:80
# Visit http://localhost:9090 and query: container_cpu_usage_seconds_total
```

3. **RBAC issues**:
```bash
# Check controller permissions
kubectl auth can-i get pods --as=system:serviceaccount:pod-rightsizer-system:pod-rightsizer-controller-manager
kubectl auth can-i update deployments --as=system:serviceaccount:pod-rightsizer-system:pod-rightsizer-controller-manager
```

4. **Controller not reconciling**:
```bash
# Check controller logs
kubectl logs -n pod-rightsizer-system deployment/pod-rightsizer-controller-manager -f

# Verify CRDs are installed
kubectl get crd podrightsizings.rightsizing.k8s-rightsizer.io

# Check for webhook issues (if applicable)
kubectl get validatingwebhookconfigurations
kubectl get mutatingwebhookconfigurations
```

### Debug Mode

Enable verbose logging:

```bash
# Update controller args for debug mode
kubectl patch deployment -n pod-rightsizer-system pod-rightsizer-controller-manager \
  --type='json' \
  -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--zap-log-level=debug"}]'
```

## 🚀 Production Deployment

### AKS-Specific Configuration

1. **Use Azure Monitor for metrics** (if not using Prometheus):
```yaml
spec:
  metricsSource:
    type: metrics-server
```

2. **Configure for AKS node pools**:
```yaml
spec:
  target:
    namespaceSelector:
      matchLabels:
        environment: production
    includeWorkloadTypes:
    - "Deployment"
    - "StatefulSet"
  thresholds:
    # Conservative settings for production
    cpuUtilizationPercentile: 90
    memoryUtilizationPercentile: 95
    safetyMargin: 30
    minChangeThreshold: 25
```

3. **Production-ready scheduling**:
```yaml
spec:
  schedule: "0 2 * * 1"  # Weekly on Monday at 2 AM
  updatePolicy:
    strategy: gradual
    maxUnavailable: "10%"
    minStabilityPeriod: "15m"
```

### Security Considerations

1. **Network policies** (if using Calico/Azure CNI):
```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: pod-rightsizer-netpol
  namespace: pod-rightsizer-system
spec:
  podSelector:
    matchLabels:
      control-plane: controller-manager
  egress:
  - to:
    - namespaceSelector:
        matchLabels:
          name: monitoring
    ports:
    - protocol: TCP
      port: 9090
```

2. **Pod Security Standards**:
```bash
kubectl label namespace pod-rightsizer-system \
  pod-security.kubernetes.io/enforce=restricted \
  pod-security.kubernetes.io/audit=restricted \
  pod-security.kubernetes.io/warn=restricted
```

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📝 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- Built with [Kubebuilder](https://kubebuilder.io/)
- Inspired by [ScaleOps](https://www.scaleops.com/) and similar commercial solutions
- Uses [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) for Kubernetes integration