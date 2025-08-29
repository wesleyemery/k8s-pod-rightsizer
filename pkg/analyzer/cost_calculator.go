package analyzer

import (
	"context"
	"fmt"
	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"time"
)

// CostCalculator calculates cost savings from resource recommendations
type CostCalculator struct {
	// Cost per CPU core per month (USD)
	CPUCostPerCoreMonth float64
	// Cost per GB memory per month (USD)
	MemoryCostPerGBMonth float64
	// Cloud provider (for different pricing models)
	CloudProvider string
	// Azure pricing client for real-time pricing data
	AzurePricingClient *AzurePricingClient
	// Node-specific pricing data (node name -> pricing info)
	NodePricingData map[string]*AzurePriceData
}

// NewCostCalculator creates a cost calculator with default AKS pricing
func NewCostCalculator() *CostCalculator {
	return &CostCalculator{
		// Default fallback costs (used when real pricing unavailable)
		CPUCostPerCoreMonth:  20.0, // ~$20 per core per month
		MemoryCostPerGBMonth: 2.5,  // ~$2.50 per GB per month
		CloudProvider:        "azure",
		AzurePricingClient:   NewAzurePricingClient(),
		NodePricingData:      make(map[string]*AzurePriceData),
	}
}

// NewCostCalculatorWithAzurePricing creates a cost calculator with real Azure pricing
func NewCostCalculatorWithAzurePricing(ctx context.Context, k8sClient client.Client) (*CostCalculator, error) {
	logger := log.FromContext(ctx)

	calculator := &CostCalculator{
		CPUCostPerCoreMonth:  20.0, // Fallback
		MemoryCostPerGBMonth: 2.5,  // Fallback
		CloudProvider:        "azure",
		AzurePricingClient:   NewAzurePricingClient(),
		NodePricingData:      make(map[string]*AzurePriceData),
	}

	// Fetch real pricing data for cluster nodes
	logger.Info("Fetching real-time Azure pricing data for cluster nodes")
	pricingInfo, err := calculator.AzurePricingClient.GetClusterPricingInfo(ctx, k8sClient)
	if err != nil {
		logger.Error(err, "Failed to fetch Azure pricing data, using defaults")
		return calculator, nil // Return with defaults rather than error
	}

	calculator.NodePricingData = pricingInfo

	// Calculate average pricing across all nodes for fallback
	if len(pricingInfo) > 0 {
		var totalCPUCost, totalMemoryCost float64
		var nodeCount int

		for _, priceData := range pricingInfo {
			if priceData.CPUCostPerCore > 0 {
				totalCPUCost += priceData.CPUCostPerCore
				nodeCount++
			}
			if priceData.MemoryCostPerGB > 0 {
				totalMemoryCost += priceData.MemoryCostPerGB
			}
		}

		if nodeCount > 0 {
			calculator.CPUCostPerCoreMonth = totalCPUCost / float64(nodeCount)
			calculator.MemoryCostPerGBMonth = totalMemoryCost / float64(nodeCount)

			logger.Info("Updated calculator with real Azure pricing",
				"avgCPUCostPerCore", fmt.Sprintf("$%.2f/month", calculator.CPUCostPerCoreMonth),
				"avgMemoryCostPerGB", fmt.Sprintf("$%.2f/month", calculator.MemoryCostPerGBMonth),
				"nodesWithPricing", nodeCount)
		}
	}

	return calculator, nil
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
	return c.CalculateSavingsForNode(current, recommended, "")
}

// CalculateSavingsForNode calculates potential cost savings for a specific node
// If nodeName is provided and node-specific pricing is available, uses that; otherwise uses defaults
func (c *CostCalculator) CalculateSavingsForNode(current, recommended corev1.ResourceRequirements, nodeName string) rightsizingv1alpha1.ResourceSavings {
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

	// Calculate cost savings using node-specific pricing if available
	monthlyCostSavings := c.calculateMonthlySavingsForNode(savings, nodeName)
	if monthlyCostSavings > 0 {
		savings.CostSavings = fmt.Sprintf("$%.2f/month", monthlyCostSavings)
	}

	return savings
}

// calculateMonthlySavings calculates monthly cost savings in USD using default pricing
func (c *CostCalculator) calculateMonthlySavings(savings rightsizingv1alpha1.ResourceSavings) float64 {
	return c.calculateMonthlySavingsForNode(savings, "")
}

