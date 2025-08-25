package analyzer

import (
	"fmt"
	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// CostCalculator calculates cost savings from resource recommendations
type CostCalculator struct {
	// Cost per CPU core per month (USD)
	CPUCostPerCoreMonth float64
	// Cost per GB memory per month (USD)
	MemoryCostPerGBMonth float64
	// Cloud provider (for different pricing models)
	CloudProvider string
}

// NewCostCalculator creates a cost calculator with default AKS pricing
func NewCostCalculator() *CostCalculator {
	return &CostCalculator{
		// AKS Standard_D2s_v3 approximate costs (adjust for your region)
		CPUCostPerCoreMonth:  20.0, // ~$20 per core per month
		MemoryCostPerGBMonth: 2.5,  // ~$2.50 per GB per month
		CloudProvider:        "azure",
	}
}

// NewAWSCostCalculator creates a cost calculator with EKS pricing
func NewAWSCostCalculator() *CostCalculator {
	return &CostCalculator{
		CPUCostPerCoreMonth:  25.0, // EC2 pricing varies significantly
		MemoryCostPerGBMonth: 3.0,
		CloudProvider:        "aws",
	}
}

// NewGCPCostCalculator creates a cost calculator with GKE pricing
func NewGCPCostCalculator() *CostCalculator {
	return &CostCalculator{
		CPUCostPerCoreMonth:  22.0,
		MemoryCostPerGBMonth: 2.8,
		CloudProvider:        "gcp",
	}
}

// CalculateSavings calculates potential cost savings from a recommendation
func (c *CostCalculator) CalculateSavings(current, recommended corev1.ResourceRequirements) rightsizingv1alpha1.ResourceSavings {
	savings := rightsizingv1alpha1.ResourceSavings{}

	// Calculate CPU savings
	if currentCPU, exists := current.Requests[corev1.ResourceCPU]; exists {
		if recommendedCPU, recExists := recommended.Requests[corev1.ResourceCPU]; recExists {
			cpuDiff := currentCPU.AsApproximateFloat64() - recommendedCPU.AsApproximateFloat64()
			if cpuDiff > 0 {
				cpuSavings := resource.NewMilliQuantity(int64(cpuDiff*1000), resource.DecimalSI)
				savings.CPUSavings = cpuSavings
			}
		}
	}

	// Calculate Memory savings
	if currentMem, exists := current.Requests[corev1.ResourceMemory]; exists {
		if recommendedMem, recExists := recommended.Requests[corev1.ResourceMemory]; recExists {
			memDiff := currentMem.AsApproximateFloat64() - recommendedMem.AsApproximateFloat64()
			if memDiff > 0 {
				memSavings := resource.NewQuantity(int64(memDiff), resource.BinarySI)
				savings.MemorySavings = memSavings
			}
		}
	}

	// Calculate cost savings
	monthlyCostSavings := c.calculateMonthlySavings(savings)
	if monthlyCostSavings > 0 {
		savings.CostSavings = fmt.Sprintf("$%.2f/month", monthlyCostSavings)
	}

	return savings
}

// calculateMonthlySavings calculates monthly cost savings in USD
func (c *CostCalculator) calculateMonthlySavings(savings rightsizingv1alpha1.ResourceSavings) float64 {
	totalSavings := 0.0

	// CPU savings
	if savings.CPUSavings != nil {
		cpuCores := savings.CPUSavings.AsApproximateFloat64()
		totalSavings += cpuCores * c.CPUCostPerCoreMonth
	}

	// Memory savings
	if savings.MemorySavings != nil {
		memoryGB := savings.MemorySavings.AsApproximateFloat64() / (1024 * 1024 * 1024)
		totalSavings += memoryGB * c.MemoryCostPerGBMonth
	}

	return totalSavings
}

// EstimateClusterSavings estimates total cluster savings from all recommendations
func (c *CostCalculator) EstimateClusterSavings(recommendations []rightsizingv1alpha1.PodRecommendation) ClusterSavingsReport {
	report := ClusterSavingsReport{
		TotalRecommendations: len(recommendations),
		CloudProvider:        c.CloudProvider,
	}

	totalCPUSavings := 0.0
	totalMemorySavings := 0.0

	for _, rec := range recommendations {
		if rec.PotentialSavings.CPUSavings != nil {
			totalCPUSavings += rec.PotentialSavings.CPUSavings.AsApproximateFloat64()
		}
		if rec.PotentialSavings.MemorySavings != nil {
			totalMemorySavings += rec.PotentialSavings.MemorySavings.AsApproximateFloat64() / (1024 * 1024 * 1024)
		}
	}

	totalMonthlySavings := totalCPUSavings*c.CPUCostPerCoreMonth + totalMemorySavings*c.MemoryCostPerGBMonth

	report.TotalCPUSavings = fmt.Sprintf("%.3f cores", totalCPUSavings)
	report.TotalMemorySavings = fmt.Sprintf("%.2f GB", totalMemorySavings)
	report.EstimatedMonthlySavings = fmt.Sprintf("$%.2f", totalMonthlySavings)
	report.EstimatedAnnualSavings = fmt.Sprintf("$%.2f", totalMonthlySavings*12)

	// ROI calculation (assuming ScaleOps costs ~$10K/year for a medium cluster)
	alternativeCost := 10000.0
	if totalMonthlySavings*12 > alternativeCost {
		paybackMonths := alternativeCost / totalMonthlySavings
		report.ROIAnalysis = fmt.Sprintf("Payback period: %.1f months vs commercial solutions", paybackMonths)
	}

	return report
}

// ClusterSavingsReport provides a comprehensive savings analysis
type ClusterSavingsReport struct {
	TotalRecommendations    int
	TotalCPUSavings         string
	TotalMemorySavings      string
	EstimatedMonthlySavings string
	EstimatedAnnualSavings  string
	ROIAnalysis             string
	CloudProvider           string
}
