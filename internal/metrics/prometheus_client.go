// pkg/metrics/prometheus_client.go
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// PrometheusClient implements MetricsClient interface for Prometheus
type PrometheusClient struct {
	client   api.Client
	queryAPI v1.API
}

// NewPrometheusClient creates a new Prometheus client
func NewPrometheusClient(prometheusURL string, roundTripper http.RoundTripper) (*PrometheusClient, error) {
	client, err := api.NewClient(api.Config{
		Address:      prometheusURL,
		RoundTripper: roundTripper,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus client: %w", err)
	}

	return &PrometheusClient{
		client:   client,
		queryAPI: v1.NewAPI(client),
	}, nil
}

// GetPodMetrics retrieves metrics for a specific pod
func (p *PrometheusClient) GetPodMetrics(ctx context.Context, namespace, podName string, window time.Duration) (*PodMetrics, error) {
	endTime := time.Now()
	startTime := endTime.Add(-window)

	// Get CPU usage metrics
	cpuQuery := fmt.Sprintf(
		`rate(container_cpu_usage_seconds_total{namespace="%s",pod="%s",container!="POD",container!=""}[5m])`,
		namespace, podName,
	)

	cpuResult, _, err := p.queryAPI.QueryRange(ctx, cpuQuery, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query CPU metrics: %w", err)
	}

	// Get Memory usage metrics
	memQuery := fmt.Sprintf(
		`container_memory_working_set_bytes{namespace="%s",pod="%s",container!="POD",container!=""}`,
		namespace, podName,
	)

	memResult, _, err := p.queryAPI.QueryRange(ctx, memQuery, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query memory metrics: %w", err)
	}

	// Convert results to our internal format
	cpuHistory := p.convertMatrixToUsageHistory(cpuResult, "cores")
	memHistory := p.convertMatrixToUsageHistory(memResult, "bytes")

	return &PodMetrics{
		PodName:         podName,
		Namespace:       namespace,
		CPUUsageHistory: cpuHistory,
		MemUsageHistory: memHistory,
		StartTime:       startTime,
		EndTime:         endTime,
	}, nil
}

// GetWorkloadMetrics retrieves aggregated metrics for a workload
func (p *PrometheusClient) GetWorkloadMetrics(ctx context.Context, namespace, workloadName, workloadType string, window time.Duration) (*WorkloadMetrics, error) {
	endTime := time.Now()
	startTime := endTime.Add(-window)

	// Build label selector based on workload type
	labelSelector := p.buildWorkloadSelector(workloadName, workloadType)

	// Get CPU usage metrics for all pods in the workload
	cpuQuery := fmt.Sprintf(
		`sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="%s",%s,container!="POD",container!=""}[5m]))`,
		namespace, labelSelector,
	)

	cpuResult, _, err := p.queryAPI.QueryRange(ctx, cpuQuery, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query workload CPU metrics: %w", err)
	}

	// Get Memory usage metrics for all pods in the workload
	memQuery := fmt.Sprintf(
		`sum by (pod) (container_memory_working_set_bytes{namespace="%s",%s,container!="POD",container!=""})`,
		namespace, labelSelector,
	)

	memResult, _, err := p.queryAPI.QueryRange(ctx, memQuery, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query workload memory metrics: %w", err)
	}

	// Convert to WorkloadMetrics format
	workloadMetrics := &WorkloadMetrics{
		WorkloadName: workloadName,
		WorkloadType: workloadType,
		Namespace:    namespace,
		StartTime:    startTime,
		EndTime:      endTime,
	}

	// Group metrics by pod
	podMetricsMap := make(map[string]*PodMetrics)

	// Process CPU metrics
	if matrix, ok := cpuResult.(model.Matrix); ok {
		for _, series := range matrix {
			podName := string(series.Metric["pod"])
			if podName == "" {
				continue
			}

			if _, exists := podMetricsMap[podName]; !exists {
				podMetricsMap[podName] = &PodMetrics{
					PodName:   podName,
					Namespace: namespace,
					StartTime: startTime,
					EndTime:   endTime,
				}
			}

			podMetricsMap[podName].CPUUsageHistory = p.convertSamplePairToUsageHistory(series.Values, "cores")
		}
	}

	// Process Memory metrics
	if matrix, ok := memResult.(model.Matrix); ok {
		for _, series := range matrix {
			podName := string(series.Metric["pod"])
			if podName == "" {
				continue
			}

			if _, exists := podMetricsMap[podName]; !exists {
				podMetricsMap[podName] = &PodMetrics{
					PodName:   podName,
					Namespace: namespace,
					StartTime: startTime,
					EndTime:   endTime,
				}
			}

			podMetricsMap[podName].MemUsageHistory = p.convertSamplePairToUsageHistory(series.Values, "bytes")
		}
	}

	// Convert map to slice
	for _, podMetrics := range podMetricsMap {
		workloadMetrics.Pods = append(workloadMetrics.Pods, *podMetrics)
	}

	return workloadMetrics, nil
}

// buildWorkloadSelector creates a Prometheus label selector for different workload types
func (p *PrometheusClient) buildWorkloadSelector(workloadName, workloadType string) string {
	switch workloadType {
	case "Deployment":
		return fmt.Sprintf(`deployment="%s"`, workloadName)
	case "StatefulSet":
		return fmt.Sprintf(`statefulset="%s"`, workloadName)
	case "DaemonSet":
		return fmt.Sprintf(`daemonset="%s"`, workloadName)
	case "Job":
		return fmt.Sprintf(`job_name="%s"`, workloadName)
	default:
		// Fallback to app label
		return fmt.Sprintf(`app="%s"`, workloadName)
	}
}

// convertMatrixToUsageHistory converts Prometheus matrix result to usage history
func (p *PrometheusClient) convertMatrixToUsageHistory(result model.Value, unit string) []ResourceUsage {
	var history []ResourceUsage

	if matrix, ok := result.(model.Matrix); ok {
		for _, series := range matrix {
			for _, value := range series.Values {
				history = append(history, ResourceUsage{
					Timestamp: value.Timestamp.Time(),
					Value:     float64(value.Value),
					Unit:      unit,
				})
			}
		}
	}

	return history
}

// convertSamplePairToUsageHistory converts sample pairs to usage history
func (p *PrometheusClient) convertSamplePairToUsageHistory(values []model.SamplePair, unit string) []ResourceUsage {
	var history []ResourceUsage

	for _, value := range values {
		history = append(history, ResourceUsage{
			Timestamp: value.Timestamp.Time(),
			Value:     float64(value.Value),
			Unit:      unit,
		})
	}

	return history
}

// MetricsServerClient implements MetricsClient interface for Kubernetes Metrics Server
type MetricsServerClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewMetricsServerClient creates a new Metrics Server client
func NewMetricsServerClient(baseURL string) *MetricsServerClient {
	return &MetricsServerClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
	}
}

