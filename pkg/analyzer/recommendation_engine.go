// pkg/analyzer/recommendation_engine.go
package analyzer

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

// RecommendationEngine generates resource recommendations based on historical usage
type RecommendationEngine struct {
	// Configuration options
	DefaultSafetyMargin        int     // Default safety margin percentage
	DefaultConfidenceThreshold int     // Minimum confidence level
	MinDataPoints              int     // Minimum data points required for recommendations
	CPURequestMultiplier       float64 // Multiplier for CPU requests vs limits
	MemoryRequestMultiplier    float64 // Multiplier for memory requests vs limits
}

// NewRecommendationEngine creates a new recommendation engine with default settings
func NewRecommendationEngine() *RecommendationEngine {
	return &RecommendationEngine{
		DefaultSafetyMargin:        20,  // 20% safety margin
		DefaultConfidenceThreshold: 70,  // 70% confidence threshold
		MinDataPoints:              10,  // Minimum 10 data points
		CPURequestMultiplier:       0.8, // Requests = 80% of limits
		MemoryRequestMultiplier:    0.9, // Requests = 90% of limits
	}
}

// GenerateRecommendations generates resource recommendations for a workload
func (r *RecommendationEngine) GenerateRecommendations(
	ctx context.Context,
	workloadMetrics *metrics.WorkloadMetrics,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) ([]rightsizingv1alpha1.PodRecommendation, error) {
	logger := log.FromContext(ctx)

	if len(workloadMetrics.Pods) == 0 {
		return nil, fmt.Errorf("no pod metrics provided")
	}

	var recommendations []rightsizingv1alpha1.PodRecommendation

	logger.Info("Generating recommendations for workload",
		"workload", workloadMetrics.WorkloadName,
		"podCount", len(workloadMetrics.Pods))

	// Generate recommendations for each pod in the workload
	for _, podMetrics := range workloadMetrics.Pods {
		recommendation, err := r.generatePodRecommendation(ctx, podMetrics, thresholds)
		if err != nil {
			logger.Error(err, "Failed to generate recommendation for pod",
				"podName", podMetrics.PodName,
				"namespace", podMetrics.Namespace)
			continue
		}

		if recommendation != nil {
			recommendations = append(recommendations, *recommendation)
		}
	}

	logger.Info("Generated recommendations",
		"workload", workloadMetrics.WorkloadName,
		"recommendationCount", len(recommendations))

	return recommendations, nil
}

