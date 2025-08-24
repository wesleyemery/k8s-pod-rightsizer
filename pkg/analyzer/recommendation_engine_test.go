package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

func TestNewRecommendationEngine(t *testing.T) {
	engine := NewRecommendationEngine()

	assert.NotNil(t, engine)
	assert.Equal(t, 20, engine.DefaultSafetyMargin)
	assert.Equal(t, 70, engine.DefaultConfidenceThreshold)
	assert.Equal(t, 10, engine.MinDataPoints)
	assert.Equal(t, 0.8, engine.CPURequestMultiplier)
	assert.Equal(t, 0.9, engine.MemoryRequestMultiplier)
}

func TestGenerateRecommendations_NoPodMetrics(t *testing.T) {
	engine := NewRecommendationEngine()
	ctx := context.Background()

	workloadMetrics := &metrics.WorkloadMetrics{
		Pods: []metrics.PodMetrics{},
	}

	thresholds := rightsizingv1alpha1.ResourceThresholds{}

	recommendations, err := engine.GenerateRecommendations(ctx, workloadMetrics, thresholds)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pod metrics provided")
	assert.Nil(t, recommendations)
}

func TestGenerateRecommendations_ValidInput(t *testing.T) {
	engine := NewRecommendationEngine()
	ctx := context.Background()

	// Create sufficient sample usage history (more than MinDataPoints)
	cpuHistory := make([]metrics.ResourceUsage, 15)
	memoryHistory := make([]metrics.ResourceUsage, 15)

	for i := 0; i < 15; i++ {
		cpuHistory[i] = metrics.ResourceUsage{
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
			Value:     0.1 + float64(i%3)*0.05, // Varying CPU usage
			Unit:      "cores",
		}
		memoryHistory[i] = metrics.ResourceUsage{
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
			Value:     float64(100+i*5) * 1024 * 1024, // Varying memory usage
			Unit:      "bytes",
		}
	}

	workloadMetrics := &metrics.WorkloadMetrics{
		Pods: []metrics.PodMetrics{
			{
				PodName:         "test-pod-1",
				Namespace:       "default",
				CPUUsageHistory: cpuHistory,
				MemUsageHistory: memoryHistory,
				StartTime:       time.Now().Add(-20 * time.Minute),
				EndTime:         time.Now(),
			},
		},
	}

	thresholds := rightsizingv1alpha1.ResourceThresholds{
		MinCPU:    resource.MustParse("10m"),
		MaxCPU:    resource.MustParse("2"),
		MinMemory: resource.MustParse("50Mi"),
		MaxMemory: resource.MustParse("2Gi"),
	}

	recommendations, err := engine.GenerateRecommendations(ctx, workloadMetrics, thresholds)

	assert.NoError(t, err)
	assert.NotNil(t, recommendations)
	assert.Len(t, recommendations, 1)

	rec := recommendations[0]
	assert.Equal(t, "test-pod-1", rec.PodReference.Name)
	assert.Equal(t, "default", rec.PodReference.Namespace)

	// Verify recommendation structure using ResourceRequirements
	assert.NotNil(t, rec.RecommendedResources.Limits[corev1.ResourceCPU])
	assert.NotNil(t, rec.RecommendedResources.Limits[corev1.ResourceMemory])
	assert.NotNil(t, rec.RecommendedResources.Requests[corev1.ResourceCPU])
	assert.NotNil(t, rec.RecommendedResources.Requests[corev1.ResourceMemory])

	assert.Greater(t, rec.Confidence, 0)
	assert.LessOrEqual(t, rec.Confidence, 100)
}

func TestAnalyzeCPUUsage(t *testing.T) {
	engine := NewRecommendationEngine()

	// Create usage data with sufficient data points
	usage := make([]metrics.ResourceUsage, 15)
	for i := 0; i < 15; i++ {
		usage[i] = metrics.ResourceUsage{
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
			Value:     0.1 + float64(i%5)*0.02, // Varying between 0.1 and 0.18
			Unit:      "cores",
		}
	}

	thresholds := rightsizingv1alpha1.ResourceThresholds{
		MinCPU: resource.MustParse("50m"),
		MaxCPU: resource.MustParse("1"),
	}

	recommendation, confidence, err := engine.analyzeCPUUsage(usage, thresholds)

	assert.NoError(t, err)
	assert.NotNil(t, recommendation)
	assert.NotNil(t, recommendation.Limit)
	assert.Greater(t, confidence, 0)
	assert.LessOrEqual(t, confidence, 100)

	// Verify that the limit is within bounds
	limitValue := recommendation.Limit.AsApproximateFloat64()
	minCPU := thresholds.MinCPU.AsApproximateFloat64()
	maxCPU := thresholds.MaxCPU.AsApproximateFloat64()
	assert.GreaterOrEqual(t, limitValue, minCPU)
	assert.LessOrEqual(t, limitValue, maxCPU)
}

