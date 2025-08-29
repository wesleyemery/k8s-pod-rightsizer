package analyzer

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
)

func TestAzurePricingClient_GetNodeSKUInfo(t *testing.T) {
	// Create fake Kubernetes client with sample nodes
	s := scheme.Scheme
	client := fake.NewClientBuilder().WithScheme(s).Build()

	// Create sample nodes with different SKUs
	nodes := []corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
				Labels: map[string]string{
					"node.kubernetes.io/instance-type": "Standard_D2s_v3",
					"topology.kubernetes.io/region":    "eastus",
					"topology.kubernetes.io/zone":      "eastus-1",
				},
			},
			Spec: corev1.NodeSpec{
				ProviderID: "azure:///subscriptions/12345/resourceGroups/test/providers/Microsoft.Compute/virtualMachines/node1",
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node2",
				Labels: map[string]string{
					"node.kubernetes.io/instance-type": "Standard_D4s_v3",
					"topology.kubernetes.io/region":    "eastus",
					"topology.kubernetes.io/zone":      "eastus-2",
				},
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
		},
	}

	ctx := context.Background()
	for _, node := range nodes {
		if err := client.Create(ctx, &node); err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}
	}

	// Test GetNodeSKUInfo
	pricingClient := NewAzurePricingClient()
	nodeInfo, err := pricingClient.GetNodeSKUInfo(ctx, client)
	if err != nil {
		t.Fatalf("GetNodeSKUInfo failed: %v", err)
	}

	if len(nodeInfo) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(nodeInfo))
	}

	// Verify node1 info
	if info, exists := nodeInfo["node1"]; exists {
		if info.SKUName != "Standard_D2s_v3" {
			t.Errorf("Expected SKU Standard_D2s_v3, got %s", info.SKUName)
		}
		if info.Region != "eastus" {
			t.Errorf("Expected region eastus, got %s", info.Region)
		}
		if info.CPUCores != 2 {
			t.Errorf("Expected 2 CPU cores, got %d", info.CPUCores)
		}
		if info.MemoryGB != 8.0 {
			t.Errorf("Expected 8GB memory, got %.1f", info.MemoryGB)
		}
	} else {
		t.Error("node1 info not found")
	}

	// Verify node2 info
	if info, exists := nodeInfo["node2"]; exists {
		if info.SKUName != "Standard_D4s_v3" {
			t.Errorf("Expected SKU Standard_D4s_v3, got %s", info.SKUName)
		}
		if info.CPUCores != 4 {
			t.Errorf("Expected 4 CPU cores, got %d", info.CPUCores)
		}
		if info.MemoryGB != 16.0 {
			t.Errorf("Expected 16GB memory, got %.1f", info.MemoryGB)
		}
	} else {
		t.Error("node2 info not found")
	}
}

func TestAzurePricingClient_getVMSpecifications(t *testing.T) {
	client := NewAzurePricingClient()

	tests := []struct {
		name           string
		skuName        string
		expectedCPU    int
		expectedMemory float64
	}{
		{
			name:           "Standard D2s v3",
			skuName:        "Standard_D2s_v3",
			expectedCPU:    2,
			expectedMemory: 8,
		},
		{
			name:           "Standard E4s v4",
			skuName:        "Standard_E4s_v4",
			expectedCPU:    4,
			expectedMemory: 32,
		},
		{
			name:           "Unknown SKU",
			skuName:        "Standard_Unknown",
			expectedCPU:    2, // Default fallback
			expectedMemory: 8, // Default fallback
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs := client.getVMSpecifications(tt.skuName)

			if specs.CPUCores != tt.expectedCPU {
				t.Errorf("Expected %d CPU cores, got %d", tt.expectedCPU, specs.CPUCores)
			}

			if specs.MemoryGB != tt.expectedMemory {
				t.Errorf("Expected %.1f GB memory, got %.1f", tt.expectedMemory, specs.MemoryGB)
			}
		})
	}
}

func TestCostCalculator_WithAzurePricing(t *testing.T) {
	// Test the enhanced cost calculator with mock Azure pricing data
	calculator := NewCostCalculator()

	// Add mock pricing data
	calculator.NodePricingData = map[string]*AzurePriceData{
		"node1": {
			SKUName:         "Standard_D2s_v3",
			UnitPrice:       0.096, // $0.096/hour
			CPUCores:        2,
			MemoryGB:        8,
			CPUCostPerCore:  35.04, // Calculated: 0.096 * 730 / 2
			MemoryCostPerGB: 8.76,  // Calculated: 0.096 * 730 / 8
			LastUpdated:     time.Now(),
		},
		"node2": {
			SKUName:         "Standard_D4s_v3",
			UnitPrice:       0.192, // $0.192/hour
			CPUCores:        4,
			MemoryGB:        16,
			CPUCostPerCore:  35.04, // Same per-core cost
			MemoryCostPerGB: 8.76,  // Same per-GB cost
			LastUpdated:     time.Now(),
		},
	}

	// Test cost calculation with node-specific pricing
	currentResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
	}

	recommendedResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}

	// Test with node-specific pricing
	savings := calculator.CalculateSavingsForNode(currentResources, recommendedResources, "node1")

	if savings.CPUSavings == nil {
		t.Error("Expected CPU savings, got nil")
	} else {
		expectedCPUSavings := int64(300) // 500m - 200m = 300m
		if savings.CPUSavings.MilliValue() != expectedCPUSavings {
			t.Errorf("Expected %dm CPU savings, got %dm", expectedCPUSavings, savings.CPUSavings.MilliValue())
		}
	}

	if savings.MemorySavings == nil {
		t.Error("Expected Memory savings, got nil")
	}

	if savings.CostSavings == "" {
		t.Error("Expected cost savings, got empty string")
	}

	// Test cluster savings report with Azure breakdown
	recommendations := []rightsizingv1alpha1.PodRecommendation{
		{
			PodReference: rightsizingv1alpha1.PodReference{
				Name: "test-pod-1",
			},
			PotentialSavings: rightsizingv1alpha1.ResourceSavings{
				CostSavings: "$5.00/month",
			},
		},
		{
			PodReference: rightsizingv1alpha1.PodReference{
				Name: "test-pod-2",
			},
			PotentialSavings: rightsizingv1alpha1.ResourceSavings{
				CostSavings: "$3.50/month",
			},
		},
	}

	report := calculator.EstimateClusterSavingsWithAzureBreakdown(recommendations)

	if !report.UsingRealPricing {
		t.Error("Expected UsingRealPricing to be true")
	}

	if report.NodeSKUBreakdown == nil {
		t.Error("Expected NodeSKUBreakdown to be populated")
	}

	if len(report.NodeSKUBreakdown) == 0 {
		t.Error("Expected at least one SKU in breakdown")
	}

	if report.PricingDataAge == "" {
		t.Error("Expected pricing data age to be set")
	}
}
