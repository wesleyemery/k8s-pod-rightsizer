package metrics

import (
	"time"
)

// PodMetrics represents resource usage metrics for a pod
type PodMetrics struct {
	PodName         string
	Namespace       string
	CPUUsageHistory []ResourceUsage
	MemUsageHistory []ResourceUsage
	StartTime       time.Time
	EndTime         time.Time
}

// WorkloadMetrics represents aggregated metrics for a workload
type WorkloadMetrics struct {
	WorkloadName string
	WorkloadType string
	Namespace    string
	Pods         []PodMetrics
	StartTime    time.Time
	EndTime      time.Time
}

// ResourceUsage represents resource usage at a point in time
type ResourceUsage struct {
	Timestamp time.Time
	Value     float64
	Unit      string
}
