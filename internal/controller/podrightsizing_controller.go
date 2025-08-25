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

package controller

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rightsizingv1alpha1 "github.com/wesleyemery/k8s-pod-rightsizer/api/v1alpha1"
	"github.com/wesleyemery/k8s-pod-rightsizer/pkg/analyzer"
)

const (
	WorkloadTypeDeployment  = "Deployment"
	WorkloadTypeStatefulSet = "StatefulSet"
	WorkloadTypeDaemonSet   = "DaemonSet"
)

// PodRightSizingReconciler reconciles a PodRightSizing object
type PodRightSizingReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	MetricsClient   analyzer.MetricsClientInterface // Use interface
	RecommendEngine *analyzer.RecommendationEngine
}

//+kubebuilder:rbac:groups=rightsizing.k8s-rightsizer.io,resources=podrightsizings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rightsizing.k8s-rightsizer.io,resources=podrightsizings/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=rightsizing.k8s-rightsizer.io,resources=podrightsizings/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups="apps",resources=deployments;statefulsets;daemonsets;replicasets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=events,verbs=create

// Reconcile handles PodRightSizing custom resources
func (r *PodRightSizingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting reconciliation", "podrightsizing", req.NamespacedName)

	// Fetch the PodRightSizing instance
	var podRightSizing rightsizingv1alpha1.PodRightSizing
	if err := r.Get(ctx, req.NamespacedName, &podRightSizing); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("PodRightSizing resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get PodRightSizing")
		return ctrl.Result{}, err
	}

	// Check if this is a scheduled run
	if !r.shouldRunAnalysis(&podRightSizing) {
		logger.Info("Skipping analysis - not scheduled to run yet")
		return r.requeueAfter(&podRightSizing), nil
	}

	// Update phase to analyzing
	if err := r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseAnalyzing, "Starting resource analysis"); err != nil {
		return ctrl.Result{}, err
	}

	// Discover target pods
	targetPods, err := r.discoverTargetPods(ctx, &podRightSizing)
	if err != nil {
		logger.Error(err, "Failed to discover target pods")
		if updateErr := r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseError, fmt.Sprintf("Failed to discover pods: %v", err)); updateErr != nil {
			logger.Error(updateErr, "Failed to update phase to error")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, err
	}

	logger.Info("Discovered target pods", "count", len(targetPods))
	if len(targetPods) > math.MaxInt32 {
		return ctrl.Result{}, fmt.Errorf("too many target pods: %d exceeds int32 limit", len(targetPods))
	}
	podRightSizing.Status.TargetedPods = int32(len(targetPods))

	if len(targetPods) == 0 {
		logger.Info("No pods found matching criteria")
		if err := r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseCompleted, "No matching pods found"); err != nil {
			return ctrl.Result{}, err
		}
		return r.requeueAfter(&podRightSizing), nil
	}

	// Group pods by workload
	workloadGroups := r.groupPodsByWorkload(targetPods)

	// Update phase to recommending
	if err := r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseRecommending, "Generating recommendations"); err != nil {
		return ctrl.Result{}, err
	}

	// Generate recommendations for each workload
	var allRecommendations []rightsizingv1alpha1.PodRecommendation

	for workloadKey, pods := range workloadGroups {
		logger.Info("Processing workload", "workload", workloadKey, "pods", len(pods))

		recommendations, err := r.generateWorkloadRecommendations(ctx, &podRightSizing, workloadKey, pods)
		if err != nil {
			logger.Error(err, "Failed to generate recommendations", "workload", workloadKey)
			continue
		}

		allRecommendations = append(allRecommendations, recommendations...)
	}

	// Update recommendations in status
	podRightSizing.Status.Recommendations = allRecommendations
	podRightSizing.Status.LastAnalysisTime = &metav1.Time{Time: time.Now()}

	// Apply recommendations if not in dry-run mode
	if !podRightSizing.Spec.DryRun {
		if err := r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseUpdating, "Applying recommendations"); err != nil {
			return ctrl.Result{}, err
		}

		updatedCount := r.applyRecommendations(ctx, &podRightSizing, allRecommendations)

		if updatedCount > math.MaxInt32 {
			return ctrl.Result{}, fmt.Errorf("too many updated pods: %d exceeds int32 limit", updatedCount)
		}
		podRightSizing.Status.UpdatedPods = int32(updatedCount)
		podRightSizing.Status.LastUpdateTime = &metav1.Time{Time: time.Now()}
	}

	// Update final status
	phase := rightsizingv1alpha1.PhaseCompleted
	message := fmt.Sprintf("Analysis completed. Found %d recommendations", len(allRecommendations))
	if podRightSizing.Spec.DryRun {
		message += " (dry-run mode)"
	}

	if err := r.updatePhase(ctx, &podRightSizing, phase, message); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Reconciliation completed successfully",
		"recommendations", len(allRecommendations),
		"updated", podRightSizing.Status.UpdatedPods)

	return r.requeueAfter(&podRightSizing), nil
}

