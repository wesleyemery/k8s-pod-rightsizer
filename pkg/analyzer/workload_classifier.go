package analyzer

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

// WorkloadClassifier classifies workloads based on usage patterns
type WorkloadClassifier struct {
	// Classification thresholds
	HighVariabilityThreshold       float64
	SpikeDetectionThreshold        float64
	MinDataPointsForClassification int
}

// NewWorkloadClassifier creates a new workload classifier.
func NewWorkloadClassifier() *WorkloadClassifier {
	return &WorkloadClassifier{
		HighVariabilityThreshold:       defaultHighVariabilityThreshold,
		SpikeDetectionThreshold:        defaultSpikeDetectionThreshold,
		MinDataPointsForClassification: defaultMinDataPointsForClassification,
	}
}

// WorkloadClass represents different types of workloads
type WorkloadClass string

const (
	WorkloadClassStable        WorkloadClass = "Stable"        // Low variability, predictable
	WorkloadClassBursty        WorkloadClass = "Bursty"        // High variability with spikes
	WorkloadClassPeriodic      WorkloadClass = "Periodic"      // Regular patterns
	WorkloadClassGrowing       WorkloadClass = "Growing"       // Increasing trend
	WorkloadClassShrinking     WorkloadClass = "Shrinking"     // Decreasing trend
	WorkloadClassUnpredictable WorkloadClass = "Unpredictable" // Chaotic patterns
)

// Trend direction constants
const (
	TrendDirectionIncreasing = "increasing"
	TrendDirectionDecreasing = "decreasing"
	TrendDirectionStable     = "stable"
)

// Classification constants
const (
	defaultHighVariabilityThreshold       = 0.3 // 30% coefficient of variation
	defaultSpikeDetectionThreshold        = 2.0 // 2 standard deviations
	defaultMinDataPointsForClassification = 20
)

// WorkloadClassification contains the classification results
type WorkloadClassification struct {
	WorkloadName    string
	WorkloadType    string
	Namespace       string
	Class           WorkloadClass
	Confidence      float64
	CPUPattern      ResourcePattern
	MemoryPattern   ResourcePattern
	Recommendations []ClassificationRecommendation
	AnalysisTime    time.Time
}

// ResourcePattern describes the usage pattern of a resource
type ResourcePattern struct {
	Mean                   float64
	StandardDeviation      float64
	CoefficientOfVariation float64
	TrendDirection         string  // "increasing", "decreasing", "stable"
	TrendStrength          float64 // 0-1, where 1 is strong trend
	SpikeFrequency         float64 // Percentage of time with spikes
	MinValue               float64
	MaxValue               float64
	P95Value               float64
}

// ClassificationRecommendation provides specific recommendations based on classification
type ClassificationRecommendation struct {
	Type        string
	Priority    string
	Description string
	Action      string
}

// ClassifyWorkload analyzes workload patterns and classifies the workload
func (w *WorkloadClassifier) ClassifyWorkload(workloadMetrics *metrics.WorkloadMetrics) (*WorkloadClassification, error) {
	if len(workloadMetrics.Pods) == 0 {
		return nil, fmt.Errorf("no pod metrics available for classification")
	}

	classification := &WorkloadClassification{
		WorkloadName: workloadMetrics.WorkloadName,
		WorkloadType: workloadMetrics.WorkloadType,
		Namespace:    workloadMetrics.Namespace,
		AnalysisTime: time.Now(),
	}

	// Analyze CPU patterns across all pods
	cpuPattern, err := w.analyzeResourcePattern(workloadMetrics, "CPU")
	if err != nil {
		return nil, fmt.Errorf("failed to analyze CPU pattern: %w", err)
	}
	classification.CPUPattern = *cpuPattern

	// Analyze Memory patterns across all pods
	memPattern, err := w.analyzeResourcePattern(workloadMetrics, "Memory")
	if err != nil {
		return nil, fmt.Errorf("failed to analyze memory pattern: %w", err)
	}
	classification.MemoryPattern = *memPattern

	// Classify based on patterns
	classification.Class = w.determineWorkloadClass(*cpuPattern, *memPattern)
	classification.Confidence = w.calculateClassificationConfidence(*cpuPattern, *memPattern)

	// Generate recommendations based on classification
	classification.Recommendations = w.generateClassificationRecommendations(classification)

	return classification, nil
}

