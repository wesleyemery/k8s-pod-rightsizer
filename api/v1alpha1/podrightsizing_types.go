/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// PodRightSizingSpec defines the desired state of PodRightSizing.
type PodRightSizingSpec struct {
	// Target defines which pods to analyze and optimize
	Target TargetSpec `json:"target"`

	// AnalysisWindow defines how far back to look for metrics (e.g., "7d", "30d")
	// +kubebuilder:default="7d"
	AnalysisWindow string `json:"analysisWindow,omitempty"`

	// UpdatePolicy defines how updates should be applied
	UpdatePolicy UpdatePolicy `json:"updatePolicy,omitempty"`

	// Thresholds define the optimization parameters
	Thresholds ResourceThresholds `json:"thresholds,omitempty"`

	// MetricsSource defines where to collect metrics from
	MetricsSource MetricsSourceSpec `json:"metricsSource,omitempty"`

	// Schedule defines when to run analysis (cron format)
	// +kubebuilder:default="0 2 * * *"
	Schedule string `json:"schedule,omitempty"`

	// DryRun when true, only generates recommendations without applying changes
	// +kubebuilder:default=false
	DryRun bool `json:"dryRun,omitempty"`
}

// TargetSpec defines which pods to target for right-sizing.
type TargetSpec struct {
	// Namespace to look for pods in. If empty, uses all namespaces
	Namespace string `json:"namespace,omitempty"`

	// LabelSelector to filter pods
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// NamespaceSelector to select multiple namespaces
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ExcludeNamespaces lists namespaces to exclude
	ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

	// IncludeWorkloadTypes lists workload types to include (Deployment, StatefulSet, etc.)
	IncludeWorkloadTypes []string `json:"includeWorkloadTypes,omitempty"`
}

// UpdatePolicy defines how resource updates should be applied.
type UpdatePolicy struct {
	// Strategy defines the update strategy: "immediate", "gradual", or "manual"
	// +kubebuilder:default="gradual"
	Strategy UpdateStrategy `json:"strategy,omitempty"`

	// MaxUnavailable defines max pods that can be unavailable during updates
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

	// MaxSurge defines max pods that can be created above desired count
	MaxSurge *intstr.IntOrString `json:"maxSurge,omitempty"`

	// BackoffLimit defines max retries for failed updates
	// +kubebuilder:default=3
	BackoffLimit int32 `json:"backoffLimit,omitempty"`

	// MinStabilityPeriod defines minimum time to wait between updates
	// +kubebuilder:default="5m"
	MinStabilityPeriod string `json:"minStabilityPeriod,omitempty"`
}

// UpdateStrategy defines the strategy for applying updates
// +kubebuilder:validation:Enum=immediate;gradual;manual
type UpdateStrategy string

const (
	UpdateStrategyImmediate UpdateStrategy = "immediate"
	UpdateStrategyGradual   UpdateStrategy = "gradual"
	UpdateStrategyManual    UpdateStrategy = "manual"
)

// ResourceThresholds defines optimization parameters
type ResourceThresholds struct {
	// CPUUtilizationPercentile defines target CPU utilization percentile (e.g., 95)
	// +kubebuilder:default=95
	CPUUtilizationPercentile int `json:"cpuUtilizationPercentile,omitempty"`

	// MemoryUtilizationPercentile defines target memory utilization percentile
	// +kubebuilder:default=95
	MemoryUtilizationPercentile int `json:"memoryUtilizationPercentile,omitempty"`

	// MinCPU defines minimum CPU request
	MinCPU resource.Quantity `json:"minCpu,omitempty"`

	// MaxCPU defines maximum CPU request
	MaxCPU resource.Quantity `json:"maxCpu,omitempty"`

	// MinMemory defines minimum memory request
	MinMemory resource.Quantity `json:"minMemory,omitempty"`

	// MaxMemory defines maximum memory request
	MaxMemory resource.Quantity `json:"maxMemory,omitempty"`

	// SafetyMargin defines safety margin percentage for recommendations
	// +kubebuilder:default=20
	SafetyMargin int `json:"safetyMargin,omitempty"`

	// MinChangeThreshold defines minimum change required to trigger update (percentage)
	// +kubebuilder:default=10
	MinChangeThreshold int `json:"minChangeThreshold,omitempty"`
}

// MetricsSourceSpec defines where to collect metrics from
type MetricsSourceSpec struct {
	// Type defines the metrics source type: "prometheus", "metrics-server"
	// +kubebuilder:default="prometheus"
	Type MetricsSourceType `json:"type,omitempty"`

	// PrometheusConfig defines Prometheus-specific configuration
	PrometheusConfig *PrometheusConfig `json:"prometheusConfig,omitempty"`
}

// MetricsSourceType defines the type of metrics source
// +kubebuilder:validation:Enum=prometheus;metrics-server
type MetricsSourceType string

const (
	MetricsSourcePrometheus    MetricsSourceType = "prometheus"
	MetricsSourceMetricsServer MetricsSourceType = "metrics-server"
)

