package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/analyzer"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

// TestCompleteWorkflowIntegration tests the complete right-sizing workflow
func TestCompleteWorkflowIntegration(t *testing.T) {
	tests := []struct {
		name           string
		workloadType   string
		podCount       int
		analysisWindow time.Duration
		expectedRecs   int
		mockVariance   float64
		expectSavings  bool
	}{
		{
			name:           "StableDeployment",
			workloadType:   "Deployment",
			podCount:       3,
			analysisWindow: 24 * time.Hour,
			expectedRecs:   3,
			mockVariance:   0.1, // Low variance = stable
			expectSavings:  true,
		},
		// {
		// 	name:           "BurstyStatefulSet",
		// 	workloadType:   "StatefulSet",
		// 	podCount:       2,
		// 	analysisWindow: 12 * time.Hour,
		// 	expectedRecs:   2,
		// 	mockVariance:   0.5, // High variance = bursty
		// 	expectSavings:  false,
		// },
		// {
		// 	name:           "SinglePodWorkload",
		// 	workloadType:   "Deployment",
		// 	podCount:       1,
		// 	analysisWindow: 6 * time.Hour,
		// 	expectedRecs:   1,
		// 	mockVariance:   0.2,
		// 	expectSavings:  true,
		// },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Setup mock metrics client with test-specific variance
			mockClient := &metrics.MockMetricsClient{
				BaseCPU:    0.05,
				BaseMemory: 67108864,
				Variance:   tt.mockVariance,
			}

			// Setup recommendation engine
			engine := analyzer.NewRecommendationEngine()

			// Create workload metrics
			workloadMetrics, err := mockClient.GetWorkloadMetrics(
				ctx, "test-namespace", "test-workload", tt.workloadType, tt.analysisWindow)
			require.NoError(t, err)
			require.NotNil(t, workloadMetrics)
			assert.Len(t, workloadMetrics.Pods, tt.podCount)

			// Setup thresholds
			thresholds := rightsizingv1alpha1.ResourceThresholds{
				CPUUtilizationPercentile:    95,
				MemoryUtilizationPercentile: 95,
				SafetyMargin:                20,
				MinCPU:                      *resource.NewMilliQuantity(10, resource.DecimalSI),
				MinMemory:                   *resource.NewQuantity(33554432, resource.BinarySI), // 32Mi
			}

			// Generate recommendations
			recommendations, err := engine.GenerateRecommendations(ctx, workloadMetrics, thresholds)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecs)

			// Validate each recommendation
			for _, rec := range recommendations {
				// Basic validation
				assert.NotEmpty(t, rec.PodReference.Name)
				assert.Equal(t, "test-namespace", rec.PodReference.Namespace)
				assert.Greater(t, rec.Confidence, 0)
				assert.NotEmpty(t, rec.Reason)

				// Resource validation
				cpuLimit := rec.RecommendedResources.Limits[corev1.ResourceCPU]
				memLimit := rec.RecommendedResources.Limits[corev1.ResourceMemory]
				cpuRequest := rec.RecommendedResources.Requests[corev1.ResourceCPU]
				memRequest := rec.RecommendedResources.Requests[corev1.ResourceMemory]

				assert.True(t, !cpuLimit.IsZero(), "CPU limit should be set")
				assert.True(t, !memLimit.IsZero(), "Memory limit should be set")
				assert.True(t, !cpuRequest.IsZero(), "CPU request should be set")
				assert.True(t, !memRequest.IsZero(), "Memory request should be set")

				// Requests should be less than or equal to limits
				assert.True(t, cpuRequest.Cmp(cpuLimit) <= 0, "CPU request should not exceed limit")
				assert.True(t, memRequest.Cmp(memLimit) <= 0, "Memory request should not exceed limit")

				// Check minimum constraints
				assert.True(t, cpuRequest.Cmp(thresholds.MinCPU) >= 0, "CPU request should meet minimum")
				assert.True(t, memRequest.Cmp(thresholds.MinMemory) >= 0, "Memory request should meet minimum")

				// Validate savings calculation
				if tt.expectSavings {
					assert.NotNil(t, rec.PotentialSavings.CPUSavings, "Should have CPU savings")
					assert.NotNil(t, rec.PotentialSavings.MemorySavings, "Should have memory savings")
					assert.NotEmpty(t, rec.PotentialSavings.CostSavings, "Should have cost savings estimate")
				}
			}

			// Test workload classification
			classifier := analyzer.NewWorkloadClassifier()
			classification, err := classifier.ClassifyWorkload(workloadMetrics)
			require.NoError(t, err)
			assert.NotNil(t, classification)
			assert.NotEmpty(t, string(classification.Class))
			assert.Greater(t, classification.Confidence, 0.0)
			assert.LessOrEqual(t, classification.Confidence, 1.0)

			// Validate classification matches expected variance
			if tt.mockVariance < 0.2 {
				assert.Contains(t, []analyzer.WorkloadClass{
					analyzer.WorkloadClassStable,
					analyzer.WorkloadClassPeriodic,
				}, classification.Class, "Low variance should result in stable/periodic classification")
			} else if tt.mockVariance > 0.4 {
				assert.Contains(t, []analyzer.WorkloadClass{
					analyzer.WorkloadClassBursty,
					analyzer.WorkloadClassUnpredictable,
				}, classification.Class, "High variance should result in bursty/unpredictable classification")
			}

			// Validate recommendations from classification
			assert.NotEmpty(t, classification.Recommendations)
			for _, classRec := range classification.Recommendations {
				assert.NotEmpty(t, classRec.Type)
				assert.NotEmpty(t, classRec.Priority)
				assert.NotEmpty(t, classRec.Description)
				assert.NotEmpty(t, classRec.Action)
			}
		})
	}
}