// analyzeResourcePattern analyzes the pattern for a specific resource type
func (w *WorkloadClassifier) analyzeResourcePattern(workloadMetrics *metrics.WorkloadMetrics, resourceType string) (*ResourcePattern, error) {
	var allValues []float64

	// Collect all values across all pods
	for _, pod := range workloadMetrics.Pods {
		var history []metrics.ResourceUsage
		switch resourceType {
		case "CPU":
			history = pod.CPUUsageHistory
		case "Memory":
			history = pod.MemUsageHistory
		default:
			return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
		}

		for _, usage := range history {
			allValues = append(allValues, usage.Value)
		}
	}

	if len(allValues) < w.MinDataPointsForClassification {
		return nil, fmt.Errorf("insufficient data points for %s analysis: %d < %d",
			resourceType, len(allValues), w.MinDataPointsForClassification)
	}

	pattern := &ResourcePattern{}

	// Calculate basic statistics
	pattern.Mean = w.calculateMean(allValues)
	pattern.StandardDeviation = w.calculateStandardDeviation(allValues, pattern.Mean)

	if pattern.Mean > 0 {
		pattern.CoefficientOfVariation = pattern.StandardDeviation / pattern.Mean
	}

	pattern.MinValue = w.calculateMin(allValues)
	pattern.MaxValue = w.calculateMax(allValues)
	pattern.P95Value = w.calculatePercentile(allValues, 95)

	// Analyze trend
	trendDirection, trendStrength := w.analyzeTrend(allValues)
	pattern.TrendDirection = trendDirection
	pattern.TrendStrength = trendStrength

	// Calculate spike frequency
	pattern.SpikeFrequency = w.calculateSpikeFrequency(allValues, pattern.Mean, pattern.StandardDeviation)

	return pattern, nil
}

// determineWorkloadClass classifies the workload based on resource patterns
func (w *WorkloadClassifier) determineWorkloadClass(cpuPattern, memPattern ResourcePattern) WorkloadClass {
	// Check for growing/shrinking trends first
	if cpuPattern.TrendStrength > 0.7 || memPattern.TrendStrength > 0.7 {
		if cpuPattern.TrendDirection == TrendDirectionIncreasing || memPattern.TrendDirection == TrendDirectionIncreasing {
			return WorkloadClassGrowing
		}
		if cpuPattern.TrendDirection == TrendDirectionDecreasing || memPattern.TrendDirection == TrendDirectionDecreasing {
			return WorkloadClassShrinking
		}
	}

	// Check for high variability (bursty workloads)
	if cpuPattern.CoefficientOfVariation > w.HighVariabilityThreshold ||
		memPattern.CoefficientOfVariation > w.HighVariabilityThreshold {

		// If there are regular spikes, it might be periodic
		if cpuPattern.SpikeFrequency > 0.1 && cpuPattern.SpikeFrequency < 0.3 {
			return WorkloadClassPeriodic
		}

		// High variability with frequent spikes = bursty
		if cpuPattern.SpikeFrequency > 0.3 || memPattern.SpikeFrequency > 0.3 {
			return WorkloadClassBursty
		}

		// High variability without clear patterns = unpredictable
		return WorkloadClassUnpredictable
	}

	// Low variability = stable workload
	return WorkloadClassStable
}

// calculateClassificationConfidence calculates confidence in the classification
func (w *WorkloadClassifier) calculateClassificationConfidence(cpuPattern, memPattern ResourcePattern) float64 {
	confidence := 1.0

	// Reduce confidence if patterns are inconsistent
	cpuCV := cpuPattern.CoefficientOfVariation
	memCV := memPattern.CoefficientOfVariation

	// If CPU and memory show very different variability patterns, reduce confidence
	cvDifference := math.Abs(cpuCV - memCV)
	if cvDifference > 0.5 {
		confidence *= 0.7
	}

	// Reduce confidence if trend directions are opposite
	if cpuPattern.TrendDirection != memPattern.TrendDirection &&
		cpuPattern.TrendStrength > 0.5 && memPattern.TrendStrength > 0.5 {
		confidence *= 0.8
	}

	// Increase confidence for very clear patterns
	if cpuPattern.CoefficientOfVariation < 0.1 && memPattern.CoefficientOfVariation < 0.1 {
		confidence *= 1.2 // Very stable
	}

	if cpuPattern.CoefficientOfVariation > 0.8 && memPattern.CoefficientOfVariation > 0.8 {
		confidence *= 1.1 // Very bursty
	}

	// Ensure confidence stays within bounds
	return math.Min(math.Max(confidence, 0.1), 1.0)
}

// generateClassificationRecommendations generates recommendations based on workload classification
func (w *WorkloadClassifier) generateClassificationRecommendations(classification *WorkloadClassification) []ClassificationRecommendation {
	var recommendations []ClassificationRecommendation

	switch classification.Class {
	case WorkloadClassStable:
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Resource Optimization",
			Priority:    "Medium",
			Description: "Stable workload with predictable resource usage",
			Action:      "Right-size resources based on 95th percentile with small safety margin (10-15%)",
		})

	case WorkloadClassBursty:
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Scaling Strategy",
			Priority:    "High",
			Description: "Bursty workload with significant resource spikes",
			Action:      "Implement Horizontal Pod Autoscaler (HPA) and consider larger safety margins (25-30%)",
		})
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Resource Limits",
			Priority:    "High",
			Description: "Set appropriate resource limits to handle spikes",
			Action:      "Set limits based on 99th percentile to prevent resource starvation",
		})

	case WorkloadClassPeriodic:
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Predictive Scaling",
			Priority:    "High",
			Description: "Workload shows periodic usage patterns",
			Action:      "Consider implementing Vertical Pod Autoscaler (VPA) or scheduled scaling",
		})

	case WorkloadClassGrowing:
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Growth Planning",
			Priority:    "High",
			Description: "Workload shows increasing resource usage trend",
			Action:      "Monitor growth rate and plan for capacity increases. Consider implementing HPA.",
		})

	case WorkloadClassShrinking:
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Resource Reduction",
			Priority:    "Medium",
			Description: "Workload shows decreasing resource usage trend",
			Action:      "Gradually reduce resource allocations and monitor for performance impact",
		})

	case WorkloadClassUnpredictable:
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Monitoring",
			Priority:    "High",
			Description: "Unpredictable workload requires careful monitoring",
			Action:      "Implement robust monitoring and alerting. Use conservative resource limits.",
		})
	}

	// Add resource-specific recommendations
	if classification.CPUPattern.CoefficientOfVariation > 0.5 {
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "CPU Management",
			Priority:    "Medium",
			Description: "High CPU usage variability detected",
			Action:      "Consider CPU-based HPA or review application efficiency",
		})
	}

	if classification.MemoryPattern.CoefficientOfVariation > 0.3 {
		recommendations = append(recommendations, ClassificationRecommendation{
			Type:        "Memory Management",
			Priority:    "High",
			Description: "Variable memory usage may indicate memory leaks or inefficient allocation",
			Action:      "Review application memory management and consider memory-based monitoring",
		})
	}

	return recommendations
}