// shouldRunAnalysis determines if analysis should run based on schedule
func (r *PodRightSizingReconciler) shouldRunAnalysis(prs *rightsizingv1alpha1.PodRightSizing) bool {
	// For now, simple time-based logic
	// In a production implementation, you'd want proper cron parsing
	if prs.Status.LastAnalysisTime == nil {
		return true
	}

	// Parse analysis window or default to 24 hours
	interval := 24 * time.Hour
	if prs.Spec.AnalysisWindow != "" {
		if d, err := time.ParseDuration(prs.Spec.AnalysisWindow); err == nil {
			// For testing, run analysis every hour if window is less than 1 day
			if d < 24*time.Hour {
				interval = time.Hour
			} else {
				interval = d / 24 // Run daily for longer windows
			}
		}
	}

	return time.Since(prs.Status.LastAnalysisTime.Time) >= interval
}

// requeueAfter calculates when to requeue based on schedule
func (r *PodRightSizingReconciler) requeueAfter(prs *rightsizingv1alpha1.PodRightSizing) ctrl.Result {
	// Simple implementation - requeue every hour for testing, daily for production
	interval := time.Hour

	if prs.Spec.AnalysisWindow != "" {
		if d, err := time.ParseDuration(prs.Spec.AnalysisWindow); err == nil && d >= 24*time.Hour {
			interval = 24 * time.Hour
		}
	}

	return ctrl.Result{RequeueAfter: interval}
}

// discoverTargetPods finds pods matching the target criteria
func (r *PodRightSizingReconciler) discoverTargetPods(ctx context.Context, prs *rightsizingv1alpha1.PodRightSizing) ([]corev1.Pod, error) {
	var pods []corev1.Pod

	// Build label selector
	selector := labels.Everything()
	if prs.Spec.Target.LabelSelector != nil {
		var err error
		selector, err = metav1.LabelSelectorAsSelector(prs.Spec.Target.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid label selector: %w", err)
		}
	}

	// Determine target namespaces
	var namespaces []string

	if prs.Spec.Target.NamespaceSelector != nil {
		// Use namespace selector
		namespaceSelector, err := metav1.LabelSelectorAsSelector(prs.Spec.Target.NamespaceSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid namespace selector: %w", err)
		}

		var namespaceList corev1.NamespaceList
		if err := r.List(ctx, &namespaceList, client.MatchingLabelsSelector{Selector: namespaceSelector}); err != nil {
			return nil, fmt.Errorf("failed to list namespaces: %w", err)
		}

		for _, ns := range namespaceList.Items {
			namespaces = append(namespaces, ns.Name)
		}
	} else if prs.Spec.Target.Namespace != "" {
		// Use specific namespace
		namespaces = []string{prs.Spec.Target.Namespace}
	} else {
		// Use all namespaces
		namespaces = []string{""}
	}

	// List pods in target namespace(s)
	for _, ns := range namespaces {
		// Skip excluded namespaces
		if r.isNamespaceExcluded(ns, prs.Spec.Target.ExcludeNamespaces) {
			continue
		}

		var podList corev1.PodList
		listOpts := []client.ListOption{
			client.MatchingLabelsSelector{Selector: selector},
		}
		if ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}

		if err := r.List(ctx, &podList, listOpts...); err != nil {
			return nil, fmt.Errorf("failed to list pods in namespace %s: %w", ns, err)
		}

		// Filter pods
		for _, pod := range podList.Items {
			if r.shouldIncludePod(&pod, prs) {
				pods = append(pods, pod)
			}
		}
	}

	return pods, nil
}

// isNamespaceExcluded checks if a namespace should be excluded
func (r *PodRightSizingReconciler) isNamespaceExcluded(namespace string, excludeList []string) bool {
	for _, excludeNs := range excludeList {
		if namespace == excludeNs {
			return true
		}
	}
	return false
}

