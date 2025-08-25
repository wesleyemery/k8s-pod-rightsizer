package metrics

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMockMetricsClient_GetPodMetrics(t *testing.T) {
	client := NewMockMetricsClient()
	ctx := context.Background()

	metrics, err := client.GetPodMetrics(ctx, "default", "test-pod", 1*time.Hour)

	assert.NoError(t, err)
	assert.NotNil(t, metrics)
	assert.Equal(t, "test-pod", metrics.PodName)
	assert.Equal(t, "default", metrics.Namespace)
	assert.NotEmpty(t, metrics.CPUUsageHistory)
	assert.NotEmpty(t, metrics.MemUsageHistory)
	assert.True(t, metrics.StartTime.Before(metrics.EndTime))

	// Verify we have reasonable number of data points
	assert.Greater(t, len(metrics.CPUUsageHistory), 10)
	assert.Greater(t, len(metrics.MemUsageHistory), 10)

	// Verify data point structure
	for _, usage := range metrics.CPUUsageHistory {
		assert.Greater(t, usage.Value, 0.0)
		assert.Equal(t, "cores", usage.Unit)
		assert.False(t, usage.Timestamp.IsZero())
	}

	for _, usage := range metrics.MemUsageHistory {
		assert.Greater(t, usage.Value, 0.0)
		assert.Equal(t, "bytes", usage.Unit)
		assert.False(t, usage.Timestamp.IsZero())
	}
}

func TestMockMetricsClient_GetWorkloadMetrics(t *testing.T) {
	client := NewMockMetricsClient()
	ctx := context.Background()

	workloadMetrics, err := client.GetWorkloadMetrics(ctx, "default", "test-deployment", "Deployment", 1*time.Hour)

	assert.NoError(t, err)
	assert.NotNil(t, workloadMetrics)
	assert.Equal(t, "test-deployment", workloadMetrics.WorkloadName)
	assert.Equal(t, "Deployment", workloadMetrics.WorkloadType)
	assert.Equal(t, "default", workloadMetrics.Namespace)
	assert.NotEmpty(t, workloadMetrics.Pods)
	assert.True(t, workloadMetrics.StartTime.Before(workloadMetrics.EndTime))

	// Verify pod metrics
	for _, podMetrics := range workloadMetrics.Pods {
		assert.NotEmpty(t, podMetrics.PodName)
		assert.Equal(t, "default", podMetrics.Namespace)
		assert.NotEmpty(t, podMetrics.CPUUsageHistory)
		assert.NotEmpty(t, podMetrics.MemUsageHistory)
	}
}

func TestNewMockMetricsClient(t *testing.T) {
	client := NewMockMetricsClient()

	assert.NotNil(t, client)
	assert.Equal(t, 0.05, client.BaseCPU)
	assert.Equal(t, 67108864.0, client.BaseMemory) // 64Mi bytes
	assert.Equal(t, 0.3, client.Variance)
}

func TestResourceUsage_Structure(t *testing.T) {
	now := time.Now()
	usage := ResourceUsage{
		Timestamp: now,
		Value:     0.15,
		Unit:      "cores",
	}

	assert.Equal(t, now, usage.Timestamp)
	assert.Equal(t, 0.15, usage.Value)
	assert.Equal(t, "cores", usage.Unit)
}

func TestPodMetrics_Structure(t *testing.T) {
	startTime := time.Now().Add(-1 * time.Hour)
	endTime := time.Now()

	podMetrics := PodMetrics{
		PodName:         "test-pod",
		Namespace:       "default",
		CPUUsageHistory: []ResourceUsage{},
		MemUsageHistory: []ResourceUsage{},
		StartTime:       startTime,
		EndTime:         endTime,
	}

	assert.Equal(t, "test-pod", podMetrics.PodName)
	assert.Equal(t, "default", podMetrics.Namespace)
	assert.Equal(t, startTime, podMetrics.StartTime)
	assert.Equal(t, endTime, podMetrics.EndTime)
	assert.Empty(t, podMetrics.CPUUsageHistory)
	assert.Empty(t, podMetrics.MemUsageHistory)
}

func TestWorkloadMetrics_Structure(t *testing.T) {
	startTime := time.Now().Add(-1 * time.Hour)
	endTime := time.Now()

	workloadMetrics := WorkloadMetrics{
		WorkloadName: "test-deployment",
		WorkloadType: "Deployment",
		Namespace:    "default",
		Pods:         []PodMetrics{},
		StartTime:    startTime,
		EndTime:      endTime,
	}

	assert.Equal(t, "test-deployment", workloadMetrics.WorkloadName)
	assert.Equal(t, "Deployment", workloadMetrics.WorkloadType)
	assert.Equal(t, "default", workloadMetrics.Namespace)
	assert.Equal(t, startTime, workloadMetrics.StartTime)
	assert.Equal(t, endTime, workloadMetrics.EndTime)
	assert.Empty(t, workloadMetrics.Pods)
}

func TestNewPrometheusClient(t *testing.T) {
	prometheusURL := "http://prometheus:9090"
	roundTripper := &http.Transport{}

	client, err := NewPrometheusClient(prometheusURL, roundTripper)

	assert.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewPrometheusClient_EmptyURL(t *testing.T) {
	roundTripper := &http.Transport{}

	client, err := NewPrometheusClient("", roundTripper)

	// The constructor doesn't validate empty URL, so it succeeds
	assert.NoError(t, err)
	assert.NotNil(t, client)
}