// calculateMonthlySavingsForNode calculates monthly cost savings using node-specific pricing when available
func (c *CostCalculator) calculateMonthlySavingsForNode(savings rightsizingv1alpha1.ResourceSavings, nodeName string) float64 {
	totalSavings := 0.0

	// Use node-specific pricing if available
	var cpuCostPerCore, memoryCostPerGB float64
	if nodeName != "" && c.NodePricingData != nil {
		if nodePrice, exists := c.NodePricingData[nodeName]; exists && nodePrice != nil {
			cpuCostPerCore = nodePrice.CPUCostPerCore
			memoryCostPerGB = nodePrice.MemoryCostPerGB
		}
	}

	// Fall back to default pricing if node-specific pricing not available
	if cpuCostPerCore == 0 {
		cpuCostPerCore = c.CPUCostPerCoreMonth
	}
	if memoryCostPerGB == 0 {
		memoryCostPerGB = c.MemoryCostPerGBMonth
	}

	// CPU savings
	if savings.CPUSavings != nil {
		cpuCores := savings.CPUSavings.AsApproximateFloat64()
		totalSavings += cpuCores * cpuCostPerCore
	}

	// Memory savings
	if savings.MemorySavings != nil {
		memoryGB := savings.MemorySavings.AsApproximateFloat64() / (1024 * 1024 * 1024)
		totalSavings += memoryGB * memoryCostPerGB
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

// EstimateClusterSavingsWithAzureBreakdown provides detailed savings analysis with Azure SKU breakdown
func (c *CostCalculator) EstimateClusterSavingsWithAzureBreakdown(recommendations []rightsizingv1alpha1.PodRecommendation) ClusterSavingsReport {
	report := c.EstimateClusterSavings(recommendations)

	// Add Azure-specific analysis if we have node pricing data
	if c.NodePricingData != nil && len(c.NodePricingData) > 0 {
		report.UsingRealPricing = true
		report.NodeSKUBreakdown = make(map[string]*NodeSKUSavings)

		// Group by SKU
		skuGroups := make(map[string]*NodeSKUSavings)

		for _, priceData := range c.NodePricingData {
			skuName := priceData.SKUName
			if skuName == "" {
				continue
			}

			if _, exists := skuGroups[skuName]; !exists {
				skuGroups[skuName] = &NodeSKUSavings{
					SKUName:         skuName,
					CPUCostPerCore:  priceData.CPUCostPerCore,
					MemoryCostPerGB: priceData.MemoryCostPerGB,
				}
			}

			skuGroups[skuName].NodeCount++
			// Calculate total monthly cost for this node type
			monthlyPrice := priceData.UnitPrice * 730 // hours per month
			skuGroups[skuName].TotalMonthlyCost += monthlyPrice
		}

		// Calculate savings per SKU based on recommendations
		for _, rec := range recommendations {
			// Try to determine which node this pod runs on
			// This would typically require additional pod->node mapping
			// For now, distribute savings proportionally across SKUs

			if rec.PotentialSavings.CostSavings != "" {
				// Parse cost savings (format: "$X.XX/month")
				costStr := strings.TrimPrefix(rec.PotentialSavings.CostSavings, "$")
				costStr = strings.TrimSuffix(costStr, "/month")
				if costSavings := parseFloat(costStr); costSavings > 0 {
					// Distribute proportionally across SKUs for now
					// In a real implementation, you'd track pod->node mappings
					skuCount := len(skuGroups)
					if skuCount > 0 {
						savingsPerSKU := costSavings / float64(skuCount)
						for _, skuSavings := range skuGroups {
							skuSavings.PotentialSavings += savingsPerSKU
							skuSavings.RecommendationCount++
						}
					}
				}
			}
		}

		report.NodeSKUBreakdown = skuGroups

		// Add pricing data age
		oldestData := time.Now()
		for _, priceData := range c.NodePricingData {
			if priceData.LastUpdated.Before(oldestData) {
				oldestData = priceData.LastUpdated
			}
		}
		report.PricingDataAge = fmt.Sprintf("%.1f hours ago", time.Since(oldestData).Hours())
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
	// Azure-specific fields
	NodeSKUBreakdown map[string]*NodeSKUSavings `json:"nodeSKUBreakdown,omitempty"`
	UsingRealPricing bool                       `json:"usingRealPricing"`
	PricingDataAge   string                     `json:"pricingDataAge,omitempty"`
}

// NodeSKUSavings provides savings breakdown by node SKU
type NodeSKUSavings struct {
	SKUName             string  `json:"skuName"`
	NodeCount           int     `json:"nodeCount"`
	TotalMonthlyCost    float64 `json:"totalMonthlyCost"`
	PotentialSavings    float64 `json:"potentialSavings"`
	RecommendationCount int     `json:"recommendationCount"`
	CPUCostPerCore      float64 `json:"cpuCostPerCore"`
	MemoryCostPerGB     float64 `json:"memoryCostPerGB"`
}

// parseFloat safely parses a float from string, returning 0.0 on error
func parseFloat(s string) float64 {
	if val := 0.0; len(s) > 0 {
		fmt.Sscanf(s, "%f", &val)
		return val
	}
	return 0.0
}