// Helper functions for statistical calculations

func (w *WorkloadClassifier) calculateMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func (w *WorkloadClassifier) calculateStandardDeviation(values []float64, mean float64) float64 {
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

func (w *WorkloadClassifier) calculateMin(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minVal := values[0]
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

func (w *WorkloadClassifier) calculateMax(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	maxVal := values[0]
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func (w *WorkloadClassifier) calculatePercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Create a copy and sort
	sorted := make([]float64, len(values))
	copy(sorted, values)

	// Simple bubble sort (for small datasets)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	index := (percentile / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sorted[lower]
	}

	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func (w *WorkloadClassifier) analyzeTrend(values []float64) (string, float64) {
	if len(values) < 10 {
		return TrendDirectionStable, 0.0
	}

	// Simple linear regression to detect trend
	n := float64(len(values))
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	// Calculate slope
	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)

	// Normalize slope by mean to get relative trend strength
	mean := sumY / n
	if mean == 0 {
		return TrendDirectionStable, 0.0
	}

	relativeSlope := math.Abs(slope) / mean

	// Determine direction and strength
	if math.Abs(slope) < mean*0.001 { // Very small trend
		return TrendDirectionStable, 0.0
	}

	var direction string
	if slope > 0 {
		direction = TrendDirectionIncreasing
	} else {
		direction = TrendDirectionDecreasing
	}

	// Cap strength at 1.0
	strength := math.Min(relativeSlope*100, 1.0)

	return direction, strength
}

func (w *WorkloadClassifier) calculateSpikeFrequency(values []float64, mean, stdDev float64) float64 {
	if len(values) == 0 || stdDev == 0 {
		return 0.0
	}

	spikeThreshold := mean + w.SpikeDetectionThreshold*stdDev
	spikes := 0

	for _, v := range values {
		if v > spikeThreshold {
			spikes++
		}
	}

	return float64(spikes) / float64(len(values))
}

// GetClassificationSummary provides a human-readable summary of the classification
func (classification *WorkloadClassification) GetClassificationSummary() string {
	var summary strings.Builder

	summary.WriteString(fmt.Sprintf("Workload: %s/%s (%s)\n",
		classification.Namespace, classification.WorkloadName, classification.WorkloadType))
	summary.WriteString(fmt.Sprintf("Classification: %s (Confidence: %.1f%%)\n",
		classification.Class, classification.Confidence*100))

	summary.WriteString(fmt.Sprintf("\nCPU Pattern:\n"))
	summary.WriteString(fmt.Sprintf("  - Variability: %.2f (CV)\n", classification.CPUPattern.CoefficientOfVariation))
	summary.WriteString(fmt.Sprintf("  - Trend: %s (%.1f%%)\n",
		classification.CPUPattern.TrendDirection, classification.CPUPattern.TrendStrength*100))
	summary.WriteString(fmt.Sprintf("  - Spike Frequency: %.1f%%\n", classification.CPUPattern.SpikeFrequency*100))

	summary.WriteString(fmt.Sprintf("\nMemory Pattern:\n"))
	summary.WriteString(fmt.Sprintf("  - Variability: %.2f (CV)\n", classification.MemoryPattern.CoefficientOfVariation))
	summary.WriteString(fmt.Sprintf("  - Trend: %s (%.1f%%)\n",
		classification.MemoryPattern.TrendDirection, classification.MemoryPattern.TrendStrength*100))
	summary.WriteString(fmt.Sprintf("  - Spike Frequency: %.1f%%\n", classification.MemoryPattern.SpikeFrequency*100))

	if len(classification.Recommendations) > 0 {
		summary.WriteString(fmt.Sprintf("\nRecommendations:\n"))
		for _, rec := range classification.Recommendations {
			summary.WriteString(fmt.Sprintf("  - %s (%s): %s\n", rec.Type, rec.Priority, rec.Description))
		}
	}

	return summary.String()
}
