package headroom

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func replicas(n int32) *int32 { return &n }

// deployment is a Deployment with one sized container (100m/128Mi requests, 500m/512Mi limits).
func deployment(name string, n *int32, strategy appsv1.DeploymentStrategy) appsv1.Deployment {
	return appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: appsv1.DeploymentSpec{Replicas: n, Strategy: strategy,
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			container("app", list("cpu", "100m", "memory", "128Mi"), list("cpu", "500m", "memory", "512Mi"))}}}}}
}

// statefulSet is the StatefulSet db with one container of 1 cpu and 1Gi.
func statefulSet(n *int32) appsv1.StatefulSet {
	return appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db"}, Spec: appsv1.StatefulSetSpec{Replicas: n,
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			container("db", list("cpu", "1", "memory", "1Gi"), list("cpu", "1", "memory", "1Gi"))}}}}}
}

func rolling(surge intstr.IntOrString) appsv1.DeploymentStrategy {
	return appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: &surge}}
}

func hpa(name, apiVersion, kind, target string, maxReplicas int32) autoscalingv2.HorizontalPodAutoscaler {
	return autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
		ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: apiVersion, Kind: kind, Name: target}, MaxReplicas: maxReplicas}}
}

func TestDeploymentPeak(t *testing.T) {
	tests := []struct {
		name     string
		d        appsv1.Deployment
		hpas     []autoscalingv2.HorizontalPodAutoscaler
		replicas int
		peak     int
		hpaName  string
	}{
		{"replicas unset is 1; the default surge is 25%, rounded up", deployment("api", nil, appsv1.DeploymentStrategy{}), nil, 1, 2, ""},
		{"25% of 3 rounds up to 1", deployment("api", replicas(3), rolling(intstr.FromString("25%"))), nil, 3, 4, ""},
		{"30% of 10 is 3", deployment("api", replicas(10), rolling(intstr.FromString("30%"))), nil, 10, 13, ""},
		{"an absolute surge", deployment("api", replicas(3), rolling(intstr.FromInt32(2))), nil, 3, 5, ""},
		{"a surge of 0", deployment("api", replicas(3), rolling(intstr.FromInt32(0))), nil, 3, 3, ""},
		{"RollingUpdate with no surge set takes 25%", deployment("api", replicas(8), appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}), nil, 8, 10, ""},
		{"Recreate never overlaps", deployment("api", replicas(3), appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}), nil, 3, 3, ""},
		{"scaled to zero has no rollout", deployment("api", replicas(0), rolling(intstr.FromInt32(2))), nil, 0, 0, ""},
		{"an HPA's maxReplicas replaces replicas", deployment("api", replicas(3), rolling(intstr.FromString("25%"))),
			[]autoscalingv2.HorizontalPodAutoscaler{hpa("api", "apps/v1", "Deployment", "api", 4)}, 4, 5, "api"},
		{"an HPA for another kind, group or name does not match", deployment("api", replicas(3), rolling(intstr.FromString("25%"))),
			[]autoscalingv2.HorizontalPodAutoscaler{
				hpa("sts", "apps/v1", "StatefulSet", "api", 9), hpa("rollout", "argoproj.io/v1alpha1", "Deployment", "api", 9), hpa("web", "apps/v1", "Deployment", "web", 9)}, 3, 4, ""},
		{"several HPAs: the largest maxReplicas", deployment("api", replicas(3), rolling(intstr.FromInt32(1))),
			[]autoscalingv2.HorizontalPodAutoscaler{hpa("a", "apps/v1", "Deployment", "api", 6), hpa("b", "apps/v1", "Deployment", "api", 8)}, 8, 9, "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := Deployment(tt.d, tt.hpas, Defaults{})
			if w.Kind != "Deployment" || w.Name != "api" || w.Replicas != tt.replicas || w.Peak != tt.peak || w.HPA != tt.hpaName {
				t.Errorf("Deployment() = %s %s replicas %d peak %d HPA %q; want replicas %d peak %d HPA %q", w.Kind, w.Name, w.Replicas, w.Peak, w.HPA, tt.replicas, tt.peak, tt.hpaName)
			}
		})
	}
}

func TestStatefulSetPeakIsItsReplicas(t *testing.T) {
	for _, tt := range []struct {
		s    appsv1.StatefulSet
		hpas []autoscalingv2.HorizontalPodAutoscaler
		want int
	}{
		{statefulSet(nil), nil, 1},
		{statefulSet(replicas(3)), nil, 3},
		{statefulSet(replicas(3)), []autoscalingv2.HorizontalPodAutoscaler{hpa("db", "apps/v1", "StatefulSet", "db", 5)}, 5},
	} {
		if w := StatefulSet(tt.s, tt.hpas, Defaults{}); w.Kind != "StatefulSet" || w.Replicas != tt.want || w.Peak != tt.want {
			t.Errorf("StatefulSet() = replicas %d peak %d, want %d for both", w.Replicas, w.Peak, tt.want)
		}
	}
}

func TestWorkloadsCarryTheirPod(t *testing.T) {
	d := Defaults{Name: "limits", Request: list("cpu", "100m", "memory", "128Mi"), Limit: list("cpu", "500m", "memory", "512Mi")}
	s := statefulSet(replicas(1))
	s.Spec.Template.Spec.Containers = append(s.Spec.Template.Spec.Containers, container("unsized", nil, nil))
	if w := StatefulSet(s, nil, d); !same(w.Pod.Requests, list("cpu", "1100m", "memory", "1152Mi")) || w.Pod.Defaulted != 1 {
		t.Errorf("StatefulSet() pod = %+v; want the LimitRange defaults applied to the unsized container", w.Pod)
	}
}
