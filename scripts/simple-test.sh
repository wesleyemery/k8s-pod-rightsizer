#!/bin/bash
set -e

echo "🚀 Pod Right-sizer Simple Test"
echo "=============================="

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info() { echo -e "${GREEN}✅ $1${NC}"; }
log_warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }
log_error() { echo -e "${RED}❌ $1${NC}"; }

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    kubectl delete podrightsizing test-rightsizing --ignore-not-found=true 2>/dev/null || true
    kubectl delete deployment test-webapp --ignore-not-found=true 2>/dev/null || true
    kubectl delete service test-webapp-service --ignore-not-found=true 2>/dev/null || true
}

trap cleanup EXIT

echo "Step 1: Updating dependencies..."
go mod tidy

echo "Step 2: Generating and installing CRDs..."
make generate manifests

# Check if generate succeeded
if [ $? -ne 0 ]; then
    log_error "Failed to generate CRDs. Please check the errors above."
    exit 1
fi

make install

echo "Step 3: Deploying test workload..."
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
        ports:
        - containerPort: 80
EOF

kubectl wait --for=condition=available deployment/test-webapp --timeout=60s

echo "Step 4: Creating test configuration..."
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
    minCPU: 50m
    minMemory: 64Mi
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-server.monitoring.svc.cluster.local"
EOF

echo "Step 5: Test completed!"
echo ""
log_info "Resources deployed successfully!"
echo ""
echo "Now run the controller manually:"
echo "  make run ARGS=\"--use-mock-metrics=true\""
echo ""
echo "Then monitor progress:"
echo "  kubectl get podrightsizing test-rightsizing -w"
echo ""
echo "View detailed status:"
echo "  kubectl describe podrightsizing test-rightsizing"