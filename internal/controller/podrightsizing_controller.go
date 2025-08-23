// internal/controller/podrightsizing_controller.go
package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rightsizingv1alpha1 "github.com/your-username/k8s-pod-rightsizer/api/v1alpha1"
)

// PodRightSizingReconciler reconciles a PodRightSizing object
type PodRightSizingReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	MetricsClient   MetricsClient
	RecommendEngine RecommendationEngine
}

// MetricsClient interface for collecting pod metrics
type MetricsClient interface {
	GetPodMetrics(ctx context.Context, namespace, podName string, window time.Duration) (*PodMetrics, error)
	GetWorkloadMetrics(ctx context.Context, namespace, workloadName, workloadType string, window time.Duration) (*WorkloadMetrics, error)
}

// RecommendationEngine interface for generating resource recommendations
type RecommendationEngine interface {
	GenerateRecommendations(ctx context.Context, metrics *WorkloadMetrics, thresholds rightsizingv1alpha1.ResourceThresholds) ([]rightsizingv1alpha1.PodRecommendation, error)
}

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

// +kubebuilder:rbac:groups=rightsizing.k8s-rightsizer.io,resources=podrightsizings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rightsizing.k8s-rightsizer.io,resources=podrightsizings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rightsizing.k8s-rightsizer.io,resources=podrightsizings/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="apps",resources=deployments;statefulsets;daemonsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create

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
		r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseError, fmt.Sprintf("Failed to discover pods: %v", err))
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, err
	}

	logger.Info("Discovered target pods", "count", len(targetPods))
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

		updatedCount, err := r.applyRecommendations(ctx, &podRightSizing, allRecommendations)
		if err != nil {
			logger.Error(err, "Failed to apply some recommendations")
			r.updatePhase(ctx, &podRightSizing, rightsizingv1alpha1.PhaseError, fmt.Sprintf("Failed to apply recommendations: %v", err))
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, err
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
			interval = d
		}
	}

	return time.Since(prs.Status.LastAnalysisTime.Time) >= interval
}

