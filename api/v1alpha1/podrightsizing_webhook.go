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
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var podrightsizinglog = logf.Log.WithName("podrightsizing-webhook")

func (r *PodRightSizing) SetupWebhookWithManager(mgr ctrl.Manager) error {
	// This would register webhook, but we'll skip for now to avoid complexity
	podrightsizinglog.Info("Webhook registration skipped - validation implemented as library functions")
	return nil
}

// ValidatePodRightSizing performs comprehensive validation of the PodRightSizing resource
// This can be called from controllers or tests
func (r *PodRightSizing) ValidatePodRightSizing() error {
	var allErrs field.ErrorList

	// Validate target configuration
	if errs := r.validateTarget(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Validate thresholds
	if errs := r.validateThresholds(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Validate analysis window
	if errs := r.validateAnalysisWindow(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Validate schedule
	if errs := r.validateSchedule(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Validate update policy
	if errs := r.validateUpdatePolicy(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Validate metrics source
	if errs := r.validateMetricsSource(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if len(allErrs) == 0 {
		return nil
	}

	return fmt.Errorf("validation errors: %v", allErrs.ToAggregate())
}

// validateTarget validates the target specification
func (r *PodRightSizing) validateTarget() field.ErrorList {
	var allErrs field.ErrorList
	targetPath := field.NewPath("spec").Child("target")

	// Must have at least one targeting method
	if r.Spec.Target.Namespace == "" &&
		r.Spec.Target.LabelSelector == nil &&
		r.Spec.Target.NamespaceSelector == nil {
		allErrs = append(allErrs, field.Required(targetPath,
			"must specify at least one of: namespace, labelSelector, or namespaceSelector"))
	}

	// Validate that namespace and namespaceSelector are not both specified
	if r.Spec.Target.Namespace != "" && r.Spec.Target.NamespaceSelector != nil {
		allErrs = append(allErrs, field.Invalid(targetPath.Child("namespace"), r.Spec.Target.Namespace,
			"cannot specify both namespace and namespaceSelector"))
	}

	// Validate workload types if specified
	if len(r.Spec.Target.IncludeWorkloadTypes) > 0 {
		validWorkloadTypes := map[string]bool{
			"Deployment":  true,
			"StatefulSet": true,
			"DaemonSet":   true,
			"Job":         true,
			"CronJob":     true,
		}

		for i, workloadType := range r.Spec.Target.IncludeWorkloadTypes {
			if !validWorkloadTypes[workloadType] {
				allErrs = append(allErrs, field.Invalid(
					targetPath.Child("includeWorkloadTypes").Index(i),
					workloadType,
					"must be one of: Deployment, StatefulSet, DaemonSet, Job, CronJob"))
			}
		}
	}

	return allErrs
}

// validateThresholds validates resource thresholds
func (r *PodRightSizing) validateThresholds() field.ErrorList {
	var allErrs field.ErrorList
	thresholdsPath := field.NewPath("spec").Child("thresholds")

	// Validate percentiles are in valid range
	if r.Spec.Thresholds.CPUUtilizationPercentile < 0 || r.Spec.Thresholds.CPUUtilizationPercentile > 100 {
		allErrs = append(allErrs, field.Invalid(
			thresholdsPath.Child("cpuUtilizationPercentile"),
			r.Spec.Thresholds.CPUUtilizationPercentile,
			"must be between 0 and 100"))
	}

	if r.Spec.Thresholds.MemoryUtilizationPercentile < 0 || r.Spec.Thresholds.MemoryUtilizationPercentile > 100 {
		allErrs = append(allErrs, field.Invalid(
			thresholdsPath.Child("memoryUtilizationPercentile"),
			r.Spec.Thresholds.MemoryUtilizationPercentile,
			"must be between 0 and 100"))
	}

	// Validate safety margin is reasonable
	if r.Spec.Thresholds.SafetyMargin < 0 || r.Spec.Thresholds.SafetyMargin > 1000 {
		allErrs = append(allErrs, field.Invalid(
			thresholdsPath.Child("safetyMargin"),
			r.Spec.Thresholds.SafetyMargin,
			"must be between 0 and 1000 (percentage)"))
	}

	// Validate change threshold
	if r.Spec.Thresholds.MinChangeThreshold < 0 || r.Spec.Thresholds.MinChangeThreshold > 100 {
		allErrs = append(allErrs, field.Invalid(
			thresholdsPath.Child("minChangeThreshold"),
			r.Spec.Thresholds.MinChangeThreshold,
			"must be between 0 and 100 (percentage)"))
	}

	// Validate min/max resource constraints are logical
	if !r.Spec.Thresholds.MinCPU.IsZero() && !r.Spec.Thresholds.MaxCPU.IsZero() {
		if r.Spec.Thresholds.MinCPU.Cmp(r.Spec.Thresholds.MaxCPU) > 0 {
			allErrs = append(allErrs, field.Invalid(
				thresholdsPath.Child("minCpu"),
				r.Spec.Thresholds.MinCPU.String(),
				"minCpu cannot be greater than maxCpu"))
		}
	}

	if !r.Spec.Thresholds.MinMemory.IsZero() && !r.Spec.Thresholds.MaxMemory.IsZero() {
		if r.Spec.Thresholds.MinMemory.Cmp(r.Spec.Thresholds.MaxMemory) > 0 {
			allErrs = append(allErrs, field.Invalid(
				thresholdsPath.Child("minMemory"),
				r.Spec.Thresholds.MinMemory.String(),
				"minMemory cannot be greater than maxMemory"))
		}
	}

	return allErrs
}

// validateAnalysisWindow validates the analysis window format and duration
func (r *PodRightSizing) validateAnalysisWindow() field.ErrorList {
	var allErrs field.ErrorList
	windowPath := field.NewPath("spec").Child("analysisWindow")

	if r.Spec.AnalysisWindow == "" {
		return allErrs
	}

	duration, err := time.ParseDuration(r.Spec.AnalysisWindow)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(windowPath, r.Spec.AnalysisWindow,
			fmt.Sprintf("invalid duration format: %v", err)))
		return allErrs
	}

	// Validate reasonable bounds
	if duration < time.Hour {
		allErrs = append(allErrs, field.Invalid(windowPath, r.Spec.AnalysisWindow,
			"analysis window must be at least 1 hour"))
	}

	if duration > 90*24*time.Hour {
		allErrs = append(allErrs, field.Invalid(windowPath, r.Spec.AnalysisWindow,
			"analysis window must not exceed 90 days"))
	}

	return allErrs
}

// validateSchedule validates the cron schedule format
func (r *PodRightSizing) validateSchedule() field.ErrorList {
	var allErrs field.ErrorList
	schedulePath := field.NewPath("spec").Child("schedule")

	if r.Spec.Schedule == "" {
		return allErrs
	}

	_, err := cron.ParseStandard(r.Spec.Schedule)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(schedulePath, r.Spec.Schedule,
			fmt.Sprintf("invalid cron expression: %v", err)))
	}

	return allErrs
}

// validateUpdatePolicy validates the update policy configuration
func (r *PodRightSizing) validateUpdatePolicy() field.ErrorList {
	var allErrs field.ErrorList
	policyPath := field.NewPath("spec").Child("updatePolicy")

	// Validate strategy
	validStrategies := map[UpdateStrategy]bool{
		UpdateStrategyImmediate: true,
		UpdateStrategyGradual:   true,
		UpdateStrategyManual:    true,
	}

	if r.Spec.UpdatePolicy.Strategy != "" && !validStrategies[r.Spec.UpdatePolicy.Strategy] {
		allErrs = append(allErrs, field.Invalid(
			policyPath.Child("strategy"),
			r.Spec.UpdatePolicy.Strategy,
			"must be one of: immediate, gradual, manual"))
	}

	// Validate backoff limit
	if r.Spec.UpdatePolicy.BackoffLimit < 0 {
		allErrs = append(allErrs, field.Invalid(
			policyPath.Child("backoffLimit"),
			r.Spec.UpdatePolicy.BackoffLimit,
			"must be non-negative"))
	}

	// Validate stability period
	if r.Spec.UpdatePolicy.MinStabilityPeriod != "" {
		if _, err := time.ParseDuration(r.Spec.UpdatePolicy.MinStabilityPeriod); err != nil {
			allErrs = append(allErrs, field.Invalid(
				policyPath.Child("minStabilityPeriod"),
				r.Spec.UpdatePolicy.MinStabilityPeriod,
				fmt.Sprintf("invalid duration format: %v", err)))
		}
	}

	return allErrs
}

// validateMetricsSource validates the metrics source configuration
func (r *PodRightSizing) validateMetricsSource() field.ErrorList {
	var allErrs field.ErrorList
	sourcePath := field.NewPath("spec").Child("metricsSource")

	// Validate metrics source type
	validTypes := map[MetricsSourceType]bool{
		MetricsSourcePrometheus:    true,
		MetricsSourceMetricsServer: true,
	}

	if r.Spec.MetricsSource.Type != "" && !validTypes[r.Spec.MetricsSource.Type] {
		allErrs = append(allErrs, field.Invalid(
			sourcePath.Child("type"),
			r.Spec.MetricsSource.Type,
			"must be one of: prometheus, metrics-server"))
	}

	// Validate Prometheus configuration if specified
	if r.Spec.MetricsSource.Type == MetricsSourcePrometheus && r.Spec.MetricsSource.PrometheusConfig != nil {
		prometheusPath := sourcePath.Child("prometheusConfig")

		// URL is required for Prometheus
		if r.Spec.MetricsSource.PrometheusConfig.URL == "" {
			allErrs = append(allErrs, field.Required(prometheusPath.Child("url"),
				"Prometheus URL is required when using prometheus metrics source"))
		}

		// Validate auth configuration
		if r.Spec.MetricsSource.PrometheusConfig.AuthConfig != nil {
			authPath := prometheusPath.Child("authConfig")

			validAuthTypes := map[AuthType]bool{
				AuthTypeNone:   true,
				AuthTypeBasic:  true,
				AuthTypeBearer: true,
			}

			if !validAuthTypes[r.Spec.MetricsSource.PrometheusConfig.AuthConfig.Type] {
				allErrs = append(allErrs, field.Invalid(
					authPath.Child("type"),
					r.Spec.MetricsSource.PrometheusConfig.AuthConfig.Type,
					"must be one of: none, basic, bearer"))
			}

			if r.Spec.MetricsSource.PrometheusConfig.AuthConfig.Type != AuthTypeNone &&
				r.Spec.MetricsSource.PrometheusConfig.AuthConfig.SecretRef == nil {
				allErrs = append(allErrs, field.Required(authPath.Child("secretRef"),
					"secretRef is required when using basic or bearer authentication"))
			}
		}
	}

	return allErrs
}
