package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AzurePricingClient fetches pricing data from Azure Retail Prices API
type AzurePricingClient struct {
	HTTPClient *http.Client
	BaseURL    string
	Cache      map[string]*AzurePriceData
	CacheTTL   time.Duration
}

// AzurePriceData represents pricing information for an Azure VM SKU
type AzurePriceData struct {
	SKUName         string    `json:"skuName"`
	ServiceName     string    `json:"serviceName"`
	ProductName     string    `json:"productName"`
	UnitPrice       float64   `json:"unitPrice"`
	CurrencyCode    string    `json:"currencyCode"`
	UnitOfMeasure   string    `json:"unitOfMeasure"`
	Location        string    `json:"location"`
	LastUpdated     time.Time `json:"-"`
	CPUCores        int       `json:"-"`
	MemoryGB        float64   `json:"-"`
	CPUCostPerCore  float64   `json:"-"`
	MemoryCostPerGB float64   `json:"-"`
}

// AzurePricingResponse represents the API response from Azure Retail Prices API
type AzurePricingResponse struct {
	BillingCurrency    string `json:"BillingCurrency"`
	CustomerEntityID   string `json:"CustomerEntityId"`
	CustomerEntityType string `json:"CustomerEntityType"`
	Items              []struct {
		CurrencyCode         string  `json:"currencyCode"`
		TierMinimumUnits     int     `json:"tierMinimumUnits"`
		RetailPrice          float64 `json:"retailPrice"`
		UnitPrice            float64 `json:"unitPrice"`
		ArmRegionName        string  `json:"armRegionName"`
		Location             string  `json:"location"`
		EffectiveStartDate   string  `json:"effectiveStartDate"`
		MeterID              string  `json:"meterId"`
		MeterName            string  `json:"meterName"`
		ProductID            string  `json:"productId"`
		SkuID                string  `json:"skuId"`
		ProductName          string  `json:"productName"`
		SkuName              string  `json:"skuName"`
		ServiceName          string  `json:"serviceName"`
		ServiceID            string  `json:"serviceId"`
		ServiceFamily        string  `json:"serviceFamily"`
		UnitOfMeasure        string  `json:"unitOfMeasure"`
		Type                 string  `json:"type"`
		IsPrimaryMeterRegion bool    `json:"isPrimaryMeterRegion"`
		ArmSkuName           string  `json:"armSkuName"`
	} `json:"Items"`
	NextPageLink string `json:"NextPageLink"`
	Count        int    `json:"Count"`
}

// NodeSKUInfo contains information about a Kubernetes node's Azure VM SKU
type NodeSKUInfo struct {
	NodeName     string
	SKUName      string
	Region       string
	CPUCores     int
	MemoryGB     float64
	InstanceType string
	Zone         string
}

// NewAzurePricingClient creates a new Azure pricing client
func NewAzurePricingClient() *AzurePricingClient {
	return &AzurePricingClient{
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		BaseURL:  "https://prices.azure.com/api/retail/prices",
		Cache:    make(map[string]*AzurePriceData),
		CacheTTL: 24 * time.Hour, // Cache pricing data for 24 hours
	}
}

// GetNodeSKUInfo extracts Azure VM SKU information from Kubernetes nodes
func (c *AzurePricingClient) GetNodeSKUInfo(ctx context.Context, k8sClient client.Client) (map[string]*NodeSKUInfo, error) {
	logger := log.FromContext(ctx)

	var nodeList corev1.NodeList
	if err := k8sClient.List(ctx, &nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodeInfo := make(map[string]*NodeSKUInfo)

	for _, node := range nodeList.Items {
		info := &NodeSKUInfo{
			NodeName: node.Name,
		}

		// Extract SKU information from node labels and annotations
		if instanceType, ok := node.Labels["node.kubernetes.io/instance-type"]; ok {
			info.InstanceType = instanceType
			info.SKUName = instanceType
		}

		if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
			info.Region = region
		}

		if zone, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
			info.Zone = zone
		}

		// Extract CPU and Memory from node capacity
		if cpu := node.Status.Capacity[corev1.ResourceCPU]; !cpu.IsZero() {
			info.CPUCores = int(cpu.Value())
		}

		if memory := node.Status.Capacity[corev1.ResourceMemory]; !memory.IsZero() {
			// Convert from bytes to GB
			info.MemoryGB = float64(memory.Value()) / (1024 * 1024 * 1024)
		}

		// Try to get SKU from Azure-specific labels/annotations
		if azureSKU, ok := node.Labels["kubernetes.azure.com/node-image-version"]; ok {
			logger.V(1).Info("Found Azure node image version", "node", node.Name, "version", azureSKU)
		}

		if providerID := node.Spec.ProviderID; strings.Contains(providerID, "azure") {
			// Extract more detailed Azure information from provider ID
			// Format: azure:///subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Compute/virtualMachines/{vm}
			parts := strings.Split(providerID, "/")
			if len(parts) > 0 {
				logger.V(1).Info("Azure provider ID found", "node", node.Name, "providerID", providerID)
			}
		}

		if info.SKUName != "" {
			nodeInfo[node.Name] = info
			logger.Info("Discovered node SKU info",
				"node", node.Name,
				"sku", info.SKUName,
				"region", info.Region,
				"cpu", info.CPUCores,
				"memory", fmt.Sprintf("%.1fGB", info.MemoryGB))
		}
	}

	return nodeInfo, nil
}

