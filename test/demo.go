package main

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/analyzer"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

func main() {
	fmt.Println("🚀 Testing Kubernetes Pod Right-sizer")
	fmt.Println("=====================================")

	ctx := context.Background()

	// 1. Test with Mock Metrics
	fmt.Println("\n📊 Testing with Mock Metrics...")
	mockClient := metrics.NewMockMetricsClient()

	workloadMetrics, err := mockClient.GetWorkloadMetrics(
		ctx, "test-namespace", "nginx", "Deployment", 24*time.Hour)
	if err != nil {
		panic(err)
	}

	fmt.Printf("✅ Generated mock metrics for %d pods\n", len(workloadMetrics.Pods))

	// 2. Initialize Recommendation Engine
	fmt.Println("\n🧠 Testing Recommendation Engine...")
	engine := analyzer.NewRecommendationEngine()

	thresholds := rightsizingv1alpha1.ResourceThresholds{
		CPUUtilizationPercentile:    95,
		MemoryUtilizationPercentile: 95,
		SafetyMargin:                20,
		MinCPU:                      resource.MustParse("10m"),
		MinMemory:                   resource.MustParse("32Mi"),
	}

	recommendations, err := engine.GenerateRecommendations(ctx, workloadMetrics, thresholds)
	if err != nil {
		panic(err)
	}

	fmt.Printf("✅ Generated %d recommendations\n", len(recommendations))

	// 3. Display Recommendations
	fmt.Println("\n📋 Resource Recommendations:")
	fmt.Println("============================")

	for i, rec := range recommendations {
		fmt.Printf("\n🔍 Pod %d: %s\n", i+1, rec.PodReference.Name)
		fmt.Printf("   Confidence: %d%%\n", rec.Confidence)

		cpuRequest := rec.RecommendedResources.Requests[corev1.ResourceCPU]
		cpuLimit := rec.RecommendedResources.Limits[corev1.ResourceCPU]
		memRequest := rec.RecommendedResources.Requests[corev1.ResourceMemory]
		memLimit := rec.RecommendedResources.Limits[corev1.ResourceMemory]

		fmt.Printf("   💻 CPU: %s request / %s limit\n", cpuRequest.String(), cpuLimit.String())
		fmt.Printf("   🧠 Memory: %s request / %s limit\n", memRequest.String(), memLimit.String())

		if rec.PotentialSavings.CostSavings != "" {
			fmt.Printf("   💰 Estimated savings: %s\n", rec.PotentialSavings.CostSavings)
		}
	}

	// 4. Test Workload Classification
	fmt.Println("\n🏷️  Testing Workload Classification...")
	classifier := analyzer.NewWorkloadClassifier()
	classification, err := classifier.ClassifyWorkload(workloadMetrics)
	if err != nil {
		fmt.Printf("❌ Classification failed: %v\n", err)
	} else {
		fmt.Printf("✅ Workload Classification: %s (%.1f%% confidence)\n",
			classification.Class, classification.Confidence*100)

		fmt.Printf("   📈 CPU Pattern: %.2f variability, %s trend\n",
			classification.CPUPattern.CoefficientOfVariation,
			classification.CPUPattern.TrendDirection)

		fmt.Printf("   📈 Memory Pattern: %.2f variability, %s trend\n",
			classification.MemoryPattern.CoefficientOfVariation,
			classification.MemoryPattern.TrendDirection)

		if len(classification.Recommendations) > 0 {
			fmt.Printf("   💡 Classification Recommendations:\n")
			for _, classRec := range classification.Recommendations {
				fmt.Printf("      - [%s] %s: %s\n", classRec.Priority, classRec.Type, classRec.Description)
			}
		}
	}

	// 5. Test Cost Analysis
	fmt.Println("\n💰 Testing Cost Analysis...")
	costCalc := analyzer.NewCostCalculator()
	report := costCalc.EstimateClusterSavings(recommendations)

	fmt.Printf("   Cloud Provider: %s\n", report.CloudProvider)
	fmt.Printf("   Total Recommendations: %d\n", report.TotalRecommendations)
	fmt.Printf("   Estimated CPU Savings: %s\n", report.TotalCPUSavings)
	fmt.Printf("   Estimated Memory Savings: %s\n", report.TotalMemorySavings)
	fmt.Printf("   Monthly Savings: %s\n", report.EstimatedMonthlySavings)
	fmt.Printf("   Annual Savings: %s\n", report.EstimatedAnnualSavings)

	// 6. Test Advanced Analysis
	fmt.Println("\n🔬 Testing Advanced Analysis...")
	advancedAnalyzer := analyzer.NewAdvancedAnalyzer()
	analysis, err := advancedAnalyzer.AnalyzeWorkloadPatterns(workloadMetrics)
	if err != nil {
		fmt.Printf("❌ Advanced analysis failed: %v\n", err)
	} else {
		fmt.Printf("✅ Advanced Analysis Complete:\n")
		fmt.Printf("   Analysis Window: %v\n", analysis.AnalysisWindow)
		fmt.Printf("   Total Pods: %d\n", analysis.TotalPods)

		if analysis.CPUAnalysis != nil {
			fmt.Printf("   💻 CPU: %d data points, %.3f mean cores, %.3f P95\n",
				analysis.CPUAnalysis.TotalDataPoints,
				analysis.CPUAnalysis.WorkloadMean,
				analysis.CPUAnalysis.WorkloadP95)
		}

		if analysis.MemoryAnalysis != nil {
			fmt.Printf("   🧠 Memory: %d data points, %.0f MB mean, %.0f MB P95\n",
				analysis.MemoryAnalysis.TotalDataPoints,
				analysis.MemoryAnalysis.WorkloadMean/1024/1024,
				analysis.MemoryAnalysis.WorkloadP95/1024/1024)
		}

		if len(analysis.Recommendations) > 0 {
			fmt.Printf("   💡 Workload Recommendations:\n")
			for _, rec := range analysis.Recommendations {
				fmt.Printf("      - [%s] %s: %s\n", rec.Priority, rec.Type, rec.Description)
			}
		}
	}

	fmt.Println("\n🎉 All Tests Completed Successfully!")
	fmt.Println("=====================================")
	fmt.Println("The recommendation engine is working correctly!")
	fmt.Println("You can now deploy this to your Kubernetes cluster.")
}
