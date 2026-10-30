package headroom

import (
	"slices"
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

// withMin sets h's minReplicas.
func withMin(h autoscalingv2.HorizontalPodAutoscaler, n int32) autoscalingv2.HorizontalPodAutoscaler {
	h.Spec.MinReplicas = &n
	return h
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
		// Value: protects=an HPA maxReplicas below spec.replicas lowers the count (ADR 0006, D110); fails_when=the one-HPA case keeps spec.replicas when maxReplicas is lower; why_new=every HPA row has max above replicas; seam=none; no_claim=replicas 0 with an HPA (TODOS)
		{"an HPA's maxReplicas below replicas lowers them", deployment("api", replicas(3), rolling(intstr.FromInt32(1))),
			[]autoscalingv2.HorizontalPodAutoscaler{hpa("api", "apps/v1", "Deployment", "api", 2)}, 2, 3, "api"},
		{"an HPA for another kind, group or name does not match", deployment("api", replicas(3), rolling(intstr.FromString("25%"))),
			[]autoscalingv2.HorizontalPodAutoscaler{
				hpa("sts", "apps/v1", "StatefulSet", "api", 9), hpa("rollout", "argoproj.io/v1alpha1", "Deployment", "api", 9), hpa("web", "apps/v1", "Deployment", "web", 9)}, 3, 4, ""},
		{"several HPAs: no metric scaling, so spec.replicas stays (AmbiguousSelector, D126)", deployment("api", replicas(3), rolling(intstr.FromInt32(1))),
			[]autoscalingv2.HorizontalPodAutoscaler{hpa("a", "apps/v1", "Deployment", "api", 6), hpa("b", "apps/v1", "Deployment", "api", 8)}, 3, 4, ""},
		{"several HPAs still hold replicas at the largest minReplicas", deployment("api", replicas(3), rolling(intstr.FromInt32(1))),
			[]autoscalingv2.HorizontalPodAutoscaler{withMin(hpa("a", "apps/v1", "Deployment", "api", 6), 5), withMin(hpa("b", "apps/v1", "Deployment", "api", 8), 2)}, 5, 6, ""},
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
	d := Defaults{Request: list("cpu", "100m", "memory", "128Mi"), Limit: list("cpu", "500m", "memory", "512Mi")}
	s := statefulSet(replicas(1))
	s.Spec.Template.Spec.Containers = append(s.Spec.Template.Spec.Containers, container("unsized", nil, nil))
	if w := StatefulSet(s, nil, d); !same(w.Pod.Requests, list("cpu", "1100m", "memory", "1152Mi")) || w.Pod.Defaulted != 1 {
		t.Errorf("StatefulSet() pod = %+v; want the LimitRange defaults applied to the unsized container", w.Pod)
	}
}

func TestDeploymentPeakDoesNotWrapOnAHugeSurgePercent(t *testing.T) {
	// The API stores any maxSurge percent (reproduced on kind); scaling 9e18% overflows int and
	// AT-10 printed "pods -9223372036854774809 of 20" as a PASS.
	w := Deployment(deployment("api", replicas(1000), rolling(intstr.FromString("9000000000000000000%"))), nil, Defaults{})
	if w.Peak < w.Replicas {
		t.Fatalf("peak %d is below replicas %d: the surge wrapped negative", w.Peak, w.Replicas)
	}
	if p := Total([]Workload{w, w})[corev1.ResourcePods]; p.Sign() < 0 {
		t.Errorf("pods total %s is negative", p.String())
	}
	// Value: protects=the peak never wraps on a surge that scales inside int64 on every arch; fails_when=the clamp narrows to extra < 0 (9e18% above takes that arm on amd64 CI only); why_new=the 9e18% case is arch-defined; seam=none; no_claim=none beyond protects
	w = Deployment(deployment("api", replicas(2000), rolling(intstr.FromString("461168601842738739%"))), nil, Defaults{})
	if w.Peak < w.Replicas {
		t.Errorf("peak %d is below replicas %d: a surge inside int64 still wrapped the sum", w.Peak, w.Replicas)
	}
}

func TestDeploymentPeakMarksAMaxSurgePast64Bits(t *testing.T) {
	// D135: API validation stores a percent of 2^63 or more; it does not parse, here or in the Deployment
	// controller, which then fails every sync. No surge is counted, and the workload says why.
	w := Deployment(deployment("api", replicas(3), rolling(intstr.FromString("9223372036854775808%"))), nil, Defaults{})
	if !w.BadSurge || w.Peak != 3 {
		t.Errorf("Deployment() = BadSurge %v peak %d; want BadSurge and peak 3 (no surge)", w.BadSurge, w.Peak)
	}
	if w := Deployment(deployment("api", replicas(3), rolling(intstr.FromString("25%"))), nil, Defaults{}); w.BadSurge {
		t.Error("Deployment() marks a 25% maxSurge as bad")
	}
}

func TestSeveralHPAsAreNamedForTheNote(t *testing.T) {
	w := Deployment(deployment("api", replicas(3), rolling(intstr.FromInt32(1))),
		[]autoscalingv2.HorizontalPodAutoscaler{hpa("b", "apps/v1", "Deployment", "api", 8), hpa("a", "apps/v1", "Deployment", "api", 6), hpa("other", "apps/v1", "Deployment", "web", 9)}, Defaults{})
	if !slices.Equal(w.ConflictingHPAs, []string{"a", "b"}) {
		t.Errorf("ConflictingHPAs = %v, want [a b]: the HPAs that target api, by name", w.ConflictingHPAs)
	}
}