// PrometheusConfig defines Prometheus connection details
type PrometheusConfig struct {
	// URL is the Prometheus server URL
	URL string `json:"url,omitempty"`

	// InsecureSkipTLSVerify skips TLS verification
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// AuthConfig defines authentication configuration
	AuthConfig *AuthConfig `json:"authConfig,omitempty"`
}

// AuthConfig defines authentication configuration
type AuthConfig struct {
	// Type defines the auth type: "basic", "bearer", "none"
	Type AuthType `json:"type,omitempty"`

	// SecretRef references a secret containing auth credentials
	SecretRef *corev1.SecretReference `json:"secretRef,omitempty"`
}

// AuthType defines the authentication type
// +kubebuilder:validation:Enum=none;basic;bearer
type AuthType string

const (
	AuthTypeNone   AuthType = "none"
	AuthTypeBasic  AuthType = "basic"
	AuthTypeBearer AuthType = "bearer"
)

// PodRightSizingStatus defines the observed state of PodRightSizing
type PodRightSizingStatus struct {
	// Phase indicates the current phase of the right-sizing process
	Phase RightSizingPhase `json:"phase,omitempty"`

	// Message provides a human-readable status message
	Message string `json:"message,omitempty"`

	// LastAnalysisTime indicates when the last analysis was performed
	LastAnalysisTime *metav1.Time `json:"lastAnalysisTime,omitempty"`

	// LastUpdateTime indicates when resources were last updated
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

	// TargetedPods indicates the number of pods being managed
	TargetedPods int32 `json:"targetedPods,omitempty"`

	// UpdatedPods indicates the number of pods that have been updated
	UpdatedPods int32 `json:"updatedPods,omitempty"`

	// Recommendations contains the current recommendations
	Recommendations []PodRecommendation `json:"recommendations,omitempty"`

	// Conditions contains the current service state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RightSizingPhase defines the phase of the right-sizing process
// +kubebuilder:validation:Enum=Initializing;Analyzing;Recommending;Updating;Completed;Error
type RightSizingPhase string

const (
	PhaseInitializing RightSizingPhase = "Initializing"
	PhaseAnalyzing    RightSizingPhase = "Analyzing"
	PhaseRecommending RightSizingPhase = "Recommending"
	PhaseUpdating     RightSizingPhase = "Updating"
	PhaseCompleted    RightSizingPhase = "Completed"
	PhaseError        RightSizingPhase = "Error"
)

// PodRecommendation contains resource recommendations for a specific pod
type PodRecommendation struct {
	// PodReference identifies the target pod
	PodReference PodReference `json:"podReference"`

	// CurrentResources shows current resource requests/limits
	CurrentResources corev1.ResourceRequirements `json:"currentResources"`

	// RecommendedResources shows recommended resource requests/limits
	RecommendedResources corev1.ResourceRequirements `json:"recommendedResources"`

	// Reason explains why this recommendation was made
	Reason string `json:"reason,omitempty"`

	// Confidence indicates confidence level (0-100)
	Confidence int `json:"confidence,omitempty"`

	// PotentialSavings estimates cost/resource savings
	PotentialSavings ResourceSavings `json:"potentialSavings,omitempty"`

	// Applied indicates if this recommendation has been applied
	Applied bool `json:"applied,omitempty"`

	// AppliedTime indicates when this recommendation was applied
	AppliedTime *metav1.Time `json:"appliedTime,omitempty"`
}

// PodReference uniquely identifies a pod
type PodReference struct {
	// Name is the pod name
	Name string `json:"name"`

	// Namespace is the pod namespace
	Namespace string `json:"namespace"`

	// WorkloadType is the type of workload (Deployment, StatefulSet, etc.)
	WorkloadType string `json:"workloadType,omitempty"`

	// WorkloadName is the name of the parent workload
	WorkloadName string `json:"workloadName,omitempty"`
}

// ResourceSavings estimates potential savings from applying recommendations
type ResourceSavings struct {
	// CPUSavings estimates CPU savings (in cores)
	CPUSavings *resource.Quantity `json:"cpuSavings,omitempty"`

	// MemorySavings estimates memory savings (in bytes)
	MemorySavings *resource.Quantity `json:"memorySavings,omitempty"`

	// CostSavings estimates cost savings (in USD per month)
	CostSavings string `json:"costSavings,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Targeted",type="integer",JSONPath=".status.targetedPods"
//+kubebuilder:printcolumn:name="Updated",type="integer",JSONPath=".status.updatedPods"
//+kubebuilder:printcolumn:name="Last Analysis",type="date",JSONPath=".status.lastAnalysisTime"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PodRightSizing is the Schema for the podrightsizings API.
type PodRightSizing struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodRightSizingSpec   `json:"spec,omitempty"`
	Status PodRightSizingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PodRightSizingList contains a list of PodRightSizing.
type PodRightSizingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodRightSizing `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PodRightSizing{}, &PodRightSizingList{})
}
