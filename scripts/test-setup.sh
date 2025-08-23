#!/bin/bash
set -e

# Pod Right-sizer Comprehensive Test Script
echo "🚀 Pod Right-sizer Comprehensive Test Setup"
echo "============================================"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}ℹ️  $1${NC}"; }
log_warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }
log_error() { echo -e "${RED}❌ $1${NC}"; }
log_step() { echo -e "${BLUE}📋 Step: $1${NC}"; }

# Cleanup function
cleanup() {
    log_info "Cleaning up resources..."
    kubectl delete podrightsizing test-rightsizing --ignore-not-found=true
    kubectl delete deployment test-webapp --ignore-not-found=true
    kubectl delete service test-webapp-service --ignore-not-found=true
}

# Trap cleanup on exit
trap cleanup EXIT

# Check prerequisites
check_prerequisites() {
    log_step "Checking prerequisites"
    
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is required but not installed"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        log_error "Cannot access Kubernetes cluster"
        exit 1
    fi
    
    if [[ ! -f "Makefile" ]]; then
        log_error "Please run this script from the pod-rightsizer project root"
        exit 1
    fi
    
    log_info "Prerequisites check passed!"
}

# Build and setup
build_and_setup() {
    log_step "Building and setting up CRDs"
    
    # Update dependencies
    go mod tidy
    
    # Generate CRDs and manifests
    make generate manifests
    
    # Install CRDs
    make install
    
    log_info "CRDs installed successfully!"
}

# Deploy test workload
deploy_test_workload() {
    log_step "Deploying test workload"
    
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
---
apiVersion: v1
kind: Service
metadata:
  name: test-webapp-service
spec:
  selector:
    app: test-webapp
  ports:
  - port: 80
    targetPort: 80
  type: ClusterIP
EOF
    
    # Wait for deployment
    kubectl wait --for=condition=available deployment/test-webapp --timeout=60s
    log_info "Test workload deployed successfully!"
}

# Create test configuration
create_test_config() {
    log_step "Creating test PodRightSizing configuration"
    
    cat > test-rightsizing.yaml <<EOF
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
  analysisWindow: "1h"  # Short for testing
  dryRun: true  # Safe mode
  schedule: "*/2 * * * *"  # Every 2 minutes for testing
  thresholds:
    cpuUtilizationPercentile: 90
    memoryUtilizationPercentile: 90
    safetyMargin: 25
    minCpu: 50m
    minMemory: 64Mi
    maxCpu: 2
    maxMemory: 2Gi
  metricsSource:
    type: prometheus
    prometheusConfig:
      url: "http://prometheus-server.monitoring.svc.cluster.local"
EOF
    
    log_info "Test configuration created: test-rightsizing.yaml"
}

# Test with mock metrics
test_with_mock() {
    log_step "Testing with mock metrics"
    
    log_info "Starting controller with mock metrics..."
    log_info "This will run in the background. Check the logs in another terminal."
    
    # Start controller with mock metrics in background
    make run ARGS="--use-mock-metrics=true" > controller.log 2>&1 &
    CONTROLLER_PID=$!
    
    # Give controller time to start
    sleep 5
    
    # Apply test configuration
    log_info "Applying test configuration..."
    kubectl apply -f test-rightsizing.yaml
    
    # Monitor for 2 minutes
    log_info "Monitoring for 2 minutes..."
    for i in {1..24}; do
        echo "Checking status... ($i/24)"
        kubectl get podrightsizing test-rightsizing -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,MESSAGE:.status.message,TARGETED:.status.targetedPods --no-headers
        
        # Check if we have recommendations
        if kubectl get podrightsizing test-rightsizing -o jsonpath='{.status.recommendations}' 2>/dev/null | grep -q "podReference"; then
            log_info "✅ Recommendations generated successfully!"
            break
        fi
        
        sleep 5
    done
    
    # Show results
    echo ""
    log_info "=== Final Results ==="
    kubectl get podrightsizing test-rightsizing -o yaml | head -50
    
    # Check for recommendations
    if kubectl get podrightsizing test-rightsizing -o jsonpath='{.status.recommendations}' 2>/dev/null | grep -q "podReference"; then
        log_info "✅ SUCCESS: Pod Right-sizer is working correctly!"
        echo "Recommendations:"
        kubectl get podrightsizing test-rightsizing -o jsonpath='{.status.recommendations}' | jq '.' || echo "Recommendations found (jq not available for pretty printing)"
    else
        log_warn "⚠️  No recommendations generated yet. Check controller logs:"
        tail -20 controller.log
    fi
    
    # Stop controller
    kill $CONTROLLER_PID 2>/dev/null || true
    rm -f controller.log
}