// TestCostCalculationIntegration tests cost calculation functionality
func TestCostCalculationIntegration(t *testing.T) {
	costCalc := analyzer.NewCostCalculator()

	current := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(200, resource.DecimalSI), // 200m
			corev1.ResourceMemory: *resource.NewQuantity(268435456, resource.BinarySI), // 256Mi
		},
	}

	recommended := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI), // 100m
			corev1.ResourceMemory: *resource.NewQuantity(134217728, resource.BinarySI), // 128Mi
		},
	}

	savings := costCalc.CalculateSavings(current, recommended)

	// Should have savings since recommended is less than current
	assert.NotNil(t, savings.CPUSavings, "Should have CPU savings")
	assert.NotNil(t, savings.MemorySavings, "Should have memory savings")
	assert.NotEmpty(t, savings.CostSavings, "Should have cost estimate")

	// Verify savings amounts
	assert.Equal(t, int64(100), savings.CPUSavings.MilliValue(), "Should save 100m CPU")
	assert.Equal(t, int64(134217728), savings.MemorySavings.Value(), "Should save 128Mi memory")
}

// TestAdvancedAnalyzerIntegration tests the advanced analyzer features
func TestAdvancedAnalyzerIntegration(t *testing.T) {
	ctx := context.Background()

	// Create mock data with known patterns
	mockClient := &metrics.MockMetricsClient{
		BaseCPU:    0.1,
		BaseMemory: 134217728,
		Variance:   0.3,
	}

	workloadMetrics, err := mockClient.GetWorkloadMetrics(
		ctx, "test-ns", "advanced-workload", "Deployment", 24*time.Hour)
	require.NoError(t, err)

	// Test advanced analyzer
	advancedAnalyzer := analyzer.NewAdvancedAnalyzer()
	analysis, err := advancedAnalyzer.AnalyzeWorkloadPatterns(workloadMetrics)
	require.NoError(t, err)
	require.NotNil(t, analysis)

	// Validate analysis structure
	assert.Equal(t, "advanced-workload", analysis.WorkloadName)
	assert.Equal(t, "Deployment", analysis.WorkloadType)
	assert.Equal(t, "test-ns", analysis.Namespace)
	assert.Equal(t, len(workloadMetrics.Pods), analysis.TotalPods)

	// Validate CPU analysis
	assert.NotNil(t, analysis.CPUAnalysis)
	assert.Equal(t, "CPU", analysis.CPUAnalysis.ResourceType)
	assert.Greater(t, analysis.CPUAnalysis.TotalDataPoints, 0)
	assert.Greater(t, analysis.CPUAnalysis.WorkloadP95, analysis.CPUAnalysis.WorkloadP50)

	// Validate Memory analysis
	assert.NotNil(t, analysis.MemoryAnalysis)
	assert.Equal(t, "Memory", analysis.MemoryAnalysis.ResourceType)
	assert.Greater(t, analysis.MemoryAnalysis.TotalDataPoints, 0)
	assert.Greater(t, analysis.MemoryAnalysis.WorkloadP95, analysis.MemoryAnalysis.WorkloadP50)

	// Validate usage patterns
	assert.GreaterOrEqual(t, len(analysis.UsagePatterns), 0)

	// Validate recommendations
	assert.GreaterOrEqual(t, len(analysis.Recommendations), 1)
	for _, rec := range analysis.Recommendations {
		assert.NotEmpty(t, rec.Type)
		assert.NotEmpty(t, rec.Description)
		assert.NotEmpty(t, rec.Priority)
		assert.Contains(t, []string{"High", "Medium", "Low"}, rec.Priority)
	}
}

