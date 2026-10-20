package checks

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at10Spec() Spec { return Registry[9] }

const (
	deploymentsPath  = "GET /apis/apps/v1/namespaces/tenant-a/deployments"
	statefulSetsPath = "GET /apis/apps/v1/namespaces/tenant-a/statefulsets"
	hpasPath         = "GET /apis/autoscaling/v2/namespaces/tenant-a/horizontalpodautoscalers"
	estimate         = "estimate: "
	notCounted       = "the peak counts active pods during a rollout; terminating pods can add to usage; not counted: DaemonSets, Jobs, CronJobs, bare Pods and Argo Rollouts"
	at10FailHint     = "raise the quota's hard values by the shortfall, or lower replicas, HPA maxReplicas or maxSurge; the estimate assumes every workload rolls out at once"
)

// rl builds a ResourceList from "cpu", "100m" pairs.
func rl(kv ...string) corev1.ResourceList {
	l := corev1.ResourceList{}
	for pair := range slices.Chunk(kv, 2) {
		l[corev1.ResourceName(pair[0])] = resource.MustParse(pair[1])
	}
	return l
}

// api is the fixture's Deployment: 3 replicas, 25% surge, 100m/128Mi requests, 500m/512Mi limits.
func api() appsv1.Deployment {
	three, surge := int32(3), intstr.FromString("25%")
	return appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api"}, Spec: appsv1.DeploymentSpec{Replicas: &three,
		Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: &surge}},
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api",
			Resources: corev1.ResourceRequirements{Requests: rl("cpu", "100m", "memory", "128Mi"), Limits: rl("cpu", "500m", "memory", "512Mi")}}}}}}}
}

// apiHPA is the fixture's HPA: maxReplicas 4 for Deployment api.
func apiHPA() autoscalingv2.HorizontalPodAutoscaler {
	return autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "api"}, Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
		ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}, MaxReplicas: 4}}
}

// fixtureQuota is tenant-a-quota with the used values of the three running api pods.
func fixtureQuota() corev1.ResourceQuota {
	q := quota("tenant-a-quota", fiveKeys...)
	q.Spec.Hard = rl("requests.cpu", "4", "requests.memory", "8Gi", "limits.cpu", "8", "limits.memory", "16Gi", "pods", "20")
	q.Status.Used = rl("requests.cpu", "300m", "requests.memory", "384Mi", "limits.cpu", "1500m", "limits.memory", "1536Mi", "pods", "3")
	return q
}

// fixtureLimits is tenant-a-limits: default 500m/512Mi, defaultRequest 100m/128Mi.
func fixtureLimits() corev1.LimitRange {
	return limitRange("tenant-a-limits", corev1.LimitRangeItem{Type: corev1.LimitTypeContainer,
		Default: rl("cpu", "500m", "memory", "512Mi"), DefaultRequest: rl("cpu", "100m", "memory", "128Mi")})
}

// at10Routes answers every list AT-10 makes with the fixture: tenant-a-quota, tenant-a-limits,
// Deployment api and its HPA.
func at10Routes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		quotasPath:       listOf(fixtureQuota()),
		limitRangesPath:  listOf(fixtureLimits()),
		deploymentsPath:  listOf(api()),
		statefulSetsPath: listOf[appsv1.StatefulSet](),
		hpasPath:         listOf(apiHPA()),
	}
}

func TestAT10TheFixtureFits(t *testing.T) {
	spec := at10Spec()
	got := at10(t.Context(), configClient(t, at10Routes()), spec, ignore)
	want := configResult(spec, report.Pass, "config", estimate+"peak fits tenant-a-quota: requests.cpu 500m of 4, requests.memory 640Mi of 8Gi, limits.cpu 2500m of 8, limits.memory 2560Mi of 16Gi, pods 5 of 20 "+
		"(deployments: api 4->5 (HPA max)); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3", "")
	want.Note = notCounted
	if len(got) != 1 || got[0] != want {
		t.Errorf("at10() = %+v,\nwant %+v", got, want)
	}
}