// GetPodMetrics retrieves current metrics from Metrics Server (limited historical data)
func (m *MetricsServerClient) GetPodMetrics(ctx context.Context, namespace, podName string, window time.Duration) (*PodMetrics, error) {
	// Metrics Server only provides current metrics, not historical data
	// This is a limitation - for proper historical analysis, you need Prometheus
	
	endpoint := fmt.Sprintf("%s/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods/%s", m.baseURL, namespace, podName)
	
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics server returned status %d", resp.StatusCode)
	}

	var metricsResponse PodMetricsResponse
	if err := json.NewDecoder(resp.Body).Decode(&metricsResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to our internal format
	now := time.Now()
	podMetrics := &PodMetrics{
		PodName:   podName,
		Namespace: namespace,
		StartTime: now,
		EndTime:   now,
	}

	for _, container := range metricsResponse.Containers {
		// Parse CPU usage
		if cpuValue, err := parseQuantity(container.Usage.CPU); err == nil {
			podMetrics.CPUUsageHistory = append(podMetrics.CPUUsageHistory, ResourceUsage{
				Timestamp: now,
				Value:     cpuValue,
				Unit:      "cores",
			})
		}

		// Parse Memory usage
		if memValue, err := parseQuantity(container.Usage.Memory); err == nil {
			podMetrics.MemUsageHistory = append(podMetrics.MemUsageHistory, ResourceUsage{
				Timestamp: now,
				Value:     memValue,
				Unit:      "bytes",
			})
		}
	}

	return podMetrics, nil
}

