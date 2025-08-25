package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodRightSizing_Creation(t *testing.T) {
	prs := &PodRightSizing{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rightsizing",
			Namespace: "default",
		},
		Spec: PodRightSizingSpec{
			Target: TargetSpec{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "test-app",
					},
				},
			},
			UpdatePolicy: UpdatePolicy{
				Strategy: UpdateStrategyGradual,
			},
			Thresholds: ResourceThresholds{
				MinCPU:    resource.MustParse("10m"),
				MaxCPU:    resource.MustParse("2"),
				MinMemory: resource.MustParse("50Mi"),
				MaxMemory: resource.MustParse("4Gi"),
			},
		},
	}

	assert.Equal(t, "test-rightsizing", prs.Name)
	assert.Equal(t, "default", prs.Namespace)
	assert.Equal(t, "test-app", prs.Spec.Target.LabelSelector.MatchLabels["app"])
	assert.Equal(t, UpdateStrategyGradual, prs.Spec.UpdatePolicy.Strategy)
	assert.NotNil(t, prs.Spec.Thresholds.MinCPU)
	assert.NotNil(t, prs.Spec.Thresholds.MaxCPU)
	assert.NotNil(t, prs.Spec.Thresholds.MinMemory)
	assert.NotNil(t, prs.Spec.Thresholds.MaxMemory)
}

func TestPodRightSizingStatus_Basic(t *testing.T) {
	status := &PodRightSizingStatus{
		Phase:           PhaseAnalyzing,
		TargetedPods:    int32(10),
		Recommendations: []PodRecommendation{},
	}

	assert.Equal(t, PhaseAnalyzing, status.Phase)
	assert.Equal(t, int32(10), status.TargetedPods)
	assert.Empty(t, status.Recommendations)
}

func TestPodRecommendation_Creation(t *testing.T) {
	rec := &PodRecommendation{
		PodReference: PodReference{
			Name:         "test-pod",
			Namespace:    "default",
			WorkloadName: "test-deployment",
			WorkloadType: "Deployment",
		},
		RecommendedResources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
		Confidence: 85,
		Reason:     "Based on 95th percentile usage",
	}

	assert.Equal(t, "test-pod", rec.PodReference.Name)
	assert.Equal(t, "default", rec.PodReference.Namespace)
	assert.Equal(t, "test-deployment", rec.PodReference.WorkloadName)
	assert.Equal(t, "Deployment", rec.PodReference.WorkloadType)
	assert.NotNil(t, rec.RecommendedResources.Requests)
	assert.NotNil(t, rec.RecommendedResources.Limits)
	assert.Equal(t, 85, rec.Confidence)
	assert.Equal(t, "Based on 95th percentile usage", rec.Reason)
}

func TestResourceThresholds_Values(t *testing.T) {
	thresholds := &ResourceThresholds{
		MinCPU:    resource.MustParse("10m"),
		MaxCPU:    resource.MustParse("4"),
		MinMemory: resource.MustParse("100Mi"),
		MaxMemory: resource.MustParse("8Gi"),
	}

	assert.False(t, thresholds.MinCPU.IsZero())
	assert.False(t, thresholds.MaxCPU.IsZero())
	assert.False(t, thresholds.MinMemory.IsZero())
	assert.False(t, thresholds.MaxMemory.IsZero())

	// Verify actual values
	assert.Equal(t, int64(10), thresholds.MinCPU.MilliValue())
	assert.Equal(t, int64(4000), thresholds.MaxCPU.MilliValue())
}

func TestUpdatePolicy_Strategy(t *testing.T) {
	policy := &UpdatePolicy{
		Strategy: UpdateStrategyGradual,
	}

	assert.Equal(t, UpdateStrategyGradual, policy.Strategy)
}

func TestRightSizingPhases(t *testing.T) {
	// Test that all phases are defined
	phases := []RightSizingPhase{
		PhaseInitializing,
		PhaseAnalyzing,
		PhaseRecommending,
		PhaseUpdating,
		PhaseCompleted,
		PhaseError,
	}

	expectedPhases := []string{
		"Initializing",
		"Analyzing",
		"Recommending",
		"Updating",
		"Completed",
		"Error",
	}

	assert.Len(t, phases, len(expectedPhases))

	for i, phase := range phases {
		assert.Equal(t, expectedPhases[i], string(phase))
	}
}

func TestUpdateStrategies(t *testing.T) {
	strategies := []UpdateStrategy{
		UpdateStrategyImmediate,
		UpdateStrategyGradual,
		UpdateStrategyManual,
	}

	expectedStrategies := []string{
		"immediate",
		"gradual",
		"manual",
	}

	for i, strategy := range strategies {
		assert.Equal(t, expectedStrategies[i], string(strategy))
	}
}

func TestMetricsSourceTypes(t *testing.T) {
	sources := []MetricsSourceType{
		MetricsSourcePrometheus,
		MetricsSourceMetricsServer,
	}

	expectedSources := []string{
		"prometheus",
		"metrics-server",
	}

	for i, source := range sources {
		assert.Equal(t, expectedSources[i], string(source))
	}
}

func TestAuthTypes(t *testing.T) {
	authTypes := []AuthType{
		AuthTypeNone,
		AuthTypeBasic,
		AuthTypeBearer,
	}

	expectedTypes := []string{
		"none",
		"basic",
		"bearer",
	}

	for i, authType := range authTypes {
		assert.Equal(t, expectedTypes[i], string(authType))
	}
}