// shouldIncludePod determines if a pod should be included for analysis
func (r *PodRightSizingReconciler) shouldIncludePod(pod *corev1.Pod, prs *rightsizingv1alpha1.PodRightSizing) bool {
	// Skip pods that are not running
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	// Skip pods without owner references (standalone pods)
	if len(pod.OwnerReferences) == 0 {
		return false
	}

	// Check if pod belongs to supported workload types
	if len(prs.Spec.Target.IncludeWorkloadTypes) > 0 {
		workloadType := r.getWorkloadType(pod)
		found := false
		for _, allowedType := range prs.Spec.Target.IncludeWorkloadTypes {
			if workloadType == allowedType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Skip if pod doesn't have resource requests/limits (nothing to optimize)
	hasResources := false
	for _, container := range pod.Spec.Containers {
		if len(container.Resources.Requests) > 0 || len(container.Resources.Limits) > 0 {
			hasResources = true
			break
		}
	}

	return hasResources
}

// getWorkloadType determines the workload type of a pod
func (r *PodRightSizingReconciler) getWorkloadType(pod *corev1.Pod) string {
	for _, owner := range pod.OwnerReferences {
		switch owner.Kind {
		case "ReplicaSet":
			return WorkloadTypeDeployment
		case "StatefulSet":
			return WorkloadTypeStatefulSet
		case "DaemonSet":
			return WorkloadTypeDaemonSet
		case "Job":
			return "Job"
		case "CronJob":
			return "CronJob"
		}
	}
	return "Pod"
}

// groupPodsByWorkload groups pods by their parent workload
func (r *PodRightSizingReconciler) groupPodsByWorkload(pods []corev1.Pod) map[string][]corev1.Pod {
	groups := make(map[string][]corev1.Pod)

	for _, pod := range pods {
		workloadName := r.getWorkloadName(context.Background(), &pod)
		workloadType := r.getWorkloadType(&pod)
		key := fmt.Sprintf("%s/%s/%s", pod.Namespace, workloadType, workloadName)
		groups[key] = append(groups[key], pod)
	}

	return groups
}

// getWorkloadName gets the name of the parent workload
func (r *PodRightSizingReconciler) getWorkloadName(ctx context.Context, pod *corev1.Pod) string {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			// For Deployments, we need to get the Deployment name from ReplicaSet
			var rs appsv1.ReplicaSet
			if err := r.Get(ctx, types.NamespacedName{
				Name:      owner.Name,
				Namespace: pod.Namespace,
			}, &rs); err == nil {
				for _, rsOwner := range rs.OwnerReferences {
					if rsOwner.Kind == "Deployment" {
						return rsOwner.Name
					}
				}
			}
		} else {
			// For other workload types, return the owner name directly
			return owner.Name
		}
	}
	return pod.Name
}

// generateWorkloadRecommendations generates recommendations for a workload
func (r *PodRightSizingReconciler) generateWorkloadRecommendations(
	ctx context.Context,
	prs *rightsizingv1alpha1.PodRightSizing,
	workloadKey string,
	pods []corev1.Pod,
) ([]rightsizingv1alpha1.PodRecommendation, error) {

	logger := log.FromContext(ctx)

	// Parse workload key
	parts := r.splitWorkloadKey(workloadKey)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid workload key: %s", workloadKey)
	}

	namespace, workloadType, workloadName := parts[0], parts[1], parts[2]

	// Parse analysis window
	window, err := time.ParseDuration(prs.Spec.AnalysisWindow)
	if err != nil {
		window = 7 * 24 * time.Hour // Default to 7 days
		logger.Info("Using default analysis window", "window", window)
	}

	// Collect metrics for the workload
	logger.Info("Collecting workload metrics", "workload", workloadKey, "window", window)
	workloadMetrics, err := r.MetricsClient.GetWorkloadMetrics(ctx, namespace, workloadName, workloadType, window)
	if err != nil {
		logger.Error(err, "Failed to get workload metrics", "workload", workloadKey)
		return nil, err
	}

	if len(workloadMetrics.Pods) == 0 {
		logger.Info("No metrics found for workload", "workload", workloadKey)
		return nil, fmt.Errorf("no metrics found for workload %s", workloadKey)
	}

	// Generate recommendations using the recommendation engine
	recommendations, err := r.RecommendEngine.GenerateRecommendations(ctx, workloadMetrics, prs.Spec.Thresholds)
	if err != nil {
		logger.Error(err, "Failed to generate recommendations", "workload", workloadKey)
		return nil, err
	}

	// Enhance recommendations with workload information
	for i := range recommendations {
		recommendations[i].PodReference.WorkloadType = workloadType
		recommendations[i].PodReference.WorkloadName = workloadName

		// Get current resources for comparison
		for _, pod := range pods {
			if pod.Name == recommendations[i].PodReference.Name {
				recommendations[i].CurrentResources = r.getCurrentResources(&pod)
				break
			}
		}
	}

	logger.Info("Generated recommendations", "workload", workloadKey, "count", len(recommendations))
	return recommendations, nil
}

