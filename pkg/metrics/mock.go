package metrics

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

const (
	defaultBaseCPU                 = 0.05     // 50m cores
	defaultBaseMemory              = 67108864 // 64Mi bytes
	defaultVariance                = 0.3      // 30% variance
	varianceOffset                 = 0.5
	varianceMultiplier             = 2
	minDataPointsForClassification = 20
)

// MockMetricsClient provides fake metrics for testing
type MockMetricsClient struct {
	// Configuration for generating fake data
	BaseCPU    float64
	BaseMemory float64
	Variance   float64
}

// NewMockMetricsClient creates a mock metrics client for testing
func NewMockMetricsClient() *MockMetricsClient {
	return &MockMetricsClient{
		BaseCPU:    defaultBaseCPU,
		BaseMemory: defaultBaseMemory,
		Variance:   defaultVariance,
	}
}

// GetPodMetrics generates fake pod metrics for testing
func (m *MockMetricsClient) GetPodMetrics(
	_ context.Context,
	namespace, podName string,
	window time.Duration,
) (*PodMetrics, error) {
	now := time.Now()
	start := now.Add(-window)

	// Generate sample data points (one per 5 minutes)
	interval := 5 * time.Minute
	dataPoints := int(window / interval)

	if dataPoints == 0 {
		dataPoints = 1
	}

	var cpuHistory, memHistory []ResourceUsage

	for i := 0; i < dataPoints; i++ {
		timestamp := start.Add(time.Duration(i) * interval)

		// Generate CPU usage with some variance
		cpuVariance := (rand.Float64() - varianceOffset) * varianceMultiplier * m.Variance
		cpuValue := m.BaseCPU * (1 + cpuVariance)
		if cpuValue < 0 {
			cpuValue = 0.001
		}

		// Generate memory usage with some variance
		memVariance := (rand.Float64() - varianceOffset) * varianceMultiplier * m.Variance
		memValue := m.BaseMemory * (1 + memVariance)
		if memValue < 0 {
			memValue = 1024
		}

		cpuHistory = append(cpuHistory, ResourceUsage{
			Timestamp: timestamp,
			Value:     cpuValue,
			Unit:      "cores",
		})

		memHistory = append(memHistory, ResourceUsage{
			Timestamp: timestamp,
			Value:     memValue,
			Unit:      "bytes",
		})
	}

	return &PodMetrics{
		PodName:         podName,
		Namespace:       namespace,
		CPUUsageHistory: cpuHistory,
		MemUsageHistory: memHistory,
		StartTime:       start,
		EndTime:         now,
	}, nil
}

// GetWorkloadMetrics generates fake workload metrics for testing
func (m *MockMetricsClient) GetWorkloadMetrics(
	ctx context.Context,
	namespace, workloadName, workloadType string,
	window time.Duration,
) (*WorkloadMetrics, error) {
	// Simulate multiple pods in the workload
	podCount := 3
	var pods []PodMetrics

	for i := 0; i < podCount; i++ {
		podName := fmt.Sprintf("%s-%s-%d", workloadName, generateRandomSuffix(), i)
		podMetrics, err := m.GetPodMetrics(ctx, namespace, podName, window)
		if err != nil {
			continue
		}
		pods = append(pods, *podMetrics)
	}

	return &WorkloadMetrics{
		WorkloadName: workloadName,
		WorkloadType: workloadType,
		Namespace:    namespace,
		Pods:         pods,
		StartTime:    time.Now().Add(-window),
		EndTime:      time.Now(),
	}, nil
}

// generateRandomSuffix generates a random suffix like Kubernetes does
func generateRandomSuffix() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 5)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
