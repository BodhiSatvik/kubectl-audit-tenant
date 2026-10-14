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

func quota(hard corev1.ResourceList) corev1.ResourceQuota {
	return corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "q"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}}
}

func TestTheDesignFixtureFits(t *testing.T) {
	// DESIGN.md section 8, tenant-a: Deployment api, 3 replicas, 25% surge, HPA maxReplicas 4.
	d := deployment("api", replicas(3), rolling(intstr.FromString("25%")))
	w := Deployment(d, []autoscalingv2.HorizontalPodAutoscaler{hpa("api", "apps/v1", "Deployment", "api", 4)}, Defaults{})
	total := Total([]Workload{w})
	want := list("requests.cpu", "500m", "requests.memory", "640Mi", "limits.cpu", "2500m", "limits.memory", "2.5Gi", "pods", "5")
	if !same(total, want) {
		t.Fatalf("Total() = %v, want %v (5 pods, 0.5 cpu / 640Mi requests, 2.5 cpu / 2.5Gi limits)", total, want)
	}
	hard := list("requests.cpu", "4", "requests.memory", "8Gi", "limits.cpu", "8", "limits.memory", "16Gi", "pods", "20")
	if short := Compare(quota(hard), total); len(short) != 0 {
		t.Errorf("Compare() = %v, want none: the fixture fits tenant-a-quota", short)
	}
	if got, want := Breakdown([]Workload{w}), "deployments: api 4->5 (HPA max)"; got != want {
		t.Errorf("Breakdown() = %q, want %q", got, want)
	}
}

func TestCompareNamesEveryShortKeyInOrder(t *testing.T) {
	api := deployment("api", replicas(3), rolling(intstr.FromInt32(1)))
	api.Spec.Template.Spec.Containers[0].Resources.Requests = list("cpu", "750m", "memory", "128Mi")
	worker := deployment("worker", replicas(2), rolling(intstr.FromInt32(1)))
	worker.Spec.Template.Spec.Containers[0].Resources.Requests = list("cpu", "1", "memory", "128Mi")
	ws := []Workload{Deployment(api, nil, Defaults{}), Deployment(worker, nil, Defaults{})}
	// Peaks: api 4 pods, worker 3 pods; requests.cpu 3 + 3 = 6; limits.memory 7 x 512Mi = 3584Mi; pods 7.
	hard := list("cpu", "4", "limits.memory", "2Gi", "pods", "7", "services", "1")
	var got []string
	for _, s := range Compare(quota(hard), Total(ws)) {
		got = append(got, s.String())
	}
	want := []string{
		"cpu: peak needs 6, hard is 4, short by 2",
		"limits.memory: peak needs 3584Mi, hard is 2Gi, short by 1536Mi",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Compare() = %q,\nwant %q (pods fits exactly; services is not compared)", got, want)
	}
	if got, want := Breakdown(ws), "deployments: api 3->4, worker 2->3"; got != want {
		t.Errorf("Breakdown() = %q, want %q", got, want)
	}
}

func TestCompareReadsBothSpellingsOfAKey(t *testing.T) {
	ws := []Workload{StatefulSet(statefulSet(replicas(3)), nil, Defaults{})}
	hard := list("memory", "2Gi", "requests.memory", "4Gi", "count/pods", "2")
	var got []string
	for _, s := range Compare(quota(hard), Total(ws)) {
		got = append(got, s.String())
	}
	want := []string{ // requests.memory 4Gi fits; its alias memory 2Gi does not
		"memory: peak needs 3Gi, hard is 2Gi, short by 1Gi",
		"count/pods: peak needs 3, hard is 2, short by 1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Compare() = %q, want %q", got, want)
	}
}

func TestBreakdownGroupsByKind(t *testing.T) {
	ws := []Workload{
		StatefulSet(statefulSet(replicas(3)), nil, Defaults{}),
		Deployment(deployment("api", replicas(2), appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}), nil, Defaults{}),
	}
	if got, want := Breakdown(ws), "deployments: api 2->2; statefulsets: db 3->3"; got != want {
		t.Errorf("Breakdown() = %q, want %q", got, want)
	}
	if got := Breakdown(nil); got != "" {
		t.Errorf("Breakdown(nil) = %q, want empty", got)
	}
}

func TestCanonical(t *testing.T) {
	for _, tt := range []struct {
		key  corev1.ResourceName
		want corev1.ResourceName
		ok   bool
	}{
		{"cpu", "requests.cpu", true}, {"requests.cpu", "requests.cpu", true},
		{"memory", "requests.memory", true}, {"limits.memory", "limits.memory", true},
		{"count/pods", "pods", true}, {"pods", "pods", true},
		{"services", "", false}, {"requests.storage", "", false},
	} {
		if got, ok := Canonical(tt.key); got != tt.want || ok != tt.ok {
			t.Errorf("Canonical(%s) = %s, %v; want %s, %v", tt.key, got, ok, tt.want, tt.ok)
		}
	}
}

func TestQuotaKeys(t *testing.T) {
	hard := list("services", "1", "count/pods", "9", "limits.cpu", "2", "memory", "1Gi", "requests.memory", "2Gi", "cpu", "1")
	got := QuotaKeys(quota(hard))
	want := []corev1.ResourceName{"cpu", "requests.memory", "memory", "limits.cpu", "count/pods"}
	if !slices.Equal(got, want) {
		t.Errorf("QuotaKeys() = %v, want %v (Keys order, canonical spelling first, other keys left out)", got, want)
	}
}

func TestTotalCountsAnUnsizedPodAsZero(t *testing.T) {
	d := deployment("bare", replicas(2), appsv1.DeploymentStrategy{})
	d.Spec.Template.Spec.Containers = []corev1.Container{{Name: "app"}}
	total := Total([]Workload{Deployment(d, nil, Defaults{})})
	if want := list("requests.cpu", "0", "requests.memory", "0", "limits.cpu", "0", "limits.memory", "0", "pods", "3"); !same(total, want) {
		t.Errorf("Total() = %v, want %v", total, want)
	}
	if got := total[corev1.ResourceRequestsCPU]; got.String() != "0" {
		t.Errorf("an unsized total reads %q, want 0", got.String())
	}
}