// GetSKUPricing fetches pricing data for a specific Azure VM SKU
func (c *AzurePricingClient) GetSKUPricing(ctx context.Context, skuName, region string) (*AzurePriceData, error) {
	logger := log.FromContext(ctx)

	cacheKey := fmt.Sprintf("%s-%s", skuName, region)

	// Check cache first
	if cached, exists := c.Cache[cacheKey]; exists {
		if time.Since(cached.LastUpdated) < c.CacheTTL {
			logger.V(1).Info("Using cached pricing data", "sku", skuName, "region", region)
			return cached, nil
		}
		// Cache expired, remove it
		delete(c.Cache, cacheKey)
	}

	logger.Info("Fetching pricing data from Azure API", "sku", skuName, "region", region)

	// Build API URL with filters
	// Filter for Virtual Machines service, specific SKU, and region
	filter := fmt.Sprintf("serviceName eq 'Virtual Machines' and armSkuName eq '%s' and armRegionName eq '%s'",
		skuName, region)

	url := fmt.Sprintf("%s?$filter=%s", c.BaseURL, filter)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pricing data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure pricing API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var pricingResp AzurePricingResponse
	if err := json.Unmarshal(body, &pricingResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pricing response: %w", err)
	}

	if len(pricingResp.Items) == 0 {
		logger.Info("No pricing data found for SKU", "sku", skuName, "region", region)
		return nil, fmt.Errorf("no pricing data found for SKU %s in region %s", skuName, region)
	}

	// Find the best pricing item (usually the first one for Linux VMs)
	var bestItem *struct {
		CurrencyCode         string  `json:"currencyCode"`
		TierMinimumUnits     int     `json:"tierMinimumUnits"`
		RetailPrice          float64 `json:"retailPrice"`
		UnitPrice            float64 `json:"unitPrice"`
		ArmRegionName        string  `json:"armRegionName"`
		Location             string  `json:"location"`
		EffectiveStartDate   string  `json:"effectiveStartDate"`
		MeterID              string  `json:"meterId"`
		MeterName            string  `json:"meterName"`
		ProductID            string  `json:"productId"`
		SkuID                string  `json:"skuId"`
		ProductName          string  `json:"productName"`
		SkuName              string  `json:"skuName"`
		ServiceName          string  `json:"serviceName"`
		ServiceID            string  `json:"serviceId"`
		ServiceFamily        string  `json:"serviceFamily"`
		UnitOfMeasure        string  `json:"unitOfMeasure"`
		Type                 string  `json:"type"`
		IsPrimaryMeterRegion bool    `json:"isPrimaryMeterRegion"`
		ArmSkuName           string  `json:"armSkuName"`
	}

	for i := range pricingResp.Items {
		item := &pricingResp.Items[i]
		// Prefer Linux pricing over Windows, and primary regions
		if strings.Contains(strings.ToLower(item.ProductName), "linux") ||
			(!strings.Contains(strings.ToLower(item.ProductName), "windows") && bestItem == nil) {
			bestItem = item
			if item.IsPrimaryMeterRegion {
				break // Primary region takes precedence
			}
		}
	}

	if bestItem == nil {
		bestItem = &pricingResp.Items[0] // Fallback to first item
	}

	// Get VM specifications for cost per core/GB calculation
	vmSpecs := c.getVMSpecifications(skuName)

	priceData := &AzurePriceData{
		SKUName:       bestItem.ArmSkuName,
		ServiceName:   bestItem.ServiceName,
		ProductName:   bestItem.ProductName,
		UnitPrice:     bestItem.UnitPrice,
		CurrencyCode:  bestItem.CurrencyCode,
		UnitOfMeasure: bestItem.UnitOfMeasure,
		Location:      bestItem.Location,
		LastUpdated:   time.Now(),
		CPUCores:      vmSpecs.CPUCores,
		MemoryGB:      vmSpecs.MemoryGB,
	}

	// Calculate per-core and per-GB costs
	if priceData.CPUCores > 0 {
		// UnitPrice is typically per hour, convert to monthly (730 hours average)
		monthlyPrice := priceData.UnitPrice * 730
		priceData.CPUCostPerCore = monthlyPrice / float64(priceData.CPUCores)
	}

	if priceData.MemoryGB > 0 {
		monthlyPrice := priceData.UnitPrice * 730
		priceData.MemoryCostPerGB = monthlyPrice / priceData.MemoryGB
	}

	// Cache the result
	c.Cache[cacheKey] = priceData

	logger.Info("Successfully fetched pricing data",
		"sku", skuName,
		"region", region,
		"hourlyPrice", fmt.Sprintf("$%.4f", priceData.UnitPrice),
		"cpuCostPerCore", fmt.Sprintf("$%.2f/month", priceData.CPUCostPerCore),
		"memoryCostPerGB", fmt.Sprintf("$%.2f/month", priceData.MemoryCostPerGB))

	return priceData, nil
}

