package analyzer

import (
	"context"
	"fmt"
	"math"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

// MetricsClientInterface defines the interface for metrics clients
type MetricsClientInterface interface {
	GetPodMetrics(ctx context.Context, namespace, podName string, window time.Duration) (*metrics.PodMetrics, error)
	GetWorkloadMetrics(ctx context.Context, namespace, workloadName, workloadType string, window time.Duration) (*metrics.WorkloadMetrics, error)
}

// RecommendationEngine generates resource recommendations based on historical usage
type RecommendationEngine struct {
	DefaultSafetyMargin        int
	DefaultConfidenceThreshold int
	MinDataPoints              int
	CPURequestMultiplier       float64
	MemoryRequestMultiplier    float64
}

// NewRecommendationEngine creates a new recommendation engine with default settings
func NewRecommendationEngine() *RecommendationEngine {
	return &RecommendationEngine{
		DefaultSafetyMargin:        20,
		DefaultConfidenceThreshold: 70,
		MinDataPoints:              10,
		CPURequestMultiplier:       0.8,
		MemoryRequestMultiplier:    0.9,
	}
}

// GenerateRecommendations generates resource recommendations for a workload
func (r *RecommendationEngine) GenerateRecommendations(
	ctx context.Context,
	workloadMetrics *metrics.WorkloadMetrics,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) ([]rightsizingv1alpha1.PodRecommendation, error) {

	if len(workloadMetrics.Pods) == 0 {
		return nil, fmt.Errorf("no pod metrics provided")
	}

	var recommendations []rightsizingv1alpha1.PodRecommendation

	for _, podMetrics := range workloadMetrics.Pods {
		recommendation, err := r.generatePodRecommendation(podMetrics, thresholds)
		if err != nil {
			continue
		}

		if recommendation != nil {
			recommendations = append(recommendations, *recommendation)
		}
	}

	return recommendations, nil
}

func (r *RecommendationEngine) generatePodRecommendation(
	podMetrics metrics.PodMetrics,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) (*rightsizingv1alpha1.PodRecommendation, error) {

	// Analyze CPU usage
	cpuRec, cpuConfidence, err := r.analyzeCPUUsage(podMetrics.CPUUsageHistory, thresholds)
	if err != nil {
		return nil, err
	}

	// Analyze Memory usage
	memoryRec, memoryConfidence, err := r.analyzeMemoryUsage(podMetrics.MemUsageHistory, thresholds)
	if err != nil {
		return nil, err
	}

	// Calculate overall confidence
	overallConfidence := int(math.Min(float64(cpuConfidence), float64(memoryConfidence)))

	if overallConfidence < r.DefaultConfidenceThreshold {
		return nil, nil
	}

	// Build recommended resources
	recommendedResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// Set CPU recommendations
	if cpuRec.Limit != nil {
		recommendedResources.Limits[corev1.ResourceCPU] = *cpuRec.Limit

		limitValue := cpuRec.Limit.AsApproximateFloat64()
		requestValue := limitValue * r.CPURequestMultiplier
		recommendedResources.Requests[corev1.ResourceCPU] = *resource.NewMilliQuantity(
			int64(requestValue*1000), resource.DecimalSI)
	}

	// Set Memory recommendations
	if memoryRec.Limit != nil {
		recommendedResources.Limits[corev1.ResourceMemory] = *memoryRec.Limit

		limitValue := memoryRec.Limit.AsApproximateFloat64()
		requestValue := limitValue * r.MemoryRequestMultiplier
		recommendedResources.Requests[corev1.ResourceMemory] = *resource.NewQuantity(
			int64(requestValue), resource.BinarySI)
	}

	recommendation := &rightsizingv1alpha1.PodRecommendation{
		PodReference: rightsizingv1alpha1.PodReference{
			Name:      podMetrics.PodName,
			Namespace: podMetrics.Namespace,
		},
		RecommendedResources: recommendedResources,
		Confidence:           overallConfidence,
		Reason:               r.buildReasonString(cpuRec, memoryRec, thresholds),
		Applied:              false,
	}

	return recommendation, nil
}

type ResourceRecommendation struct {
	Request    *resource.Quantity
	Limit      *resource.Quantity
	Percentile float64
	Confidence int
	DataPoints int
	Reason     string
}

func (r *RecommendationEngine) analyzeCPUUsage(
	cpuHistory []metrics.ResourceUsage,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) (*ResourceRecommendation, int, error) {

	if len(cpuHistory) < r.MinDataPoints {
		return nil, 0, fmt.Errorf("insufficient CPU data points: %d < %d", len(cpuHistory), r.MinDataPoints)
	}

	values := make([]float64, len(cpuHistory))
	for i, usage := range cpuHistory {
		values[i] = usage.Value
	}
	sort.Float64s(values)

	percentile := 95
	if thresholds.CPUUtilizationPercentile > 0 {
		percentile = thresholds.CPUUtilizationPercentile
	}

	percentileValue := r.calculatePercentile(values, float64(percentile))

	safetyMargin := r.DefaultSafetyMargin
	if thresholds.SafetyMargin > 0 {
		safetyMargin = thresholds.SafetyMargin
	}

	recommendedLimit := percentileValue * (1.0 + float64(safetyMargin)/100.0)

	// Apply min/max constraints
	if thresholds.MinCPU != nil {
		minCPU := thresholds.MinCPU.AsApproximateFloat64()
		if recommendedLimit < minCPU {
			recommendedLimit = minCPU
		}
	}

	if thresholds.MaxCPU != nil {
		maxCPU := thresholds.MaxCPU.AsApproximateFloat64()
		if recommendedLimit > maxCPU {
			recommendedLimit = maxCPU
		}
	}

	confidence := r.calculateConfidence(values, percentileValue)
	limitQuantity := resource.NewMilliQuantity(int64(recommendedLimit*1000), resource.DecimalSI)

	return &ResourceRecommendation{
		Limit:      limitQuantity,
		Percentile: percentileValue,
		Confidence: confidence,
		DataPoints: len(cpuHistory),
		Reason:     fmt.Sprintf("Based on %dth percentile of %d data points", percentile, len(cpuHistory)),
	}, confidence, nil
}

func (r *RecommendationEngine) analyzeMemoryUsage(
	memoryHistory []metrics.ResourceUsage,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) (*ResourceRecommendation, int, error) {

	if len(memoryHistory) < r.MinDataPoints {
		return nil, 0, fmt.Errorf("insufficient memory data points: %d < %d", len(memoryHistory), r.MinDataPoints)
	}

	values := make([]float64, len(memoryHistory))
	for i, usage := range memoryHistory {
		values[i] = usage.Value
	}
	sort.Float64s(values)

	percentile := 95
	if thresholds.MemoryUtilizationPercentile > 0 {
		percentile = thresholds.MemoryUtilizationPercentile
	}

	percentileValue := r.calculatePercentile(values, float64(percentile))

	safetyMargin := r.DefaultSafetyMargin
	if thresholds.SafetyMargin > 0 {
		safetyMargin = thresholds.SafetyMargin
	}

	recommendedLimit := percentileValue * (1.0 + float64(safetyMargin)/100.0)

	if thresholds.MinMemory != nil {
		minMemory := thresholds.MinMemory.AsApproximateFloat64()
		if recommendedLimit < minMemory {
			recommendedLimit = minMemory
		}
	}

	if thresholds.MaxMemory != nil {
		maxMemory := thresholds.MaxMemory.AsApproximateFloat64()
		if recommendedLimit > maxMemory {
			recommendedLimit = maxMemory
		}
	}

	confidence := r.calculateConfidence(values, percentileValue)
	limitQuantity := resource.NewQuantity(int64(recommendedLimit), resource.BinarySI)

	return &ResourceRecommendation{
		Limit:      limitQuantity,
		Percentile: percentileValue,
		Confidence: confidence,
		DataPoints: len(memoryHistory),
		Reason:     fmt.Sprintf("Based on %dth percentile of %d data points", percentile, len(memoryHistory)),
	}, confidence, nil
}

func (r *RecommendationEngine) calculatePercentile(sortedValues []float64, percentile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}

	if percentile <= 0 {
		return sortedValues[0]
	}
	if percentile >= 100 {
		return sortedValues[len(sortedValues)-1]
	}

	index := (percentile / 100.0) * float64(len(sortedValues)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sortedValues[lower]
	}

	weight := index - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}