// getCurrentResources extracts current resource requirements from a pod
func (r *PodRightSizingReconciler) getCurrentResources(pod *corev1.Pod) corev1.ResourceRequirements {
	totalRequests := make(corev1.ResourceList)
	totalLimits := make(corev1.ResourceList)

	for _, container := range pod.Spec.Containers {
		r.addResourceToTotal(totalRequests, container.Resources.Requests, corev1.ResourceCPU)
		r.addResourceToTotal(totalRequests, container.Resources.Requests, corev1.ResourceMemory)
		r.addResourceToTotal(totalLimits, container.Resources.Limits, corev1.ResourceCPU)
		r.addResourceToTotal(totalLimits, container.Resources.Limits, corev1.ResourceMemory)
	}

	return corev1.ResourceRequirements{
		Requests: totalRequests,
		Limits:   totalLimits,
	}
}

// addResourceToTotal adds a resource quantity to the total resource list
func (r *PodRightSizingReconciler) addResourceToTotal(total corev1.ResourceList, source corev1.ResourceList, resourceType corev1.ResourceName) {
	if quantity, ok := source[resourceType]; ok {
		if existing, exists := total[resourceType]; exists {
			existing.Add(quantity)
			total[resourceType] = existing
		} else {
			total[resourceType] = quantity
		}
	}
}

// applyRecommendations applies the generated recommendations
func (r *PodRightSizingReconciler) applyRecommendations(
	ctx context.Context,
	prs *rightsizingv1alpha1.PodRightSizing,
	recommendations []rightsizingv1alpha1.PodRecommendation,
) int {

	logger := log.FromContext(ctx)
	updatedCount := 0

	// Group recommendations by workload
	workloadRecommendations := make(map[string][]rightsizingv1alpha1.PodRecommendation)
	for _, rec := range recommendations {
		key := fmt.Sprintf("%s/%s/%s", rec.PodReference.Namespace, rec.PodReference.WorkloadType, rec.PodReference.WorkloadName)
		workloadRecommendations[key] = append(workloadRecommendations[key], rec)
	}

	// Apply recommendations per workload based on update strategy
	for workloadKey, workloadRecs := range workloadRecommendations {
		logger.Info("Applying recommendations for workload", "workload", workloadKey, "recommendations", len(workloadRecs))

		updated, err := r.applyWorkloadRecommendations(ctx, prs, workloadKey, workloadRecs)
		if err != nil {
			logger.Error(err, "Failed to apply workload recommendations", "workload", workloadKey)
			continue
		}

		updatedCount += updated
	}

	return updatedCount
}

// applyWorkloadRecommendations applies recommendations for a specific workload
func (r *PodRightSizingReconciler) applyWorkloadRecommendations(
	ctx context.Context,
	prs *rightsizingv1alpha1.PodRightSizing,
	workloadKey string,
	recommendations []rightsizingv1alpha1.PodRecommendation,
) (int, error) {

	logger := log.FromContext(ctx)

	if prs.Spec.UpdatePolicy.Strategy == rightsizingv1alpha1.UpdateStrategyManual {
		logger.Info("Manual strategy - skipping actual updates", "workload", workloadKey)
		return 0, nil // Don't apply, just generate recommendations
	}

	if len(recommendations) == 0 {
		return 0, nil
	}

	// Parse workload information
	parts := r.splitWorkloadKey(workloadKey)
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid workload key: %s", workloadKey)
	}

	namespace, workloadType, workloadName := parts[0], parts[1], parts[2]

	// Calculate average recommended resources across all pods in the workload
	avgRecommendation := r.calculateAverageRecommendation(recommendations)

	// Apply based on workload type
	switch workloadType {
	case "Deployment":
		return r.updateDeployment(ctx, namespace, workloadName, avgRecommendation)
	case "StatefulSet":
		return r.updateStatefulSet(ctx, namespace, workloadName, avgRecommendation)
	case "DaemonSet":
		return r.updateDaemonSet(ctx, namespace, workloadName, avgRecommendation)
	default:
		logger.Info("Workload type not supported for automatic updates", "type", workloadType)
		return 0, nil
	}
}