// VMSpecifications contains CPU and memory specs for Azure VM SKUs
type VMSpecifications struct {
	CPUCores int
	MemoryGB float64
}

// getVMSpecifications returns the CPU and memory specifications for common Azure VM SKUs
// This is a fallback when we can't get the info from the node itself
func (c *AzurePricingClient) getVMSpecifications(skuName string) VMSpecifications {
	// Common Azure VM SKU specifications
	specs := map[string]VMSpecifications{
		// D-series (General purpose)
		"Standard_D2s_v3":  {CPUCores: 2, MemoryGB: 8},
		"Standard_D4s_v3":  {CPUCores: 4, MemoryGB: 16},
		"Standard_D8s_v3":  {CPUCores: 8, MemoryGB: 32},
		"Standard_D16s_v3": {CPUCores: 16, MemoryGB: 64},
		"Standard_D32s_v3": {CPUCores: 32, MemoryGB: 128},

		// D-series v4
		"Standard_D2s_v4":  {CPUCores: 2, MemoryGB: 8},
		"Standard_D4s_v4":  {CPUCores: 4, MemoryGB: 16},
		"Standard_D8s_v4":  {CPUCores: 8, MemoryGB: 32},
		"Standard_D16s_v4": {CPUCores: 16, MemoryGB: 64},

		// D-series v5
		"Standard_D2s_v5": {CPUCores: 2, MemoryGB: 8},
		"Standard_D4s_v5": {CPUCores: 4, MemoryGB: 16},
		"Standard_D8s_v5": {CPUCores: 8, MemoryGB: 32},

		// B-series (Burstable)
		"Standard_B1ms": {CPUCores: 1, MemoryGB: 2},
		"Standard_B2s":  {CPUCores: 2, MemoryGB: 4},
		"Standard_B4ms": {CPUCores: 4, MemoryGB: 16},
		"Standard_B8ms": {CPUCores: 8, MemoryGB: 32},

		// F-series (Compute optimized)
		"Standard_F2s_v2":  {CPUCores: 2, MemoryGB: 4},
		"Standard_F4s_v2":  {CPUCores: 4, MemoryGB: 8},
		"Standard_F8s_v2":  {CPUCores: 8, MemoryGB: 16},
		"Standard_F16s_v2": {CPUCores: 16, MemoryGB: 32},

		// E-series (Memory optimized)
		"Standard_E2s_v3":  {CPUCores: 2, MemoryGB: 16},
		"Standard_E4s_v3":  {CPUCores: 4, MemoryGB: 32},
		"Standard_E8s_v3":  {CPUCores: 8, MemoryGB: 64},
		"Standard_E16s_v3": {CPUCores: 16, MemoryGB: 128},

		// E-series v4
		"Standard_E2s_v4": {CPUCores: 2, MemoryGB: 16},
		"Standard_E4s_v4": {CPUCores: 4, MemoryGB: 32},
		"Standard_E8s_v4": {CPUCores: 8, MemoryGB: 64},
	}

	if spec, exists := specs[skuName]; exists {
		return spec
	}

	// Default fallback - try to parse from name
	return VMSpecifications{
		CPUCores: 2, // Default
		MemoryGB: 8, // Default
	}
}

// GetClusterPricingInfo returns pricing information for all nodes in the cluster
func (c *AzurePricingClient) GetClusterPricingInfo(ctx context.Context, k8sClient client.Client) (map[string]*AzurePriceData, error) {
	nodeInfo, err := c.GetNodeSKUInfo(ctx, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get node SKU info: %w", err)
	}

	pricingInfo := make(map[string]*AzurePriceData)

	for nodeName, info := range nodeInfo {
		if info.SKUName == "" || info.Region == "" {
			log.FromContext(ctx).Info("Skipping node with missing SKU or region info",
				"node", nodeName, "sku", info.SKUName, "region", info.Region)
			continue
		}

		priceData, err := c.GetSKUPricing(ctx, info.SKUName, info.Region)
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to get pricing for node",
				"node", nodeName, "sku", info.SKUName)
			continue
		}

		// Update with actual node specifications if available
		if info.CPUCores > 0 {
			priceData.CPUCores = info.CPUCores
		}
		if info.MemoryGB > 0 {
			priceData.MemoryGB = info.MemoryGB
		}

		// Recalculate costs with actual specs
		if priceData.CPUCores > 0 && priceData.UnitPrice > 0 {
			monthlyPrice := priceData.UnitPrice * 730
			priceData.CPUCostPerCore = monthlyPrice / float64(priceData.CPUCores)
		}
		if priceData.MemoryGB > 0 && priceData.UnitPrice > 0 {
			monthlyPrice := priceData.UnitPrice * 730
			priceData.MemoryCostPerGB = monthlyPrice / priceData.MemoryGB
		}

		pricingInfo[nodeName] = priceData
	}

	return pricingInfo, nil
}
