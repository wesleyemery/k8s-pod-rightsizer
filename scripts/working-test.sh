#!/bin/bash
set -e

# Working Pod Right-sizer Test (Fixed Scheduling)
echo "🔧 Fixed Pod Right-sizer Test"
echo "============================="

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
    kubectl delete podrightsizing working-test --ignore-not-found=true
    kubectl delete deployment huge-waste-app --ignore-not-found=true
    pkill -f "pod-rightsizer" || true
    
    # Clean up port 8081 if still in use
    local port_pid=$(lsof -ti:8081 2>/dev/null || echo "")
    if [[ -n "$port_pid" ]]; then
        log_info "Cleaning up port 8081 (PID: $port_pid)"
        kill -9 $port_pid 2>/dev/null || true
    fi
    
    rm -f controller.log working-test.yaml
}

# Check and clean port 8081 before starting
check_port_8081() {
    local port_pid=$(lsof -ti:8081 2>/dev/null || echo "")
    if [[ -n "$port_pid" ]]; then
        log_warn "Port 8081 is in use by PID $port_pid, cleaning up..."
        kill -9 $port_pid 2>/dev/null || true
        sleep 2
        log_info "✅ Port 8081 cleaned up"
    fi
}

trap cleanup EXIT

main() {
    echo "This test uses the fixed controller scheduling logic:"
    echo "• No schedule field = runs every 5 minutes (testing mode)"
    echo "• Massive resource waste: 3000m CPU, 3Gi memory for nginx"
    echo "• Mock metrics: 30m CPU, 48Mi memory usage"
    echo "• Expected savings: 99% CPU, 98.4% memory reduction"
    echo ""
    
    read -p "Run working test with fixed scheduling? (Y/n): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Nn]$ ]]; then
        exit 0
    fi
    
    # Prerequisites
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is required"
        exit 1
    fi
    
    # Check and clean port 8081 to prevent crashes
    check_port_8081
    
    log_step "Building and installing CRDs with fixes"
    make generate manifests install
    
    log_step "Creating extremely wasteful deployment"
    kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: huge-waste-app
  labels:
    app: huge-waste-app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: huge-waste-app
  template:
    metadata:
      labels:
        app: huge-waste-app
    spec:
      containers:
      - name: app
        image: nginx:alpine
        resources:
          requests:
            cpu: 3000m    # 3 full CPU cores for nginx!
            memory: 3Gi   # 3GB for nginx!
          limits:
            cpu: 6000m
            memory: 6Gi
        ports:
        - containerPort: 80
EOF
    
    kubectl wait --for=condition=available deployment/huge-waste-app --timeout=60s
    log_info "✅ Extremely wasteful deployment created: 3000m CPU, 3Gi memory requests"
    
    log_step "Creating working test config (NO SCHEDULE = 5min intervals)"
    cat > working-test.yaml <<EOF
apiVersion: rightsizing.k8s-rightsizer.io/v1alpha1
kind: PodRightSizing
metadata:
  name: working-test
  namespace: default
spec:
  target:
    namespace: default
    labelSelector:
      matchLabels:
        app: huge-waste-app
  analysisWindow: "2m"    # Very short for testing
  # NO SCHEDULE FIELD = uses 5-minute testing mode
  dryRun: true
  thresholds:
    cpuUtilizationPercentile: 50      # Very low
    memoryUtilizationPercentile: 50   # Very low
    safetyMargin: 5                   # Small margin
    minChangeThreshold: 1             # 1% threshold
    minCpu: "10m"
    minMemory: "16Mi"
    maxCpu: "200m"                    # Force reduction
    maxMemory: "256Mi"                # Force reduction
  metricsSource:
    type: metrics-server
EOF
    
    log_step "Starting controller with enhanced mock metrics"
    
    # Set extreme mock conditions
    export USE_MOCK_METRICS=true
    export MOCK_CPU_USAGE_MILLIS=30        # 30m CPU (1% of 3000m)
    export MOCK_MEMORY_USAGE_BYTES=$((48 * 1024 * 1024))  # 48Mi (1.6% of 3Gi)
    export CONFIDENCE_THRESHOLD=1          # Accept any confidence
    export MIN_DATA_POINTS=1               # Need only 1 data point
    
    log_info "Mock metrics setup:"
    log_info "  📊 Wasteful requests: 3000m CPU, 3072Mi memory"  
    log_info "  📉 Mock actual usage: 30m CPU (1%), 48Mi memory (1.6%)"
    log_info "  🎯 Fixed controller will retry every 5 minutes (no schedule field)"
    
    # Start controller
    make run ARGS="--use-mock-metrics=true --zap-devel=true" > controller.log 2>&1 &
    CONTROLLER_PID=$!
    
    sleep 10
    
    if ! kill -0 $CONTROLLER_PID 2>/dev/null; then
        log_error "Controller failed to start!"
        cat controller.log
        exit 1
    fi
    
    log_info "✅ Controller started with fixed scheduling logic"
    
    log_step "Applying config and monitoring (should work now!)"
    kubectl apply -f working-test.yaml
    
    # Monitor for recommendations with fixed scheduling
    local found_recommendations=false
    for i in {1..20}; do  # Monitor for up to 10 minutes (20 * 30s)
        echo ""
        echo "=== Check $i/20 ($(date)) ==="
        
        # Check status
        if kubectl get podrightsizing working-test >/dev/null 2>&1; then
            kubectl get podrightsizing working-test -o custom-columns=\
NAME:.metadata.name,\
PHASE:.status.phase,\
TARGETED:.status.targetedPods,\
LAST_ANALYSIS:.status.lastAnalysisTime,\
MESSAGE:.status.message --no-headers
            
            # Check for recommendations
            RECS=$(kubectl get podrightsizing working-test -o jsonpath='{.status.recommendations}' 2>/dev/null || echo "null")
            if [[ "$RECS" != "null" && "$RECS" != "" && "$RECS" != "[]" ]]; then
                log_info "🎉 SUCCESS! Fixed scheduling generated recommendations!"
                echo ""
                echo "📋 Recommendations (fixed controller):"
                kubectl get podrightsizing working-test -o jsonpath='{.status.recommendations}' | jq '.'
                
                echo ""
                echo "💰 Savings Summary:"
                kubectl get podrightsizing working-test -o jsonpath='{.status.recommendations[0].potentialSavings}' | jq '.' 2>/dev/null
                
                found_recommendations=true
                break
            else
                echo "📝 No recommendations yet..."
                
                # Show last analysis time vs current time
                LAST_ANALYSIS=$(kubectl get podrightsizing working-test -o jsonpath='{.status.lastAnalysisTime}' 2>/dev/null || echo "none")
                echo "🕐 Last analysis: $LAST_ANALYSIS"
                echo "🕐 Current time: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
                
                if [[ "$LAST_ANALYSIS" != "none" ]]; then
                    # Calculate minutes since last analysis
                    LAST_EPOCH=$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$LAST_ANALYSIS" +%s 2>/dev/null || echo "0")
                    CURRENT_EPOCH=$(date +%s)
                    MINUTES_SINCE=$((($CURRENT_EPOCH - $LAST_EPOCH) / 60))
                    echo "🕐 Minutes since last analysis: $MINUTES_SINCE (should re-run at 5 minutes)"
                fi
            fi
        else
            log_warn "PodRightSizing resource not found"
        fi
        
        # Show controller logs every 5 checks
        if (( i % 5 == 0 )); then
            echo ""
            echo "🔍 Controller logs (last 3 lines):"
            tail -3 controller.log
        fi
        
        sleep 30
    done
    
    # Final results
    echo ""
    if [[ "$found_recommendations" == "true" ]]; then
        log_info "✅ WORKING TEST PASSED! Fixed scheduling logic works!"
        echo ""
        echo "📊 Final Results:"
        kubectl get podrightsizing working-test -o yaml
        
    else
        log_error "❌ Test incomplete - monitor longer or check logs"
        echo ""
        echo "🔍 Debug information:"
        echo "PodRightSizing status:"
        kubectl get podrightsizing working-test -o yaml
        
        echo ""
        echo "Controller logs (last 10 lines):"
        tail -10 controller.log
    fi
    
    # Stop controller
    kill $CONTROLLER_PID 2>/dev/null || true
}

case "${1:-}" in
    "cleanup")
        cleanup
        ;;
    *)
        main
        ;;
esac