// generatePodRecommendation generates a recommendation for a single pod
func (r *RecommendationEngine) generatePodRecommendation(
	ctx context.Context,
	podMetrics metrics.PodMetrics,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) (*rightsizingv1alpha1.PodRecommendation, error) {
	logger := log.FromContext(ctx).WithValues("pod", podMetrics.PodName)

	logger.V(1).Info("Analyzing pod metrics",
		"cpuDataPoints", len(podMetrics.CPUUsageHistory),
		"memoryDataPoints", len(podMetrics.MemUsageHistory))

	// Analyze CPU usage
	cpuRecommendation, cpuConfidence, err := r.analyzeCPUUsage(podMetrics.CPUUsageHistory, thresholds)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze CPU usage: %w", err)
	}

	// Analyze Memory usage
	memoryRecommendation, memoryConfidence, err := r.analyzeMemoryUsage(podMetrics.MemUsageHistory, thresholds)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze memory usage: %w", err)
	}

	// Calculate overall confidence (minimum of CPU and memory confidence)
	overallConfidence := int(math.Min(float64(cpuConfidence), float64(memoryConfidence)))

	logger.V(1).Info("Calculated confidence scores",
		"cpuConfidence", cpuConfidence,
		"memoryConfidence", memoryConfidence,
		"overallConfidence", overallConfidence)

	// Skip recommendation if confidence is too low
	if overallConfidence < r.DefaultConfidenceThreshold {
		logger.Info("Skipping recommendation due to low confidence",
			"confidence", overallConfidence,
			"threshold", r.DefaultConfidenceThreshold)
		return nil, nil
	}

	logger.Info("Generating recommendation with sufficient confidence",
		"confidence", overallConfidence,
		"threshold", r.DefaultConfidenceThreshold)

	// Build recommended resource requirements
	recommendedResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// Set CPU recommendations
	if cpuRecommendation.Limit != nil {
		recommendedResources.Limits[corev1.ResourceCPU] = *cpuRecommendation.Limit

		// Calculate request as percentage of limit
		limitValue := cpuRecommendation.Limit.AsApproximateFloat64()
		requestValue := limitValue * r.CPURequestMultiplier
		recommendedResources.Requests[corev1.ResourceCPU] = *resource.NewMilliQuantity(
			int64(requestValue*1000), resource.DecimalSI)
	}

	// Set Memory recommendations
	if memoryRecommendation.Limit != nil {
		recommendedResources.Limits[corev1.ResourceMemory] = *memoryRecommendation.Limit

		// Calculate request as percentage of limit
		limitValue := memoryRecommendation.Limit.AsApproximateFloat64()
		requestValue := limitValue * r.MemoryRequestMultiplier
		recommendedResources.Requests[corev1.ResourceMemory] = *resource.NewQuantity(
			int64(requestValue), resource.BinarySI)
	}

	// Create the recommendation
	recommendation := &rightsizingv1alpha1.PodRecommendation{
		PodReference: rightsizingv1alpha1.PodReference{
			Name:      podMetrics.PodName,
			Namespace: podMetrics.Namespace,
			// WorkloadType and WorkloadName would be filled by the controller
		},
		RecommendedResources: recommendedResources,
		Confidence:           overallConfidence,
		Reason:               r.buildReasonString(cpuRecommendation, memoryRecommendation, thresholds),
		Applied:              false,
	}

	// Calculate potential savings (placeholder - actual current resources would come from controller)
	placeholderCurrent := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI), // 100m
			corev1.ResourceMemory: *resource.NewQuantity(134217728, resource.BinarySI), // 128Mi
		},
	}
	costCalculator := NewCostCalculator()
	recommendation.PotentialSavings = costCalculator.CalculateSavings(placeholderCurrent, recommendedResources)

	return recommendation, nil
}

// ResourceRecommendation represents a recommendation for a single resource type
type ResourceRecommendation struct {
	Request    *resource.Quantity
	Limit      *resource.Quantity
	Percentile float64
	Confidence int
	DataPoints int
	Reason     string
}

// analyzeCPUUsage analyzes CPU usage history and generates recommendations
func (r *RecommendationEngine) analyzeCPUUsage(
	cpuHistory []metrics.ResourceUsage,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) (*ResourceRecommendation, int, error) {

	if len(cpuHistory) < r.MinDataPoints {
		return nil, 0, fmt.Errorf("insufficient CPU data points: %d < %d", len(cpuHistory), r.MinDataPoints)
	}

	// Extract values and sort them
	values := make([]float64, len(cpuHistory))
	for i, usage := range cpuHistory {
		values[i] = usage.Value
	}
	sort.Float64s(values)

	// Get the target percentile (default to 95th percentile)
	percentile := 95
	if thresholds.CPUUtilizationPercentile > 0 {
		percentile = thresholds.CPUUtilizationPercentile
	}

	// Calculate percentile value
	percentileValue := r.calculatePercentile(values, float64(percentile))

	// Apply safety margin
	safetyMargin := r.DefaultSafetyMargin
	if thresholds.SafetyMargin > 0 {
		safetyMargin = thresholds.SafetyMargin
	}

	recommendedLimit := percentileValue * (1.0 + float64(safetyMargin)/100.0)

	// Apply min/max constraints
	if !thresholds.MinCPU.IsZero() {
		minCPU := thresholds.MinCPU.AsApproximateFloat64()
		if recommendedLimit < minCPU {
			recommendedLimit = minCPU
		}
	}

	if !thresholds.MaxCPU.IsZero() {
		maxCPU := thresholds.MaxCPU.AsApproximateFloat64()
		if recommendedLimit > maxCPU {
			recommendedLimit = maxCPU
		}
	}

	// Calculate confidence based on data consistency
	confidence := r.calculateConfidence(values)

	// Convert to Kubernetes resource format
	limitQuantity := resource.NewMilliQuantity(int64(recommendedLimit*1000), resource.DecimalSI)

	recommendation := &ResourceRecommendation{
		Limit:      limitQuantity,
		Percentile: percentileValue,
		Confidence: confidence,
		DataPoints: len(cpuHistory),
		Reason:     fmt.Sprintf("Based on %dth percentile of %d data points", percentile, len(cpuHistory)),
	}

	return recommendation, confidence, nil
}