func TestAnalyzeMemoryUsage(t *testing.T) {
	engine := NewRecommendationEngine()

	// Create usage data with sufficient data points
	usage := make([]metrics.ResourceUsage, 15)
	for i := 0; i < 15; i++ {
		usage[i] = metrics.ResourceUsage{
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
			Value:     float64(100+i*5) * 1024 * 1024, // Varying memory usage
			Unit:      "bytes",
		}
	}

	thresholds := rightsizingv1alpha1.ResourceThresholds{
		MinMemory: resource.MustParse("50Mi"),
		MaxMemory: resource.MustParse("2Gi"),
	}

	recommendation, confidence, err := engine.analyzeMemoryUsage(usage, thresholds)

	assert.NoError(t, err)
	assert.NotNil(t, recommendation)
	assert.NotNil(t, recommendation.Limit)
	assert.Greater(t, confidence, 0)
	assert.LessOrEqual(t, confidence, 100)

	// Verify that the limit is within bounds
	limitValue := recommendation.Limit.Value()
	minMemory := thresholds.MinMemory.Value()
	maxMemory := thresholds.MaxMemory.Value()
	assert.GreaterOrEqual(t, limitValue, minMemory)
	assert.LessOrEqual(t, limitValue, maxMemory)
}

func TestAnalyzeCPUUsage_InsufficientData(t *testing.T) {
	engine := NewRecommendationEngine()

	// Create insufficient data (less than MinDataPoints)
	usage := make([]metrics.ResourceUsage, 5)
	for i := 0; i < 5; i++ {
		usage[i] = metrics.ResourceUsage{
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
			Value:     0.1,
			Unit:      "cores",
		}
	}

	thresholds := rightsizingv1alpha1.ResourceThresholds{}

	recommendation, confidence, err := engine.analyzeCPUUsage(usage, thresholds)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient CPU data points")
	assert.Nil(t, recommendation)
	assert.Equal(t, 0, confidence)
}

func TestAnalyzeMemoryUsage_InsufficientData(t *testing.T) {
	engine := NewRecommendationEngine()

	// Create insufficient data (less than MinDataPoints)
	usage := make([]metrics.ResourceUsage, 5)
	for i := 0; i < 5; i++ {
		usage[i] = metrics.ResourceUsage{
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
			Value:     100 * 1024 * 1024,
			Unit:      "bytes",
		}
	}

	thresholds := rightsizingv1alpha1.ResourceThresholds{}

	recommendation, confidence, err := engine.analyzeMemoryUsage(usage, thresholds)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient memory data points")
	assert.Nil(t, recommendation)
	assert.Equal(t, 0, confidence)
}

func TestCalculatePercentile(t *testing.T) {
	engine := NewRecommendationEngine()

	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0}

	// Test 50th percentile (median)
	p50 := engine.calculatePercentile(values, 50)
	assert.InDelta(t, 5.5, p50, 0.01)

	// Test 90th percentile
	p90 := engine.calculatePercentile(values, 90)
	assert.InDelta(t, 9.1, p90, 0.01)

	// Test 95th percentile
	p95 := engine.calculatePercentile(values, 95)
	assert.InDelta(t, 9.55, p95, 0.01)
}

func TestCalculateConfidence(t *testing.T) {
	engine := NewRecommendationEngine()

	// Test with consistent values (low variance, high confidence)
	consistentValues := []float64{0.5, 0.51, 0.49, 0.52, 0.48, 0.50, 0.51, 0.49, 0.50, 0.51}
	highConfidence := engine.calculateConfidence(consistentValues)
	assert.Greater(t, highConfidence, 70)

	// Test with highly variable values (high variance, low confidence)
	variableValues := []float64{0.1, 0.9, 0.2, 0.8, 0.3, 0.7, 0.4, 0.6, 0.5, 1.0}
	lowConfidence := engine.calculateConfidence(variableValues)
	assert.Less(t, lowConfidence, 70)

	// Test with minimal data points
	minimalValues := []float64{0.5, 0.6, 0.4}
	minimalConfidence := engine.calculateConfidence(minimalValues)
	assert.GreaterOrEqual(t, minimalConfidence, 0)
	assert.LessOrEqual(t, minimalConfidence, 100)
}