func (r *RecommendationEngine) calculateConfidence(values []float64, percentileValue float64) int {
	if len(values) < 2 {
		return 50
	}

	mean := r.calculateMean(values)
	stdDev := r.calculateStandardDeviation(values, mean)

	cv := 0.0
	if mean > 0 {
		cv = stdDev / mean
	}

	confidence := 100
	if cv > 0.5 {
		confidence = 30 + int(20*(1-math.Min(cv-0.5, 0.5)/0.5))
	} else if cv > 0.3 {
		confidence = 70 + int(25*(1-(cv-0.3)/0.2))
	} else if cv > 0.1 {
		confidence = 95 + int(5*(1-(cv-0.1)/0.2))
	}

	dataPointBoost := math.Min(float64(len(values))/100.0, 0.1)
	confidence = int(math.Min(float64(confidence)*(1+dataPointBoost), 100))

	return confidence
}

func (r *RecommendationEngine) calculateMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func (r *RecommendationEngine) calculateStandardDeviation(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}

	sumSquaredDiff := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquaredDiff += diff * diff
	}

	variance := sumSquaredDiff / float64(len(values)-1)
	return math.Sqrt(variance)
}

func (r *RecommendationEngine) buildReasonString(
	cpuRec *ResourceRecommendation,
	memRec *ResourceRecommendation,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) string {
	var reasons []string

	if cpuRec != nil {
		reasons = append(reasons, fmt.Sprintf("CPU: %s", cpuRec.Reason))
	}

	if memRec != nil {
		reasons = append(reasons, fmt.Sprintf("Memory: %s", memRec.Reason))
	}

	safetyMargin := r.DefaultSafetyMargin
	if thresholds.SafetyMargin > 0 {
		safetyMargin = thresholds.SafetyMargin
	}

	reasonStr := "Recommendations based on historical usage analysis. "
	if len(reasons) > 0 {
		reasonStr += fmt.Sprintf("%s. ", reasons[0])
		if len(reasons) > 1 {
			reasonStr += fmt.Sprintf("%s. ", reasons[1])
		}
	}
	reasonStr += fmt.Sprintf("Applied %d%% safety margin.", safetyMargin)

	return reasonStr
}