// analyzeMemoryUsage analyzes memory usage history and generates recommendations
func (r *RecommendationEngine) analyzeMemoryUsage(
	memoryHistory []metrics.ResourceUsage,
	thresholds rightsizingv1alpha1.ResourceThresholds,
) (*ResourceRecommendation, int, error) {

	if len(memoryHistory) < r.MinDataPoints {
		return nil, 0, fmt.Errorf("insufficient memory data points: %d < %d", len(memoryHistory), r.MinDataPoints)
	}

	// Extract values and sort them
	values := make([]float64, len(memoryHistory))
	for i, usage := range memoryHistory {
		values[i] = usage.Value
	}
	sort.Float64s(values)

	// Get the target percentile (default to 95th percentile)
	percentile := 95
	if thresholds.MemoryUtilizationPercentile > 0 {
		percentile = thresholds.MemoryUtilizationPercentile
	}

	// Calculate percentile value
	percentileValue := r.calculatePercentile(values, float64(percentile))

	// Apply safety margin
	safetyMargin := r.DefaultSafetyMargin
	if thresholds.SafetyMargin > 0 {
		safetyMargin = thresholds.SafetyMargin
	}

	recommendedLimit := percentileValue * (1.0 + float64(safetyMargin)/100.0)

	// Apply min/max constraints
	if !thresholds.MinMemory.IsZero() {
		minMemory := thresholds.MinMemory.AsApproximateFloat64()
		if recommendedLimit < minMemory {
			recommendedLimit = minMemory
		}
	}

	if !thresholds.MaxMemory.IsZero() {
		maxMemory := thresholds.MaxMemory.AsApproximateFloat64()
		if recommendedLimit > maxMemory {
			recommendedLimit = maxMemory
		}
	}

	// Calculate confidence based on data consistency
	confidence := r.calculateConfidence(values)

	// Convert to Kubernetes resource format
	limitQuantity := resource.NewQuantity(int64(recommendedLimit), resource.BinarySI)

	recommendation := &ResourceRecommendation{
		Limit:      limitQuantity,
		Percentile: percentileValue,
		Confidence: confidence,
		DataPoints: len(memoryHistory),
		Reason:     fmt.Sprintf("Based on %dth percentile of %d data points", percentile, len(memoryHistory)),
	}

	return recommendation, confidence, nil
}

// calculatePercentile calculates the percentile value from sorted data
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

	// Calculate index
	index := (percentile / 100.0) * float64(len(sortedValues)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sortedValues[lower]
	}

	// Linear interpolation between lower and upper values
	weight := index - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}

// calculateConfidence calculates confidence level based on data variance
func (r *RecommendationEngine) calculateConfidence(values []float64) int {
	if len(values) < 2 {
		return 50 // Low confidence with insufficient data
	}

	// Calculate mean and standard deviation
	mean := r.calculateMean(values)
	stdDev := r.calculateStandardDeviation(values, mean)

	// Calculate coefficient of variation (CV)
	cv := 0.0
	if mean > 0 {
		cv = stdDev / mean
	}

	// Convert CV to confidence score
	// Lower CV (more stable data) = higher confidence
	// CV < 0.1 (very stable) = 95-100% confidence
	// CV 0.1-0.3 (moderate) = 70-95% confidence
	// CV 0.3-0.5 (variable) = 50-70% confidence
	// CV > 0.5 (highly variable) = 30-50% confidence

	confidence := 100
	if cv > 0.5 {
		confidence = 30 + int(20*(1-math.Min(cv-0.5, 0.5)/0.5))
	} else if cv > 0.3 {
		confidence = 70 + int(25*(1-(cv-0.3)/0.2))
	} else if cv > 0.1 {
		confidence = 95 + int(5*(1-(cv-0.1)/0.2))
	}

	// Boost confidence if we have more data points
	dataPointBoost := math.Min(float64(len(values))/100.0, 0.1) // Up to 10% boost for 100+ points
	confidence = int(math.Min(float64(confidence)*(1+dataPointBoost), 100))

	return confidence
}