// calculateAverageRecommendation calculates average resource recommendations
func (r *PodRightSizingReconciler) calculateAverageRecommendation(recommendations []rightsizingv1alpha1.PodRecommendation) corev1.ResourceRequirements {
	if len(recommendations) == 0 {
		return corev1.ResourceRequirements{}
	}

	// For simplicity, use the first recommendation as the template
	// In a more sophisticated implementation, you might average across all pods
	return recommendations[0].RecommendedResources
}

// updateDeployment updates a Deployment with new resource recommendations.
func (r *PodRightSizingReconciler) updateDeployment(ctx context.Context, namespace, name string, resources corev1.ResourceRequirements) (int, error) {
	logger := log.FromContext(ctx)

	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &deployment); err != nil {
		return 0, fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}

	// Update container resources using helper
	updated := r.updateContainerResources(deployment.Spec.Template.Spec.Containers, resources, logger, "deployment", name)

	if !updated {
		logger.Info("No resource changes needed", "deployment", name)
		return 0, nil
	}

	// Update the deployment
	if err := r.Update(ctx, &deployment); err != nil {
		return 0, fmt.Errorf("failed to update deployment %s/%s: %w", namespace, name, err)
	}

	logger.Info("Successfully updated deployment", "deployment", name)
	return 1, nil
}

// updateStatefulSet updates a StatefulSet with new resource recommendations.
func (r *PodRightSizingReconciler) updateStatefulSet(ctx context.Context, namespace, name string, resources corev1.ResourceRequirements) (int, error) {
	var statefulSet appsv1.StatefulSet
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &statefulSet); err != nil {
		return 0, fmt.Errorf("failed to get statefulset %s/%s: %w", namespace, name, err)
	}

	return r.updateWorkloadResources(ctx, &statefulSet, statefulSet.Spec.Template.Spec.Containers, resources, "statefulset", name)
}

// updateDaemonSet updates a DaemonSet with new resource recommendations.
func (r *PodRightSizingReconciler) updateDaemonSet(ctx context.Context, namespace, name string, resources corev1.ResourceRequirements) (int, error) {
	var daemonSet appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &daemonSet); err != nil {
		return 0, fmt.Errorf("failed to get daemonset %s/%s: %w", namespace, name, err)
	}

	return r.updateWorkloadResources(ctx, &daemonSet, daemonSet.Spec.Template.Spec.Containers, resources, "daemonset", name)
}

// updateWorkloadResources is a generic helper for updating workload resources.
func (r *PodRightSizingReconciler) updateWorkloadResources(ctx context.Context, obj client.Object, containers []corev1.Container, resources corev1.ResourceRequirements, workloadType, name string) (int, error) {
	logger := log.FromContext(ctx)

	// Update container resources using helper
	updated := r.updateContainerResources(containers, resources, logger, workloadType, name)

	if !updated {
		return 0, nil
	}

	// Update the workload
	if err := r.Update(ctx, obj); err != nil {
		return 0, fmt.Errorf("failed to update %s %s: %w", workloadType, name, err)
	}

	logger.Info("Successfully updated "+workloadType, workloadType, name)
	return 1, nil
}

// updateContainerResources updates container resources and returns whether any updates were made.
func (r *PodRightSizingReconciler) updateContainerResources(containers []corev1.Container, resources corev1.ResourceRequirements, logger logr.Logger, workloadType, name string) bool {
	updated := false
	for i := range containers {
		container := &containers[i]
		if !r.resourcesEqual(container.Resources, resources) {
			logger.Info("Updating container resources",
				workloadType, name,
				"container", container.Name)

			container.Resources = resources
			updated = true
		}
	}
	return updated
}

// resourcesEqual compares two ResourceRequirements for equality
func (r *PodRightSizingReconciler) resourcesEqual(a, b corev1.ResourceRequirements) bool {
	// Compare requests
	if len(a.Requests) != len(b.Requests) {
		return false
	}
	for resource, quantity := range a.Requests {
		if bQuantity, exists := b.Requests[resource]; !exists || !quantity.Equal(bQuantity) {
			return false
		}
	}

	// Compare limits
	if len(a.Limits) != len(b.Limits) {
		return false
	}
	for resource, quantity := range a.Limits {
		if bQuantity, exists := b.Limits[resource]; !exists || !quantity.Equal(bQuantity) {
			return false
		}
	}

	return true
}

