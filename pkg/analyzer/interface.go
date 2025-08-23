package analyzer

import (
	"context"
	"time"

	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/metrics"
)

// MetricsClientInterface defines the interface for metrics clients
type MetricsClientInterface interface {
	GetPodMetrics(ctx context.Context, namespace, podName string, window time.Duration) (*metrics.PodMetrics, error)
	GetWorkloadMetrics(ctx context.Context, namespace, workloadName, workloadType string, window time.Duration) (*metrics.WorkloadMetrics, error)
}