// calculateMean calculates the arithmetic mean of values
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

// calculateStandardDeviation calculates the standard deviation
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

// buildReasonString creates a human-readable reason for the recommendation
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

// AdvancedAnalyzer provides more sophisticated analysis methods
type AdvancedAnalyzer struct {
	*RecommendationEngine
}

// NewAdvancedAnalyzer creates an analyzer with advanced features
func NewAdvancedAnalyzer() *AdvancedAnalyzer {
	return &AdvancedAnalyzer{
		RecommendationEngine: NewRecommendationEngine(),
	}
}

// AnalyzeWorkloadPatterns analyzes usage patterns across the entire workload
func (a *AdvancedAnalyzer) AnalyzeWorkloadPatterns(
	workloadMetrics *metrics.WorkloadMetrics,
) (*WorkloadAnalysis, error) {

	if len(workloadMetrics.Pods) == 0 {
		return nil, fmt.Errorf("no pod metrics provided")
	}

	analysis := &WorkloadAnalysis{
		WorkloadName:   workloadMetrics.WorkloadName,
		WorkloadType:   workloadMetrics.WorkloadType,
		Namespace:      workloadMetrics.Namespace,
		AnalysisTime:   time.Now(),
		TotalPods:      len(workloadMetrics.Pods),
		AnalysisWindow: workloadMetrics.EndTime.Sub(workloadMetrics.StartTime),
	}

	// Analyze CPU patterns
	cpuAnalysis, err := a.analyzeCPUPatterns(workloadMetrics.Pods)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze CPU patterns: %w", err)
	}
	analysis.CPUAnalysis = cpuAnalysis

	// Analyze Memory patterns
	memAnalysis, err := a.analyzeMemoryPatterns(workloadMetrics.Pods)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze memory patterns: %w", err)
	}
	analysis.MemoryAnalysis = memAnalysis

	// Detect usage patterns
	analysis.UsagePatterns = a.detectUsagePatterns(workloadMetrics.Pods)

	// Generate workload-level recommendations
	analysis.Recommendations = a.generateWorkloadRecommendations(analysis)

	return analysis, nil
}

// analyzeResourcePatterns is a generic function to analyze resource patterns (CPU or Memory)
func (a *AdvancedAnalyzer) analyzeResourcePatterns(
	pods []metrics.PodMetrics,
	resourceType, unit string,
	getUsageHistory func(metrics.PodMetrics) []metrics.ResourceUsage,
) (*ResourceAnalysis, error) {
	var allValues []float64
	podAnalyses := make([]PodResourceAnalysis, 0, len(pods))

	for _, pod := range pods {
		usageHistory := getUsageHistory(pod)
		if len(usageHistory) == 0 {
			continue
		}

		podValues := make([]float64, len(usageHistory))
		for i, usage := range usageHistory {
			podValues[i] = usage.Value
			allValues = append(allValues, usage.Value)
		}

		sort.Float64s(podValues)

		podAnalysis := PodResourceAnalysis{
			PodName:     pod.PodName,
			Min:         podValues[0],
			Max:         podValues[len(podValues)-1],
			Mean:        a.calculateMean(podValues),
			P50:         a.calculatePercentile(podValues, 50),
			P95:         a.calculatePercentile(podValues, 95),
			P99:         a.calculatePercentile(podValues, 99),
			StandardDev: a.calculateStandardDeviation(podValues, a.calculateMean(podValues)),
			DataPoints:  len(podValues),
		}

		podAnalyses = append(podAnalyses, podAnalysis)
	}

	if len(allValues) == 0 {
		return nil, fmt.Errorf("no %s data available", resourceType)
	}

	sort.Float64s(allValues)

	return &ResourceAnalysis{
		ResourceType:    resourceType,
		Unit:            unit,
		TotalDataPoints: len(allValues),
		WorkloadMin:     allValues[0],
		WorkloadMax:     allValues[len(allValues)-1],
		WorkloadMean:    a.calculateMean(allValues),
		WorkloadP50:     a.calculatePercentile(allValues, 50),
		WorkloadP95:     a.calculatePercentile(allValues, 95),
		WorkloadP99:     a.calculatePercentile(allValues, 99),
		WorkloadStdDev:  a.calculateStandardDeviation(allValues, a.calculateMean(allValues)),
		PodAnalyses:     podAnalyses,
	}, nil
}