// TestResourceThresholdValidation tests various threshold configurations
func TestResourceThresholdValidation(t *testing.T) {
	ctx := context.Background()

	mockClient := metrics.NewMockMetricsClient()
	engine := analyzer.NewRecommendationEngine()

	workloadMetrics, err := mockClient.GetWorkloadMetrics(
		ctx, "test", "workload", "Deployment", 12*time.Hour)
	require.NoError(t, err)

	testCases := []struct {
		name       string
		thresholds rightsizingv1alpha1.ResourceThresholds
		expectErr  bool
	}{
		{
			name: "ValidThresholds",
			thresholds: rightsizingv1alpha1.ResourceThresholds{
				CPUUtilizationPercentile:    90,
				MemoryUtilizationPercentile: 95,
				SafetyMargin:                15,
				MinCPU:                      *resource.NewMilliQuantity(10, resource.DecimalSI),
				MinMemory:                   *resource.NewQuantity(16777216, resource.BinarySI), // 16Mi
			},
			expectErr: false,
		},
		{
			name: "ExtremePercentiles",
			thresholds: rightsizingv1alpha1.ResourceThresholds{
				CPUUtilizationPercentile:    99,
				MemoryUtilizationPercentile: 99,
				SafetyMargin:                50,
			},
			expectErr: false,
		},
		{
			name: "ZeroMinimums",
			thresholds: rightsizingv1alpha1.ResourceThresholds{
				CPUUtilizationPercentile:    95,
				MemoryUtilizationPercentile: 95,
				SafetyMargin:                20,
			},
			expectErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recommendations, err := engine.GenerateRecommendations(ctx, workloadMetrics, tc.thresholds)

			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Greater(t, len(recommendations), 0)

				// Validate that recommendations respect the thresholds
				for _, rec := range recommendations {
					if !tc.thresholds.MinCPU.IsZero() {
						cpuRequest := rec.RecommendedResources.Requests[corev1.ResourceCPU]
						assert.True(t, cpuRequest.Cmp(tc.thresholds.MinCPU) >= 0,
							"CPU request should meet minimum threshold")
					}

					if !tc.thresholds.MinMemory.IsZero() {
						memRequest := rec.RecommendedResources.Requests[corev1.ResourceMemory]
						assert.True(t, memRequest.Cmp(tc.thresholds.MinMemory) >= 0,
							"Memory request should meet minimum threshold")
					}
				}
			}
		})
	}
}

// TestClusterSavingsReport tests cluster-wide savings analysis
func TestClusterSavingsReport(t *testing.T) {
	// Create sample recommendations
	recommendations := []rightsizingv1alpha1.PodRecommendation{
		{
			PodReference: rightsizingv1alpha1.PodReference{
				Name:      "pod1",
				Namespace: "test",
			},
			PotentialSavings: rightsizingv1alpha1.ResourceSavings{
				CPUSavings:    resource.NewMilliQuantity(50, resource.DecimalSI), // 50m
				MemorySavings: resource.NewQuantity(67108864, resource.BinarySI), // 64Mi
				CostSavings:   "$2.50/month",
			},
		},
		{
			PodReference: rightsizingv1alpha1.PodReference{
				Name:      "pod2",
				Namespace: "test",
			},
			PotentialSavings: rightsizingv1alpha1.ResourceSavings{
				CPUSavings:    resource.NewMilliQuantity(100, resource.DecimalSI), // 100m
				MemorySavings: resource.NewQuantity(134217728, resource.BinarySI), // 128Mi
				CostSavings:   "$5.00/month",
			},
		},
	}

	costCalc := analyzer.NewCostCalculator()
	report := costCalc.EstimateClusterSavings(recommendations)

	assert.Equal(t, 2, report.TotalRecommendations)
	assert.Equal(t, "azure", report.CloudProvider)
	assert.NotEmpty(t, report.TotalCPUSavings)
	assert.NotEmpty(t, report.TotalMemorySavings)
	assert.NotEmpty(t, report.EstimatedMonthlySavings)
	assert.NotEmpty(t, report.EstimatedAnnualSavings)

	// Validate savings are calculated correctly
	assert.Contains(t, report.TotalCPUSavings, "0.150")   // 150m total
	assert.Contains(t, report.TotalMemorySavings, "0.19") // ~192Mi total
}

// BenchmarkRecommendationGeneration benchmarks the recommendation generation process
func BenchmarkRecommendationGeneration(b *testing.B) {
	ctx := context.Background()
	mockClient := metrics.NewMockMetricsClient()
	engine := analyzer.NewRecommendationEngine()

	workloadMetrics, _ := mockClient.GetWorkloadMetrics(
		ctx, "benchmark", "workload", "Deployment", 24*time.Hour)

	thresholds := rightsizingv1alpha1.ResourceThresholds{
		CPUUtilizationPercentile:    95,
		MemoryUtilizationPercentile: 95,
		SafetyMargin:                20,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.GenerateRecommendations(ctx, workloadMetrics, thresholds)
		if err != nil {
			b.Fatalf("Benchmark failed: %v", err)
		}
	}
}