// updatePhase updates the status phase of the PodRightSizing resource
func (r *PodRightSizingReconciler) updatePhase(
	ctx context.Context,
	prs *rightsizingv1alpha1.PodRightSizing,
	phase rightsizingv1alpha1.RightSizingPhase,
	message string,
) error {

	prs.Status.Phase = phase
	prs.Status.Message = message

	return r.Status().Update(ctx, prs)
}

// splitWorkloadKey splits a workload key into its components
func (r *PodRightSizingReconciler) splitWorkloadKey(key string) []string {
	return strings.SplitN(key, "/", 3)
}

// SetupWithManager sets up the controller with the Manager
func (r *PodRightSizingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.PodRightSizing{}).
		Owns(&corev1.Pod{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.podToRightSizingRequests),
			builder.WithPredicates(&predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Only trigger on resource changes
					oldPod := e.ObjectOld.(*corev1.Pod)
					newPod := e.ObjectNew.(*corev1.Pod)
					return !r.containerResourcesEqual(oldPod.Spec.Containers, newPod.Spec.Containers)
				},
				CreateFunc: func(e event.CreateEvent) bool {
					// Trigger on new pod creation
					return true
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					// Don't trigger on pod deletion
					return false
				},
			}),
		).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.workloadToRightSizingRequests),
			builder.WithPredicates(&predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Trigger on deployment spec changes
					oldDep := e.ObjectOld.(*appsv1.Deployment)
					newDep := e.ObjectNew.(*appsv1.Deployment)
					return !r.containerResourcesEqual(
						oldDep.Spec.Template.Spec.Containers,
						newDep.Spec.Template.Spec.Containers,
					)
				},
			}),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 5,
		}).
		Complete(r)
}

// containerResourcesEqual compares container resources for equality
func (r *PodRightSizingReconciler) containerResourcesEqual(oldContainers, newContainers []corev1.Container) bool {
	if len(oldContainers) != len(newContainers) {
		return false
	}

	for i, oldContainer := range oldContainers {
		if i >= len(newContainers) {
			return false
		}
		newContainer := newContainers[i]

		if !r.resourcesEqual(oldContainer.Resources, newContainer.Resources) {
			return false
		}
	}

	return true
}

// podToRightSizingRequests maps pod changes to PodRightSizing reconcile requests
func (r *PodRightSizingReconciler) podToRightSizingRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	pod := obj.(*corev1.Pod)

	// Find all PodRightSizing resources that might target this pod
	var rightSizingList rightsizingv1alpha1.PodRightSizingList
	if err := r.List(ctx, &rightSizingList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, prs := range rightSizingList.Items {
		if r.podMatchesTarget(pod, &prs) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      prs.Name,
					Namespace: prs.Namespace,
				},
			})
		}
	}

	return requests
}

// workloadToRightSizingRequests maps workload changes to PodRightSizing reconcile requests
func (r *PodRightSizingReconciler) workloadToRightSizingRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	// Find all PodRightSizing resources that might target this workload
	var rightSizingList rightsizingv1alpha1.PodRightSizingList
	if err := r.List(ctx, &rightSizingList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, prs := range rightSizingList.Items {
		if r.workloadMatchesTarget(obj, &prs) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      prs.Name,
					Namespace: prs.Namespace,
				},
			})
		}
	}

	return requests
}