// analyzeCPUPatterns analyzes CPU usage patterns across all pods
func (a *AdvancedAnalyzer) analyzeCPUPatterns(pods []metrics.PodMetrics) (*ResourceAnalysis, error) {
	return a.analyzeResourcePatterns(pods, "CPU", "cores", func(pod metrics.PodMetrics) []metrics.ResourceUsage {
		return pod.CPUUsageHistory
	})
}

// analyzeMemoryPatterns analyzes memory usage patterns across all pods
func (a *AdvancedAnalyzer) analyzeMemoryPatterns(pods []metrics.PodMetrics) (*ResourceAnalysis, error) {
	return a.analyzeResourcePatterns(pods, "Memory", "bytes", func(pod metrics.PodMetrics) []metrics.ResourceUsage {
		return pod.MemUsageHistory
	})
}

// detectUsagePatterns detects common usage patterns in the workload
func (a *AdvancedAnalyzer) detectUsagePatterns(pods []metrics.PodMetrics) []UsagePattern {
	var patterns []UsagePattern

	// Detect if there are clear daily/weekly patterns
	// This is a simplified implementation - production would use more sophisticated time series analysis

	for _, pod := range pods {
		// Analyze CPU patterns
		if len(pod.CPUUsageHistory) > 24 { // Need at least 24 data points
			pattern := a.analyzeTimeSeries(pod.CPUUsageHistory, "CPU")
			if pattern != nil {
				pattern.PodName = pod.PodName
				patterns = append(patterns, *pattern)
			}
		}

		// Analyze Memory patterns
		if len(pod.MemUsageHistory) > 24 {
			pattern := a.analyzeTimeSeries(pod.MemUsageHistory, "Memory")
			if pattern != nil {
				pattern.PodName = pod.PodName
				patterns = append(patterns, *pattern)
			}
		}
	}

	return patterns
}

// analyzeTimeSeries performs basic time series analysis to detect patterns
func (a *AdvancedAnalyzer) analyzeTimeSeries(usage []metrics.ResourceUsage, resourceType string) *UsagePattern {
	if len(usage) < 24 {
		return nil
	}

	// Simple pattern detection - look for cyclical behavior
	// In production, you'd use more sophisticated algorithms like FFT, autocorrelation, etc.

	values := make([]float64, len(usage))
	for i, u := range usage {
		values[i] = u.Value
	}

	mean := a.calculateMean(values)
	stdDev := a.calculateStandardDeviation(values, mean)

	// Detect if usage varies significantly (indicating a pattern)
	cv := stdDev / mean

	patternType := "steady"
	if cv > 0.3 {
		patternType = "variable"
	} else if cv > 0.1 {
		patternType = "moderate"
	}

	// Detect if there are obvious spikes
	spikes := 0
	for _, v := range values {
		if v > mean+2*stdDev {
			spikes++
		}
	}

	spikePattern := "none"
	if spikes > len(values)/10 {
		spikePattern = "frequent"
	} else if spikes > 0 {
		spikePattern = "occasional"
	}

	return &UsagePattern{
		ResourceType: resourceType,
		PatternType:  patternType,
		SpikePattern: spikePattern,
		Confidence:   a.calculateConfidence(values),
		Description:  fmt.Sprintf("%s usage shows %s pattern with %s spikes", resourceType, patternType, spikePattern),
	}
}