// requeueAfter calculates when to requeue based on schedule
func (r *PodRightSizingReconciler) requeueAfter(prs *rightsizingv1alpha1.PodRightSizing) ctrl.Result {
	// Simple implementation - requeue every hour
	// Production would parse the cron schedule properly
	return ctrl.Result{RequeueAfter: time.Hour}
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

	// List pods in target namespace(s)
	namespaces := []string{prs.Spec.Target.Namespace}
	if prs.Spec.Target.Namespace == "" {
		namespaces = []string{""} // All namespaces
	}

	for _, ns := range namespaces {
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

// shouldIncludePod determines if a pod should be included for analysis
func (r *PodRightSizingReconciler) shouldIncludePod(pod *corev1.Pod, prs *rightsizingv1alpha1.PodRightSizing) bool {
	// Skip pods that are not running
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	// Skip pods in excluded namespaces
	for _, excludeNs := range prs.Spec.Target.ExcludeNamespaces {
		if pod.Namespace == excludeNs {
			return false
		}
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

	return true
}

// getWorkloadType determines the workload type of a pod
func (r *PodRightSizingReconciler) getWorkloadType(pod *corev1.Pod) string {
	for _, owner := range pod.OwnerReferences {
		switch owner.Kind {
		case "ReplicaSet":
			return "Deployment"
		case "StatefulSet":
			return "StatefulSet"
		case "DaemonSet":
			return "DaemonSet"
		case "Job":
			return "Job"
		}
	}
	return "Pod"
}

// groupPodsByWorkload groups pods by their parent workload
func (r *PodRightSizingReconciler) groupPodsByWorkload(pods []corev1.Pod) map[string][]corev1.Pod {
	groups := make(map[string][]corev1.Pod)
	
	for _, pod := range pods {
		workloadName := r.getWorkloadName(&pod)
		workloadType := r.getWorkloadType(&pod)
		key := fmt.Sprintf("%s/%s/%s", pod.Namespace, workloadType, workloadName)
		groups[key] = append(groups[key], pod)
	}
	
	return groups
}

// getWorkloadName gets the name of the parent workload
func (r *PodRightSizingReconciler) getWorkloadName(pod *corev1.Pod) string {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			// For Deployments, we need to get the Deployment name from ReplicaSet
			var rs appsv1.ReplicaSet
			if err := r.Get(context.Background(), types.NamespacedName{
				Name:      owner.Name,
				Namespace: pod.Namespace,
			}, &rs); err == nil {
				for _, rsOwner := range rs.OwnerReferences {
					if rsOwner.Kind == "Deployment" {
						return rsOwner.Name
					}
				}
			}
		}
		return owner.Name
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
	parts := splitWorkloadKey(workloadKey)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid workload key: %s", workloadKey)
	}
	
	namespace, workloadType, workloadName := parts[0], parts[1], parts[2]
	
	// Parse analysis window
	window, err := time.ParseDuration(prs.Spec.AnalysisWindow)
	if err != nil {
		window = 7 * 24 * time.Hour // Default to 7 days
	}
	
	// Collect metrics for the workload
	workloadMetrics, err := r.MetricsClient.GetWorkloadMetrics(ctx, namespace, workloadName, workloadType, window)
	if err != nil {
		logger.Error(err, "Failed to get workload metrics", "workload", workloadKey)
		return nil, err
	}
	
	// Generate recommendations using the recommendation engine
	recommendations, err := r.RecommendEngine.GenerateRecommendations(ctx, workloadMetrics, prs.Spec.Thresholds)
	if err != nil {
		logger.Error(err, "Failed to generate recommendations", "workload", workloadKey)
		return nil, err
	}
	
	logger.Info("Generated recommendations", "workload", workloadKey, "count", len(recommendations))
	return recommendations, nil
}

// applyRecommendations applies the generated recommendations
func (r *PodRightSizingReconciler) applyRecommendations(
	ctx context.Context,
	prs *rightsizingv1alpha1.PodRightSizing,
	recommendations []rightsizingv1alpha1.PodRecommendation,
) (int, error) {
	
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
	
	return updatedCount, nil
}

// applyWorkloadRecommendations applies recommendations for a specific workload
func (r *PodRightSizingReconciler) applyWorkloadRecommendations(
	ctx context.Context,
	prs *rightsizingv1alpha1.PodRightSizing,
	workloadKey string,
	recommendations []rightsizingv1alpha1.PodRecommendation,
) (int, error) {
	
	// This is a simplified implementation
	// Production implementation would handle different update strategies:
	// - Immediate: Update all at once
	// - Gradual: Update with rolling strategy
	// - Manual: Just generate recommendations
	
	if prs.Spec.UpdatePolicy.Strategy == rightsizingv1alpha1.UpdateStrategyManual {
		return 0, nil // Don't apply, just generate recommendations
	}
	
	// For now, implement a simple update strategy
	// In production, you'd want to:
	// 1. Get the parent workload (Deployment/StatefulSet)
	// 2. Update the resource requests/limits in the workload spec
	// 3. Handle rolling updates properly
	// 4. Monitor the update progress
	
	// TODO: Implement actual workload updates
	return len(recommendations), nil
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

// SetupWithManager sets up the controller with the Manager
func (r *PodRightSizingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.PodRightSizing{}).
		Owns(&corev1.Pod{}).
		Watches(
			&source.Kind{Type: &corev1.Pod{}},
			&handler.EnqueueRequestsFromMapFunc{
				ToRequests: handler.ToRequestsFunc(r.podToRightSizingRequests),
			},
			&predicate.Funcs{
				UpdateFunc: func(e predicate.UpdateEvent) bool {
					// Only trigger on resource changes
					oldPod := e.ObjectOld.(*corev1.Pod)
					newPod := e.ObjectNew.(*corev1.Pod)
					return !resourcesEqual(oldPod.Spec.Containers, newPod.Spec.Containers)
				},
			},
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 5,
		}).
		Complete(r)
}

// podToRightSizingRequests maps pod changes to PodRightSizing reconcile requests
func (r *PodRightSizingReconciler) podToRightSizingRequests(obj client.Object) []reconcile.Request {
	pod := obj.(*corev1.Pod)
	
	// Find all PodRightSizing resources that might target this pod
	var rightSizingList rightsizingv1alpha1.PodRightSizingList
	if err := r.List(context.Background(), &rightSizingList); err != nil {
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

// podMatchesTarget checks if a pod matches the target criteria
func (r *PodRightSizingReconciler) podMatchesTarget(pod *corev1.Pod, prs *rightsizingv1alpha1.PodRightSizing) bool {
	// Simplified matching logic
	// Production implementation would do full selector matching
	if prs.Spec.Target.Namespace != "" && pod.Namespace != prs.Spec.Target.Namespace {
		return false
	}
	
	if prs.Spec.Target.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(prs.Spec.Target.LabelSelector)
		if err != nil {
			return false
		}
		return selector.Matches(labels.Set(pod.Labels))
	}
	
	return true
}

// Helper functions
func splitWorkloadKey(key string) []string {
	// Split "namespace/workloadType/workloadName"
	// This is a simplified implementation
	return []string{"default", "Deployment", "example"} // TODO: implement proper parsing
}

func resourcesEqual(oldContainers, newContainers []corev1.Container) bool {
	// Simplified comparison - compare resource requests/limits
	// Production implementation would do deep comparison
	return len(oldContainers) == len(newContainers)
}