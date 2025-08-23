package metrics

import (
	"context"
	"fmt"
	"net/http"
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

// Helper methods
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
		return fmt.Sprintf(`app="%s"`, workloadName)
	}
}

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

// MetricsServerClient for fallback functionality
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

// GetPodMetrics retrieves current metrics from Metrics Server
func (m *MetricsServerClient) GetPodMetrics(ctx context.Context, namespace, podName string, window time.Duration) (*PodMetrics, error) {
	// This is a simplified implementation for testing
	// In production, you'd query the actual metrics server API
	return &PodMetrics{
		PodName:   podName,
		Namespace: namespace,
		CPUUsageHistory: []ResourceUsage{
			{Timestamp: time.Now(), Value: 0.05, Unit: "cores"}, // 50m CPU
		},
		MemUsageHistory: []ResourceUsage{
			{Timestamp: time.Now(), Value: 67108864, Unit: "bytes"}, // 64Mi memory
		},
		StartTime: time.Now().Add(-window),
		EndTime:   time.Now(),
	}, nil
}

// GetWorkloadMetrics retrieves current metrics for a workload from Metrics Server
func (m *MetricsServerClient) GetWorkloadMetrics(ctx context.Context, namespace, workloadName, workloadType string, window time.Duration) (*WorkloadMetrics, error) {
	// This is a simplified implementation for testing
	return &WorkloadMetrics{
		WorkloadName: workloadName,
		WorkloadType: workloadType,
		Namespace:    namespace,
		Pods: []PodMetrics{
			{
				PodName:   workloadName + "-sample-pod",
				Namespace: namespace,
				CPUUsageHistory: []ResourceUsage{
					{Timestamp: time.Now(), Value: 0.05, Unit: "cores"},
				},
				MemUsageHistory: []ResourceUsage{
					{Timestamp: time.Now(), Value: 67108864, Unit: "bytes"},
				},
			},
		},
		StartTime: time.Now().Add(-window),
		EndTime:   time.Now(),
	}, nil
}

// Simple implementation of quantity parsing
func parseQuantity(quantity string) (float64, error) {
	if quantity == "" {
		return 0, fmt.Errorf("empty quantity")
	}

	multiplier := 1.0
	value := quantity

	// Handle common suffixes
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

	if len(quantity) > 1 && quantity[len(quantity)-1:] == "m" {
		multiplier = 0.001
		value = quantity[:len(quantity)-1]
	}

	parsedValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse quantity %s: %w", quantity, err)
	}

	return parsedValue * multiplier, nil
}