// GetWorkloadMetrics retrieves current metrics for a workload from Metrics Server
func (m *MetricsServerClient) GetWorkloadMetrics(ctx context.Context, namespace, workloadName, workloadType string, window time.Duration) (*WorkloadMetrics, error) {
	// Get all pods in the namespace first
	endpoint := fmt.Sprintf("%s/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods", m.baseURL, namespace)
	
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics server returned status %d", resp.StatusCode)
	}

	var metricsResponse PodMetricsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&metricsResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Filter pods belonging to the workload
	now := time.Now()
	workloadMetrics := &WorkloadMetrics{
		WorkloadName: workloadName,
		WorkloadType: workloadType,
		Namespace:    namespace,
		StartTime:    now,
		EndTime:      now,
	}

	for _, podMetrics := range metricsResponse.Items {
		// Simple filtering - in production, you'd need more sophisticated matching
		if m.podBelongsToWorkload(podMetrics.Metadata.Name, workloadName, workloadType) {
			pod := PodMetrics{
				PodName:   podMetrics.Metadata.Name,
				Namespace: podMetrics.Metadata.Namespace,
				StartTime: now,
				EndTime:   now,
			}

			for _, container := range podMetrics.Containers {
				if cpuValue, err := parseQuantity(container.Usage.CPU); err == nil {
					pod.CPUUsageHistory = append(pod.CPUUsageHistory, ResourceUsage{
						Timestamp: now,
						Value:     cpuValue,
						Unit:      "cores",
					})
				}

				if memValue, err := parseQuantity(container.Usage.Memory); err == nil {
					pod.MemUsageHistory = append(pod.MemUsageHistory, ResourceUsage{
						Timestamp: now,
						Value:     memValue,
						Unit:      "bytes",
					})
				}
			}

			workloadMetrics.Pods = append(workloadMetrics.Pods, pod)
		}
	}

	return workloadMetrics, nil
}

// podBelongsToWorkload checks if a pod belongs to a specific workload
func (m *MetricsServerClient) podBelongsToWorkload(podName, workloadName, workloadType string) bool {
	// Simplified matching logic
	// In production, you'd need to query the Kubernetes API to get actual ownership
	switch workloadType {
	case "Deployment":
		// Deployment pods typically have names like: workloadname-replicaset-podid
		return len(podName) > len(workloadName) && podName[:len(workloadName)] == workloadName
	case "StatefulSet":
		// StatefulSet pods have predictable names: workloadname-0, workloadname-1, etc.
		return len(podName) > len(workloadName)+1 && podName[:len(workloadName)+1] == workloadName+"-"
	default:
		return false
	}
}

// parseQuantity parses Kubernetes resource quantity strings
func parseQuantity(quantity string) (float64, error) {
	// This is a simplified parser
	// In production, use k8s.io/apimachinery/pkg/api/resource.ParseQuantity
	
	if quantity == "" {
		return 0, fmt.Errorf("empty quantity")
	}

	// Handle common suffixes
	multiplier := 1.0
	value := quantity

	// Memory suffixes
	if len(quantity) > 2 {
		suffix := quantity[len(quantity)-2:]
		switch suffix {
		case "Ki":
			multiplier = 1024
			value = quantity[:len(quantity)-2]
		case "Mi":
			multiplier = 1024 * 1024
			value = quantity[:len(quantity)-2]
		case "Gi":
			multiplier = 1024 * 1024 * 1024
			value = quantity[:len(quantity)-2]
		}
	}

	// CPU suffixes
	if len(quantity) > 1 && quantity[len(quantity)-1:] == "m" {
		multiplier = 0.001 // millicores to cores
		value = quantity[:len(quantity)-1]
	}

	parsedValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse quantity %s: %w", quantity, err)
	}

	return parsedValue * multiplier, nil
}

// Response structures for Metrics Server API
type PodMetricsResponse struct {
	Metadata   MetricMetadata        `json:"metadata"`
	Timestamp  string                `json:"timestamp"`
	Window     string                `json:"window"`
	Containers []ContainerMetrics    `json:"containers"`
}

type PodMetricsListResponse struct {
	Items []PodMetricsResponse `json:"items"`
}

type MetricMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type ContainerMetrics struct {
	Name  string                 `json:"name"`
	Usage map[string]string      `json:"usage"`
}

// Shared types used by both clients
type PodMetrics struct {
	PodName         string
	Namespace       string
	CPUUsageHistory []ResourceUsage
	MemUsageHistory []ResourceUsage
	StartTime       time.Time
	EndTime         time.Time
}

type WorkloadMetrics struct {
	WorkloadName string
	WorkloadType string
	Namespace    string
	Pods         []PodMetrics
	StartTime    time.Time
	EndTime      time.Time
}

type ResourceUsage struct {
	Timestamp time.Time
	Value     float64
	Unit      string
}