// podMatchesTarget checks if a pod matches the target criteria
func (r *PodRightSizingReconciler) podMatchesTarget(pod *corev1.Pod, prs *rightsizingv1alpha1.PodRightSizing) bool {
	// Check namespace
	if prs.Spec.Target.Namespace != "" && pod.Namespace != prs.Spec.Target.Namespace {
		return false
	}

	// Check excluded namespaces
	if r.isNamespaceExcluded(pod.Namespace, prs.Spec.Target.ExcludeNamespaces) {
		return false
	}

	// Check label selector
	if prs.Spec.Target.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(prs.Spec.Target.LabelSelector)
		if err != nil {
			return false
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			return false
		}
	}

	// Check namespace selector
	if prs.Spec.Target.NamespaceSelector != nil {
		// Get the namespace object to check its labels
		var namespace corev1.Namespace
		if err := r.Get(context.Background(), types.NamespacedName{Name: pod.Namespace}, &namespace); err != nil {
			return false
		}

		selector, err := metav1.LabelSelectorAsSelector(prs.Spec.Target.NamespaceSelector)
		if err != nil {
			return false
		}
		if !selector.Matches(labels.Set(namespace.Labels)) {
			return false
		}
	}

	// Check workload type
	if len(prs.Spec.Target.IncludeWorkloadTypes) > 0 {
		workloadType := r.getWorkloadType(pod)
		found := false
		for _, allowedType := range prs.Spec.Target.IncludeWorkloadTypes {
			if workloadType == allowedType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// workloadMatchesTarget checks if a workload matches the target criteria
func (r *PodRightSizingReconciler) workloadMatchesTarget(obj client.Object, prs *rightsizingv1alpha1.PodRightSizing) bool {
	return r.matchesNamespace(obj, prs) &&
		r.matchesWorkloadType(obj, prs) &&
		r.matchesLabelSelector(obj, prs) &&
		r.matchesNamespaceSelector(obj, prs)
}

// matchesNamespace checks if workload matches namespace criteria
func (r *PodRightSizingReconciler) matchesNamespace(obj client.Object, prs *rightsizingv1alpha1.PodRightSizing) bool {
	if prs.Spec.Target.Namespace != "" && obj.GetNamespace() != prs.Spec.Target.Namespace {
		return false
	}
	return !r.isNamespaceExcluded(obj.GetNamespace(), prs.Spec.Target.ExcludeNamespaces)
}

// matchesWorkloadType checks if workload type is allowed
func (r *PodRightSizingReconciler) matchesWorkloadType(obj client.Object, prs *rightsizingv1alpha1.PodRightSizing) bool {
	if len(prs.Spec.Target.IncludeWorkloadTypes) == 0 {
		return true
	}

	workloadType := r.getWorkloadTypeFromObject(obj)
	for _, allowedType := range prs.Spec.Target.IncludeWorkloadTypes {
		if workloadType == allowedType {
			return true
		}
	}
	return false
}

// matchesLabelSelector checks if workload matches label selector
func (r *PodRightSizingReconciler) matchesLabelSelector(obj client.Object, prs *rightsizingv1alpha1.PodRightSizing) bool {
	if prs.Spec.Target.LabelSelector == nil {
		return true
	}

	selector, err := metav1.LabelSelectorAsSelector(prs.Spec.Target.LabelSelector)
	if err != nil {
		return false
	}

	podLabels := r.getPodLabelsFromWorkload(obj)
	return selector.Matches(labels.Set(podLabels))
}

// matchesNamespaceSelector checks if workload's namespace matches selector
func (r *PodRightSizingReconciler) matchesNamespaceSelector(obj client.Object, prs *rightsizingv1alpha1.PodRightSizing) bool {
	if prs.Spec.Target.NamespaceSelector == nil {
		return true
	}

	var namespace corev1.Namespace
	if err := r.Get(context.Background(), types.NamespacedName{Name: obj.GetNamespace()}, &namespace); err != nil {
		return false
	}

	selector, err := metav1.LabelSelectorAsSelector(prs.Spec.Target.NamespaceSelector)
	if err != nil {
		return false
	}
	return selector.Matches(labels.Set(namespace.Labels))
}

// getPodLabelsFromWorkload extracts pod labels from workload template
func (r *PodRightSizingReconciler) getPodLabelsFromWorkload(obj client.Object) map[string]string {
	switch workload := obj.(type) {
	case *appsv1.Deployment:
		return workload.Spec.Template.Labels
	case *appsv1.StatefulSet:
		return workload.Spec.Template.Labels
	case *appsv1.DaemonSet:
		return workload.Spec.Template.Labels
	default:
		return obj.GetLabels()
	}
}

// getWorkloadTypeFromObject determines the workload type from a Kubernetes object
func (r *PodRightSizingReconciler) getWorkloadTypeFromObject(obj client.Object) string {
	switch obj.(type) {
	case *appsv1.Deployment:
		return WorkloadTypeDeployment
	case *appsv1.StatefulSet:
		return WorkloadTypeStatefulSet
	case *appsv1.DaemonSet:
		return WorkloadTypeDaemonSet
	default:
		return "Unknown"
	}
}