// generateWorkloadRecommendations generates recommendations at the workload level
func (a *AdvancedAnalyzer) generateWorkloadRecommendations(analysis *WorkloadAnalysis) []WorkloadRecommendation {
	var recommendations []WorkloadRecommendation

	// CPU recommendations
	if analysis.CPUAnalysis != nil {
		rec := WorkloadRecommendation{
			Type: "CPU Optimization",
			Description: fmt.Sprintf(
				"Based on analysis of %d pods with %d total data points, "+
					"recommend setting CPU limits to %.3f cores (95th percentile: %.3f + 20%% safety margin)",
				analysis.TotalPods,
				analysis.CPUAnalysis.TotalDataPoints,
				analysis.CPUAnalysis.WorkloadP95*1.2,
				analysis.CPUAnalysis.WorkloadP95,
			),
			Priority: a.calculateRecommendationPriority(analysis.CPUAnalysis),
			Impact:   "Medium",
		}
		recommendations = append(recommendations, rec)
	}

	// Memory recommendations
	if analysis.MemoryAnalysis != nil {
		rec := WorkloadRecommendation{
			Type: "Memory Optimization",
			Description: fmt.Sprintf(
				"Based on analysis of %d pods with %d total data points, "+
					"recommend setting Memory limits to %.0f MB (95th percentile: %.0f + 20%% safety margin)",
				analysis.TotalPods,
				analysis.MemoryAnalysis.TotalDataPoints,
				analysis.MemoryAnalysis.WorkloadP95*1.2/1024/1024,
				analysis.MemoryAnalysis.WorkloadP95/1024/1024,
			),
			Priority: a.calculateRecommendationPriority(analysis.MemoryAnalysis),
			Impact:   "High", // Memory is typically more critical
		}
		recommendations = append(recommendations, rec)
	}

	// Pattern-based recommendations
	for _, pattern := range analysis.UsagePatterns {
		if pattern.PatternType == "variable" {
			rec := WorkloadRecommendation{
				Type: "Scaling Strategy",
				Description: fmt.Sprintf(
					"Pod %s shows variable %s usage patterns. "+
						"Consider implementing Horizontal Pod Autoscaling (HPA) or Vertical Pod Autoscaling (VPA)",
					pattern.PodName,
					pattern.ResourceType,
				),
				Priority: "Medium",
				Impact:   "High",
			}
			recommendations = append(recommendations, rec)
		}
	}

	return recommendations
}

// calculateRecommendationPriority calculates priority based on resource analysis
func (a *AdvancedAnalyzer) calculateRecommendationPriority(analysis *ResourceAnalysis) string {
	cv := analysis.WorkloadStdDev / analysis.WorkloadMean

	if cv > 0.5 {
		return "High" // High variability = high priority
	} else if cv > 0.2 {
		return "Medium"
	}
	return "Low"
}

// Data structures for advanced analysis
type WorkloadAnalysis struct {
	WorkloadName    string
	WorkloadType    string
	Namespace       string
	AnalysisTime    time.Time
	AnalysisWindow  time.Duration
	TotalPods       int
	CPUAnalysis     *ResourceAnalysis
	MemoryAnalysis  *ResourceAnalysis
	UsagePatterns   []UsagePattern
	Recommendations []WorkloadRecommendation
}

type ResourceAnalysis struct {
	ResourceType    string
	Unit            string
	TotalDataPoints int
	WorkloadMin     float64
	WorkloadMax     float64
	WorkloadMean    float64
	WorkloadP50     float64
	WorkloadP95     float64
	WorkloadP99     float64
	WorkloadStdDev  float64
	PodAnalyses     []PodResourceAnalysis
}

type PodResourceAnalysis struct {
	PodName     string
	Min         float64
	Max         float64
	Mean        float64
	P50         float64
	P95         float64
	P99         float64
	StandardDev float64
	DataPoints  int
}

type UsagePattern struct {
	PodName      string
	ResourceType string
	PatternType  string
	SpikePattern string
	Confidence   int
	Description  string
}

type WorkloadRecommendation struct {
	Type        string
	Description string
	Priority    string
	Impact      string
}
