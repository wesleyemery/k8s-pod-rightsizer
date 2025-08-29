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
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodRightSizing_ValidatePodRightSizing(t *testing.T) {
	tests := []struct {
		name      string
		spec      PodRightSizingSpec
		wantError bool
	}{
		{
			name: "valid configuration with namespace target",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
				},
				Thresholds: ResourceThresholds{
					CPUUtilizationPercentile:    95,
					MemoryUtilizationPercentile: 95,
					SafetyMargin:                20,
					MinChangeThreshold:          10,
				},
				AnalysisWindow: "168h", // 7 days in hours
				Schedule:       "0 2 * * *",
				UpdatePolicy: UpdatePolicy{
					Strategy: UpdateStrategyGradual,
				},
				MetricsSource: MetricsSourceSpec{
					Type: MetricsSourcePrometheus,
					PrometheusConfig: &PrometheusConfig{
						URL: "http://prometheus.monitoring.svc.cluster.local:9090",
					},
				},
			},
			wantError: false,
		},
		{
			name: "invalid - no target specified",
			spec: PodRightSizingSpec{
				Target: TargetSpec{}, // No targeting method
			},
			wantError: true,
		},
		{
			name: "invalid - percentile out of range",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
				},
				Thresholds: ResourceThresholds{
					CPUUtilizationPercentile: 150, // Invalid - over 100
				},
			},
			wantError: true,
		},
		{
			name: "invalid - minCPU greater than maxCPU",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
				},
				Thresholds: ResourceThresholds{
					MinCPU: resource.MustParse("2"),
					MaxCPU: resource.MustParse("1"),
				},
			},
			wantError: true,
		},
		{
			name: "invalid - bad cron schedule",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
				},
				Schedule: "invalid cron",
			},
			wantError: true,
		},
		{
			name: "invalid - prometheus without URL",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
				},
				MetricsSource: MetricsSourceSpec{
					Type:             MetricsSourcePrometheus,
					PrometheusConfig: &PrometheusConfig{}, // No URL
				},
			},
			wantError: true,
		},
		{
			name: "invalid - both namespace and namespaceSelector",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"env": "prod"},
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid - invalid workload type",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace:            "test-namespace",
					IncludeWorkloadTypes: []string{"InvalidWorkloadType"},
				},
			},
			wantError: true,
		},
		{
			name: "invalid - analysis window too short",
			spec: PodRightSizingSpec{
				Target: TargetSpec{
					Namespace: "test-namespace",
				},
				AnalysisWindow: "30m", // Less than 1 hour
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prs := &PodRightSizing{
				Spec: tt.spec,
			}

			err := prs.ValidatePodRightSizing()
			if (err != nil) != tt.wantError {
				t.Errorf("ValidatePodRightSizing() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestPodRightSizing_validateTarget(t *testing.T) {
	tests := []struct {
		name      string
		target    TargetSpec
		wantError bool
	}{
		{
			name: "valid - namespace only",
			target: TargetSpec{
				Namespace: "test-namespace",
			},
			wantError: false,
		},
		{
			name: "valid - label selector only",
			target: TargetSpec{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
			},
			wantError: false,
		},
		{
			name: "valid - namespace selector only",
			target: TargetSpec{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
			wantError: false,
		},
		{
			name: "valid - workload types",
			target: TargetSpec{
				Namespace:            "test-namespace",
				IncludeWorkloadTypes: []string{"Deployment", "StatefulSet"},
			},
			wantError: false,
		},
		{
			name:      "invalid - no targeting method",
			target:    TargetSpec{},
			wantError: true,
		},
		{
			name: "invalid - both namespace and namespaceSelector",
			target: TargetSpec{
				Namespace: "test-namespace",
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
			wantError: true,
		},
		{
			name: "invalid - invalid workload type",
			target: TargetSpec{
				Namespace:            "test-namespace",
				IncludeWorkloadTypes: []string{"InvalidType"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prs := &PodRightSizing{
				Spec: PodRightSizingSpec{
					Target: tt.target,
				},
			}

			errs := prs.validateTarget()
			hasError := len(errs) > 0
			if hasError != tt.wantError {
				t.Errorf("validateTarget() error = %v, wantError %v", errs, tt.wantError)
			}
		})
	}
}