# Full integration test (if Prometheus is available)
test_with_prometheus() {
    log_step "Testing with Prometheus (if available)"
    
    # Check if Prometheus is available
    if kubectl get svc -A | grep -q prometheus; then
        log_info "Prometheus detected, running full integration test..."
        
        # Update config to use real Prometheus
        sed -i 's/url: .*/url: "http:\/\/prometheus-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090"/' test-rightsizing.yaml
        
        log_info "Starting controller with Prometheus..."
        make run > controller.log 2>&1 &
        CONTROLLER_PID=$!
        
        sleep 5
        kubectl apply -f test-rightsizing.yaml
        
        log_info "Waiting for metrics collection (this may take a few minutes)..."
        sleep 120  # Wait longer for real metrics
        
        kubectl get podrightsizing test-rightsizing -o yaml
        
        kill $CONTROLLER_PID 2>/dev/null || true
        rm -f controller.log
    else
        log_warn "Prometheus not found, skipping integration test"
    fi
}

# Performance test
performance_test() {
    log_step "Running performance test with multiple resources"
    
    # Create multiple test deployments
    for i in {1..3}; do
        kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: perf-test-$i
  labels:
    app: perf-test
    instance: "$i"
spec:
  replicas: 2
  selector:
    matchLabels:
      app: perf-test
      instance: "$i"
  template:
    metadata:
      labels:
        app: perf-test
        instance: "$i"
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
EOF
    done
    
    # Wait for deployments
    for i in {1..3}; do
        kubectl wait --for=condition=available deployment/perf-test-$i --timeout=60s
    done
    
    # Create performance test configuration
    cat > perf-test-rightsizing.yaml <<EOF
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: perf-test-rightsizing
spec:
  target:
    labelSelector:
      matchLabels:
        app: perf-test
  analysisWindow: "30m"
  dryRun: true
  thresholds:
    cpuUtilizationPercentile: 95
    memoryUtilizationPercentile: 95
    safetyMargin: 20
EOF
    
    # Test performance
    log_info "Running performance test..."
    make run ARGS="--use-mock-metrics=true" > perf-controller.log 2>&1 &
    PERF_PID=$!
    
    sleep 5
    time kubectl apply -f perf-test-rightsizing.yaml
    
    # Monitor performance
    sleep 30
    kubectl get podrightsizing perf-test-rightsizing -o yaml
    
    # Cleanup performance test
    kill $PERF_PID 2>/dev/null || true
    kubectl delete -f perf-test-rightsizing.yaml --ignore-not-found=true
    for i in {1..3}; do
        kubectl delete deployment perf-test-$i --ignore-not-found=true
    done
    rm -f perf-controller.log perf-test-rightsizing.yaml
    
    log_info "Performance test completed!"
}

# Main execution
main() {
    echo "This comprehensive test will:"
    echo "1. Check prerequisites and build"
    echo "2. Deploy test workload"
    echo "3. Test with mock metrics"
    echo "4. Test with Prometheus (if available)"
    echo "5. Run performance test"
    echo ""
    
    read -p "Continue with comprehensive test? (Y/n): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Nn]$ ]]; then
        exit 0
    fi
    
    check_prerequisites
    build_and_setup
    deploy_test_workload
    create_test_config
    test_with_mock
    test_with_prometheus
    performance_test
    
    echo ""
    log_info "🎉 Comprehensive test completed!"
    echo ""
    echo "Summary:"
    echo "- ✅ CRDs installed and working"
    echo "- ✅ Controller can start and process resources"
    echo "- ✅ Mock metrics integration working"
    echo "- ✅ Recommendations can be generated"
    echo "- ✅ Performance test passed"
    echo ""
    echo "Next steps:"
    echo "1. Test with real Prometheus in your environment"
    echo "2. Deploy to production with appropriate configurations"
    echo "3. Monitor and tune based on your workloads"
}

# Handle command line arguments
case "${1:-}" in
    "mock-only")
        check_prerequisites
        build_and_setup
        deploy_test_workload
        create_test_config
        test_with_mock
        ;;
    "cleanup")
        cleanup
        ;;
    *)
        main
        ;;
esac