# Pod Right-sizer Testing Guide

## Quick Test (5 minutes)

```bash
# 1. Make the test script executable
chmod +x scripts/test-setup.sh

# 2. Run mock-only test
./scripts/test-setup.sh mock-only
```

## Manual Step-by-Step Testing

### Step 1: Setup Dependencies
```bash
# Update Go dependencies
go get github.com/prometheus/client_golang@latest
go get github.com/prometheus/common@latest
go mod tidy

# Generate and install CRDs
make generate manifests install
```

### Step 2: Deploy Test Workload
```bash
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-webapp
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
      - name: web
        image: nginx:alpine
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
EOF

# Wait for deployment
kubectl wait --for=condition=available deployment/test-webapp --timeout=60s
```

### Step 3: Start Controller with Mock Metrics
```bash
# In terminal 1 - start controller with mock data
make run ARGS="--use-mock-metrics=true"

# You should see logs like:
# INFO    setup    Using mock metrics client for testing
# INFO    setup    starting manager
```

### Step 4: Apply Test Configuration
```bash
# In terminal 2 - apply test configuration
kubectl apply -f - <<EOF
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: test-rightsizing
  namespace: default
spec:
  target:
    namespace: default
    labelSelector:
      matchLabels:
        app: test-webapp
  analysisWindow: "1h"
  dryRun: true
  thresholds:
    cpuUtilizationPercentile: 90
    memoryUtilizationPercentile: 90
    safetyMargin: 25
    minCpu: 50m
    minMemory: 64Mi
EOF
```

### Step 5: Monitor Progress
```bash
# Watch the PodRightSizing resource
kubectl get podrightsizing test-rightsizing -w

# Check detailed status
kubectl describe podrightsizing test-rightsizing

# View recommendations (once generated)
kubectl get podrightsizing test-rightsizing -o yaml | grep -A 50 recommendations
```

## Expected Success Output

When working correctly, you should see:

### 1. Controller Logs (Terminal 1):
```
INFO    controller-runtime.manager.controller.podrightsizing    Starting EventSource
INFO    controller-runtime.manager.controller.podrightsizing    Starting Controller
INFO    controller-runtime.manager.controller.podrightsizing    Starting reconciliation
INFO    controller.podrightsizing    Discovered target pods    {"count": 2}
INFO    controller.podrightsizing    Processing workload    {"workload": "default/Deployment/test-webapp", "pods": 2}
INFO    controller.podrightsizing    Generated recommendations    {"workload": "default/Deployment/test-webapp", "count": 2}
INFO    controller.podrightsizing    Reconciliation completed successfully    {"recommendations": 2}
```

### 2. Resource Status:
```bash
kubectl get podrightsizing test-rightsizing
# NAME               PHASE       TARGETED   UPDATED   LAST ANALYSIS   AGE
# test-rightsizing   Completed   2          0         1m              2m
```

### 3. Generated Recommendations:
```yaml
status:
  recommendations:
  - applied: false
    confidence: 95
    podReference:
      name: test-webapp-xxx
      namespace: default
      workloadName: test-webapp
      workloadType: Deployment
    currentResources:
      limits:
        cpu: 500m
        memory: 512Mi
      requests:
        cpu: 100m
        memory: 128Mi
    recommendedResources:
      limits:
        cpu: 75m
        memory: 100Mi
      requests:
        cpu: 60m
        memory: 90Mi
    reason: "Recommendations based on historical usage analysis. CPU: Based on 90th percentile of 12 data points. Memory: Based on 90th percentile of 12 data points. Applied 25% safety margin."
```

## Testing with Real Prometheus

### Step 1: Install Prometheus (if not available)
```bash
# Using Helm
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace \
  --set prometheus.prometheusSpec.retention=7d \
  --wait
```

### Step 2: Update Configuration
```bash
# Update your PodRightSizing to use real Prometheus
kubectl patch podrightsizing test-rightsizing --type='merge' -p='
spec:
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090"
'
```

### Step 3: Start Controller (without mock flag)
```bash
make run
```

### Step 4: Wait for Real Metrics
```bash
# This will take 5-10 minutes for sufficient metrics data
kubectl get podrightsizing test-rightsizing -w
```

## Troubleshooting

### Issue: No Recommendations Generated

**Check 1: Verify pods are running**
```bash
kubectl get pods -l app=test-webapp
# Should show 2 running pods
```

**Check 2: Controller can access metrics**
```bash
# For mock metrics - should work immediately
# For Prometheus - check connectivity:
kubectl run prometheus-test --image=curlimages/curl --rm -i --restart=Never -- \
  curl -v http://prometheus-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090/api/v1/query?query=up
```

**Check 3: Check controller logs**
```bash
# Look for errors in terminal running `make run`
# Common issues:
# - "insufficient data points" - need to wait longer or use mock
# - "failed to get workload metrics" - Prometheus connection issue
# - "no matching pods found" - label selector issue
```

### Issue: Controller Won't Start

**Check 1: CRDs installed**
```bash
kubectl get crd podrightsizings.rightsizing.k8s-rightsizer.io
```

**Check 2: Dependencies**
```bash
go mod tidy
make generate manifests
```

**Check 3: RBAC permissions**
```bash
make install  # Re-install CRDs and RBAC
```

### Issue: Prometheus Connection Failed

**Use metrics-server fallback:**
```bash
# Update your configuration to use metrics-server
kubectl patch podrightsizing test-rightsizing --type='merge' -p='
spec:
  metricsSource:
    type: metrics-server
'
```

**Or use mock for testing:**
```bash
make run ARGS="--use-mock-metrics=true"
```

## Production Testing Checklist

- [ ] ✅ Mock metrics test passes
- [ ] ✅ Real Prometheus integration works
- [ ] ✅ Multiple workload types supported
- [ ] ✅ Dry-run mode generates recommendations
- [ ] ✅ Gradual update strategy works (test carefully!)
- [ ] ✅ RBAC permissions are correct
- [ ] ✅ Performance acceptable with 50+ pods
- [ ] ✅ Controller survives restart
- [ ] ✅ Error handling works (invalid configs, missing metrics)

## Cleanup

```bash
# Remove test resources
kubectl delete podrightsizing test-rightsizing
kubectl delete deployment test-webapp
kubectl delete service test-webapp-service

# Remove CRDs (optional)
make uninstall
```

## Next Steps

Once testing is successful:

1. **Deploy to production cluster**:
   ```bash
   make docker-build docker-push deploy IMG=your-registry/pod-rightsizer:v1.0.0
   ```

2. **Create production configurations**:
   - Longer analysis windows (7-30 days)
   - Conservative safety margins (20-30%)
   - Scheduled analysis (weekly)
   - Manual update strategy initially

3. **Monitor and iterate**:
   - Start with dry-run mode
   - Validate recommendations manually
   - Gradually enable automatic updates
   - Monitor resource usage and costs