func TestAT10(t *testing.T) {
	spec := at10Spec()
	worker := api()
	worker.Name, worker.Spec.Template.Spec.Containers[0].Resources.Requests = "worker", rl("cpu", "1", "memory", "128Mi")
	tight := fixtureQuota()
	tight.Spec.Hard = rl("cpu", "4", "pods", "20")
	tight.Status.Used = rl("cpu", "300m")
	cpuCap := func(cpu string) corev1.ResourceQuota { // a second unscoped quota that sets requests.cpu only, no used values yet
		q := quota("cpu-cap")
		q.Spec.Hard = rl("requests.cpu", cpu)
		return q
	}
	unsized := api()
	unsized.Spec.Template.Spec.Containers = append(unsized.Spec.Template.Spec.Containers, corev1.Container{Name: "sidecar"})
	one := int32(1)
	twoUnsized := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "batch"}, Spec: appsv1.DeploymentSpec{Replicas: &one,
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "a"}, {Name: "b"}}}}}}
	gvisor := api()
	runtimeClass := "gvisor"
	gvisor.Spec.Template.Spec.RuntimeClassName = &runtimeClass
	db := appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db"}, Spec: appsv1.StatefulSetSpec{
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "db",
			Resources: corev1.ResourceRequirements{Limits: rl("cpu", "1", "memory", "1Gi")}}}}}}}
	tests := []struct {
		name   string
		routes map[string]http.HandlerFunc
		want   report.Result
		note   string
	}{
		{"no quota", map[string]http.HandlerFunc{quotasPath: listOf[corev1.ResourceQuota]()},
			configResult(spec, report.Fail, "config", estimate+"no ResourceQuota to compare against", "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods (see AT-06)"), notCounted},
		{"scoped quotas only", map[string]http.HandlerFunc{quotasPath: listOf(scoped(quota("long-running", fiveKeys...)))},
			configResult(spec, report.Fail, "config", estimate+"no ResourceQuota to compare against (scoped: long-running)", "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods (see AT-06)"), notCounted},
		{"an unscoped quota with none of the five keys has nothing to compare", map[string]http.HandlerFunc{quotasPath: listOf(quota("configmaps-cap", "count/configmaps"))},
			configResult(spec, report.Fail, "config", estimate+"no ResourceQuota to compare against", "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods (see AT-06)"), notCounted},
		{"no workloads", map[string]http.HandlerFunc{quotasPath: listOf(fixtureQuota()), deploymentsPath:listOf[appsv1.Deployment](), statefulSetsPath: listOf[appsv1.StatefulSet]()},
			configResult(spec, report.Pass, "config", estimate+"no Deployments or StatefulSets; nothing to compare", ""), notCounted},
		{"two workloads over the quota", map[string]http.HandlerFunc{deploymentsPath: listOf(api(), worker), hpasPath: listOf[autoscalingv2.HorizontalPodAutoscaler](), quotasPath: listOf(tight)},
			configResult(spec, report.Fail, "config", estimate+"tenant-a-quota cpu: peak needs 4400m, hard is 4, short by 400m (deployments: api 3->4, worker 3->4); used in tenant-a-quota: cpu 300m", at10FailHint), notCounted},
		{"a quota with only some keys compares only those", map[string]http.HandlerFunc{quotasPath: listOf(cpuCap("1"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 500m of 1 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", ""), notCounted},
		{"every unscoped quota is compared", map[string]http.HandlerFunc{quotasPath: listOf(fixtureQuota(), cpuCap("400m"), scoped(quota("long-running", "pods")))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap requests.cpu: peak needs 500m, hard is 400m, short by 100m (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3", at10FailHint), notCounted},
		{"HPAs unreadable: SKIP", map[string]http.HandlerFunc{hpasPath: forbidden("horizontalpodautoscalers")},
			configResult(spec, report.Skip, "-", estimate+"cannot list horizontalpodautoscalers (forbidden), so the peak is incomplete; the partial peak fits tenant-a-quota: requests.cpu 400m of 4, requests.memory 512Mi of 8Gi, limits.cpu 2 of 8, limits.memory 2Gi of 16Gi, pods 4 of 20 (deployments: api 3->4); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3",
				"grant `list` on horizontalpodautoscalers in tenant-a to this identity, or run with --as <admin>"), notCounted},
		{"HPAs unreadable but the partial peak is already over: FAIL", map[string]http.HandlerFunc{hpasPath: forbidden("horizontalpodautoscalers"), quotasPath: listOf(cpuCap("300m"))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap requests.cpu: peak needs 400m, hard is 300m, short by 100m (deployments: api 3->4); used in cpu-cap: none reported; partial: cannot list horizontalpodautoscalers (forbidden)", at10FailHint), notCounted},
		{"deployments unreadable and no StatefulSets is not 'no workloads'", map[string]http.HandlerFunc{deploymentsPath: forbidden("deployments")},
			configResult(spec, report.Skip, "-", estimate+"cannot list deployments (forbidden), so the peak is incomplete; the partial peak fits tenant-a-quota: requests.cpu 0 of 4, requests.memory 0 of 8Gi, limits.cpu 0 of 8, limits.memory 0 of 16Gi, pods 0 of 20 (no workloads counted); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3",
				"grant `list` on deployments in tenant-a to this identity, or run with --as <admin>"), notCounted},
		{"quotas unreadable", map[string]http.HandlerFunc{quotasPath: forbidden("resourcequotas")},
			configResult(spec, report.Skip, "-", estimate+"cannot list resourcequotas (forbidden), so there is nothing to compare against", "grant `list` on resourcequotas in tenant-a to this identity, or run with --as <admin>"), notCounted},
		{"LimitRanges unreadable while a container has no sizes: SKIP", map[string]http.HandlerFunc{limitRangesPath: forbidden("limitranges"), deploymentsPath: listOf(unsized)},
			configResult(spec, report.Skip, "-", estimate+"cannot list limitranges (forbidden), so the peak is incomplete; the partial peak fits tenant-a-quota: requests.cpu 500m of 4, requests.memory 640Mi of 8Gi, limits.cpu 2500m of 8, limits.memory 2560Mi of 16Gi, pods 5 of 20 (deployments: api 4->5 (HPA max)); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3",
				"grant `list` on limitranges in tenant-a to this identity, or run with --as <admin>"), notCounted + "; 1 container had no request or limit; counted as 0"},
		{"LimitRanges unreadable but every container is sized: PASS", map[string]http.HandlerFunc{limitRangesPath: forbidden("limitranges")},
			configResult(spec, report.Pass, "config", estimate+"peak fits tenant-a-quota: requests.cpu 500m of 4, requests.memory 640Mi of 8Gi, limits.cpu 2500m of 8, limits.memory 2560Mi of 16Gi, pods 5 of 20 (deployments: api 4->5 (HPA max)); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3", ""), notCounted},
		{"LimitRange defaults are named", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 1 of 4 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; defaults from LimitRange tenant-a-limits for 1 container", ""), notCounted},
		{"no LimitRange defaults: unsized containers count as 0", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 500m of 4 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", ""), notCounted + "; 1 container had no request or limit; counted as 0"},
		{"a RuntimeClass's overhead is not counted", map[string]http.HandlerFunc{deploymentsPath: listOf(gvisor), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 500m of 4 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", ""), notCounted + "; pod overhead of a RuntimeClass (api)"},
		{"StatefulSets count", map[string]http.HandlerFunc{statefulSetsPath: listOf(db), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 1500m of 4 (deployments: api 4->5 (HPA max); statefulsets: db 1->1); used in cpu-cap: none reported", ""), notCounted},
		{"a server error", map[string]http.HandlerFunc{statefulSetsPath: testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out")},
			configResult(spec, report.Error, "-", "could not list statefulsets: etcdserver: request timed out", hintRequestFailed), ""},
		{"a server error on the HPAs", map[string]http.HandlerFunc{hpasPath: testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out")},
			configResult(spec, report.Error, "-", "could not list horizontalpodautoscalers: etcdserver: request timed out", hintRequestFailed), ""},
		{"a server error on the LimitRanges", map[string]http.HandlerFunc{limitRangesPath: testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out")},
			configResult(spec, report.Error, "-", "could not list limitranges: etcdserver: request timed out", hintRequestFailed), ""},
		{"several unsized containers", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized, twoUnsized), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 500m of 4 (deployments: api 4->5 (HPA max), batch 1->2); used in cpu-cap: none reported", ""), notCounted + "; 3 containers had no request or limit; counted as 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes := at10Routes()
			for k, v := range tt.routes {
				routes[k] = v
			}
			tt.want.Note = tt.note
			got := at10(t.Context(), configClient(t, routes), spec, ignore)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at10() = %+v,\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestAT10NeedsNoHPAsWithoutWorkloads(t *testing.T) {
	routes := at10Routes()
	routes[deploymentsPath] = listOf[appsv1.Deployment]()
	c := configClient(t, routes)
	at10(t.Context(), c, at10Spec(), ignore)
	for _, r := range c.Recorder.Requests() {
		if strings.Contains(r.Path, "horizontalpodautoscalers") {
			t.Errorf("listed HPAs although no workload is counted: %s", r.Path)
		}
	}
}

func TestAT10LeavesAnEndedRunToTheRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	routes := at10Routes()
	routes[deploymentsPath] = testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "context canceled")
	routes[quotasPath] = testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "context canceled")
	if got := at10(ctx, configClient(t, routes), at10Spec(), ignore); len(got) != 0 {
		t.Errorf("at10() = %+v; want no result, so the runner says why the run ended", got)
	}
}
