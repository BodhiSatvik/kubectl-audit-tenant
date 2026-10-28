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
	notCounted       = "the peak counts active pods of Deployments and StatefulSets during a rollout, at their template sizes; terminating pods can add to usage; not counted: other workloads (DaemonSets, Jobs, CronJobs, ReplicaSets, ReplicationControllers, bare Pods, Argo Rollouts) and sizes set at admission (VPA)"
	refusedHint      = "set cpu and memory requests and limits on every container, or give them defaults with a LimitRange (see AT-07); a webhook that sets them only on pods, not in the template, is not seen"
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
	memoryOnly := api() // its container sets memory only, so cpu comes from a LimitRange
	memoryOnly.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{Requests: rl("memory", "128Mi"), Limits: rl("memory", "512Mi")}
	requestsOnly := api() // its second container sets requests, no limits
	requestsOnly.Spec.Template.Spec.Containers = append(requestsOnly.Spec.Template.Spec.Containers,
		corev1.Container{Name: "sidecar", Resources: corev1.ResourceRequirements{Requests: rl("cpu", "100m", "memory", "64Mi")}})
	one := int32(1)
	twoUnsized := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "batch"}, Spec: appsv1.DeploymentSpec{Replicas: &one,
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "a"}, {Name: "b"}}}}}}
	podLevelCPU := api() // a pod-level cpu request; its container sets nothing
	podLevelCPU.Spec.Template.Spec.Resources = &corev1.ResourceRequirements{Requests: rl("cpu", "1")}
	podLevelCPU.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
	memCap := quota("mem-cap")
	memCap.Spec.Hard = rl("requests.memory", "1Gi")
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
			configResult(spec, report.Fail, "config", estimate+"no ResourceQuota to compare against", "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods; first make sure a LimitRange gives defaults (AT-07): with this quota, the API server refuses new pods that set no cpu or memory (see AT-06)"), notCounted},
		{"scoped quotas only", map[string]http.HandlerFunc{quotasPath: listOf(notTerminating(quota("long-running", fiveKeys...)))},
			configResult(spec, report.Fail, "config", estimate+"no ResourceQuota to compare against (scoped: long-running)", "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods; first make sure a LimitRange gives defaults (AT-07): with this quota, the API server refuses new pods that set no cpu or memory (see AT-06)"), notCounted},
		{"an unscoped quota with none of the five keys has nothing to compare", map[string]http.HandlerFunc{quotasPath: listOf(quota("configmaps-cap", "count/configmaps"))},
			configResult(spec, report.Fail, "config", estimate+"no ResourceQuota to compare against", "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods; first make sure a LimitRange gives defaults (AT-07): with this quota, the API server refuses new pods that set no cpu or memory (see AT-06)"), notCounted},
		{"an empty scopeSelector matches every pod, so the quota is compared (D110)", map[string]http.HandlerFunc{quotasPath: listOf(everyPod(fixtureQuota())), deploymentsPath: listOf[appsv1.Deployment](), statefulSetsPath: listOf[appsv1.StatefulSet]()},
			configResult(spec, report.Pass, "config", estimate+"no Deployments or StatefulSets; nothing to compare", ""), notCounted},
		{"no workloads", map[string]http.HandlerFunc{quotasPath: listOf(fixtureQuota()), deploymentsPath: listOf[appsv1.Deployment](), statefulSetsPath: listOf[appsv1.StatefulSet]()},
			configResult(spec, report.Pass, "config", estimate+"no Deployments or StatefulSets; nothing to compare", ""), notCounted},
		{"two workloads over the quota", map[string]http.HandlerFunc{deploymentsPath: listOf(api(), worker), hpasPath: listOf[autoscalingv2.HorizontalPodAutoscaler](), quotasPath: listOf(tight)},
			configResult(spec, report.Fail, "config", estimate+"tenant-a-quota cpu: peak needs 4400m, hard is 4, short by 400m (deployments: api 3->4, worker 3->4); used in tenant-a-quota: cpu 300m", at10FailHint), notCounted},
		{"a quota with only some keys compares only those", map[string]http.HandlerFunc{quotasPath: listOf(cpuCap("1"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 500m of 1 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", ""), notCounted},
		{"every unscoped quota is compared", map[string]http.HandlerFunc{quotasPath: listOf(fixtureQuota(), cpuCap("400m"), notTerminating(quota("long-running", "pods")))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap requests.cpu: peak needs 500m, hard is 400m, short by 100m (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3", at10FailHint), notCounted},
		{"HPAs unreadable: SKIP", map[string]http.HandlerFunc{hpasPath: forbidden("horizontalpodautoscalers")},
			configResult(spec, report.Skip, "-", estimate+"cannot list horizontalpodautoscalers (forbidden), so the peak is incomplete; the partial peak fits tenant-a-quota: requests.cpu 400m of 4, requests.memory 512Mi of 8Gi, limits.cpu 2 of 8, limits.memory 2Gi of 16Gi, pods 4 of 20 (deployments: api 3->4); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3",
				"grant `list` on horizontalpodautoscalers in tenant-a to this identity, or run with --as <admin>"), notCounted},
		{"HPAs unreadable and the partial peak is over: SKIP, since an HPA can lower replicas (D110)", map[string]http.HandlerFunc{hpasPath: forbidden("horizontalpodautoscalers"), quotasPath: listOf(cpuCap("300m"))},
			configResult(spec, report.Skip, "-", estimate+"cannot list horizontalpodautoscalers (forbidden), so the peak is incomplete; an HPA can lower replicas, so the partial shortfall is not proven: cpu-cap requests.cpu: peak needs 400m, hard is 300m, short by 100m (deployments: api 3->4); used in cpu-cap: none reported",
				"grant `list` on horizontalpodautoscalers in tenant-a to this identity, or run with --as <admin>"), notCounted},
		// Value: protects=a partial peak over hard is FAIL while LimitRanges are unreadable (DESIGN.md s5); fails_when=an unset tracked value with unread LimitRanges SKIPs before the shortfall; why_new=every forbidden-LimitRange row fits; seam=none; no_claim=none beyond protects
		{"LimitRanges unreadable but the partial peak is already over: FAIL", map[string]http.HandlerFunc{limitRangesPath: forbidden("limitranges"), deploymentsPath: listOf(unsized), quotasPath: listOf(cpuCap("300m"))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap requests.cpu: peak needs 500m, hard is 300m, short by 200m (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; partial: cannot list limitranges (forbidden)", at10FailHint), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		{"StatefulSets unreadable but the partial peak is already over: FAIL", map[string]http.HandlerFunc{statefulSetsPath: forbidden("statefulsets"), quotasPath: listOf(cpuCap("300m"))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap requests.cpu: peak needs 500m, hard is 300m, short by 200m (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; partial: cannot list statefulsets (forbidden)", at10FailHint), notCounted},
		{"deployments unreadable and no StatefulSets is not 'no workloads'", map[string]http.HandlerFunc{deploymentsPath: forbidden("deployments")},
			configResult(spec, report.Skip, "-", estimate+"cannot list deployments (forbidden), so the peak is incomplete; the partial peak fits tenant-a-quota: requests.cpu 0 of 4, requests.memory 0 of 8Gi, limits.cpu 0 of 8, limits.memory 0 of 16Gi, pods 0 of 20 (no workloads counted); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3",
				"grant `list` on deployments in tenant-a to this identity, or run with --as <admin>"), notCounted},
		{"quotas unreadable", map[string]http.HandlerFunc{quotasPath: forbidden("resourcequotas")},
			configResult(spec, report.Skip, "-", estimate+"cannot list resourcequotas (forbidden), so there is nothing to compare against", "grant `list` on resourcequotas in tenant-a to this identity, or run with --as <admin>"), notCounted},
		{"LimitRanges unreadable while a container has no sizes: SKIP", map[string]http.HandlerFunc{limitRangesPath: forbidden("limitranges"), deploymentsPath: listOf(unsized)},
			configResult(spec, report.Skip, "-", estimate+"cannot list limitranges (forbidden), so the peak is incomplete; the partial peak fits tenant-a-quota: requests.cpu 500m of 4, requests.memory 640Mi of 8Gi, limits.cpu 2500m of 8, limits.memory 2560Mi of 16Gi, pods 5 of 20 (deployments: api 4->5 (HPA max)); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3",
				"grant `list` on limitranges in tenant-a to this identity, or run with --as <admin>"), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		{"LimitRanges unreadable while only values no quota tracks are unset: PASS (D111)", map[string]http.HandlerFunc{limitRangesPath: forbidden("limitranges"), deploymentsPath: listOf(requestsOnly), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 1 of 4 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", ""), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		{"LimitRanges unreadable but every container is sized: PASS", map[string]http.HandlerFunc{limitRangesPath: forbidden("limitranges")},
			configResult(spec, report.Pass, "config", estimate+"peak fits tenant-a-quota: requests.cpu 500m of 4, requests.memory 640Mi of 8Gi, limits.cpu 2500m of 8, limits.memory 2560Mi of 16Gi, pods 5 of 20 (deployments: api 4->5 (HPA max)); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3", ""), notCounted},
		{"defaults merged from two LimitRanges name both (D112)", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), quotasPath: listOf(cpuCap("8")),
			limitRangesPath: listOf(limitRange("cpu-defaults", limitItem(corev1.LimitTypeContainer, "defaultRequest.cpu")), limitRange("mem-defaults", limitItem(corev1.LimitTypeContainer, "defaultRequest.memory")))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 5500m of 8 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; defaults from LimitRanges cpu-defaults and mem-defaults for 1 container", ""), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		{"only the LimitRange a container took a default from is named (D121)", map[string]http.HandlerFunc{deploymentsPath: listOf(memoryOnly), quotasPath: listOf(cpuCap("8")),
			limitRangesPath: listOf(limitRange("a-cpu", limitItem(corev1.LimitTypeContainer, "defaultRequest.cpu")), limitRange("b-mem", limitItem(corev1.LimitTypeContainer, "defaultRequest.memory")))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 5 of 8 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; defaults from LimitRange a-cpu for 1 container", ""), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		// Value: protects=AT-10 names the range a container took a limit default from, not one whose defaultRequest it did not use (D121); fails_when=PodOf stops recording limitFrom or records requestFrom for a limit; why_new=every naming row is masked by the request side; seam=none; no_claim=pod-level resources
		{"a container that takes only limit defaults names that LimitRange (D121)", map[string]http.HandlerFunc{deploymentsPath: listOf(requestsOnly), quotasPath: listOf(cpuCap("8")),
			limitRangesPath: listOf(limitRange("a-req", limitItem(corev1.LimitTypeContainer, "defaultRequest.cpu", "defaultRequest.memory")), limitRange("b-lim", limitItem(corev1.LimitTypeContainer, "default.cpu", "default.memory")))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 1 of 8 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; defaults from LimitRange b-lim for 1 container", ""), notCounted},
		// Value: protects=a workload with several HPAs counts spec.replicas and the note names those HPAs (D126); fails_when=workload() goes back to the largest maxReplicas, or at10 drops the note clause; why_new=headroom tests do not reach the AT-10 note; seam=none; no_claim=none beyond protects
		{"two HPAs on one workload: the controller scales none, and the note says so (D126)", map[string]http.HandlerFunc{hpasPath: listOf(apiHPA(), func() autoscalingv2.HorizontalPodAutoscaler { h := apiHPA(); h.Name = "api-mem"; return h }())},
			configResult(spec, report.Pass, "config", estimate+"peak fits tenant-a-quota: requests.cpu 400m of 4, requests.memory 512Mi of 8Gi, limits.cpu 2 of 8, limits.memory 2Gi of 16Gi, pods 4 of 20 (deployments: api 3->4); used in tenant-a-quota: requests.cpu 300m, requests.memory 384Mi, limits.cpu 1500m, limits.memory 1536Mi, pods 3", ""),
			notCounted + "; several HPAs target deployment api (api, api-mem); the HPA controller scales none of them (AmbiguousSelector)"},
		// Value: protects=a PASS reason shows the peak for a quota spelled with the cpu alias; fails_when=the fits loop reads total[key] and prints "cpu 0 of 4"; why_new=every PASS and SKIP row uses canonical keys; seam=none; no_claim=none beyond protects
		{"a quota that spells cpu shows the peak in a PASS", map[string]http.HandlerFunc{quotasPath: listOf(tight)},
			configResult(spec, report.Pass, "config", estimate+"peak fits tenant-a-quota: cpu 500m of 4, pods 5 of 20 (deployments: api 4->5 (HPA max)); used in tenant-a-quota: cpu 300m", ""), notCounted},
		{"LimitRange defaults are named", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Pass, "config", estimate+"peak fits cpu-cap: requests.cpu 1 of 4 (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported; defaults from LimitRange tenant-a-limits for 1 container", ""), notCounted},
		{"no LimitRange defaults: a quota that tracks an unset value refuses the pods (D111)", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap refuses the pods of deployment api: a container sets no requests.cpu (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", refusedHint), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		// Value: protects=a quota spelling cpu (alias) refuses pods with no cpu request (D111); fails_when=tracked keys skip Canonical, so the alias tracks nothing and AT-10 PASSes; why_new=refusal rows use requests.cpu only; seam=none; no_claim=memory alias
		{"a quota that spells cpu refuses the pods too", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(tight)},
			configResult(spec, report.Fail, "config", estimate+"tenant-a-quota refuses the pods of deployment api: a container sets no requests.cpu (deployments: api 4->5 (HPA max)); used in tenant-a-quota: cpu 300m", refusedHint), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		// Value: protects=a refusal is FAIL while HPAs are unreadable: quota refuses each pod whatever the replicas; fails_when=unread HPAs turn every FAIL into SKIP; why_new=no row joins a refusal with a forbidden list; seam=none; no_claim=shortfall with unread HPAs (covered)
		{"HPAs unreadable: a refusal is still FAIL", map[string]http.HandlerFunc{hpasPath: forbidden("horizontalpodautoscalers"), deploymentsPath: listOf(unsized), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(cpuCap("4"))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap refuses the pods of deployment api: a container sets no requests.cpu (deployments: api 3->4); used in cpu-cap: none reported; partial: cannot list horizontalpodautoscalers (forbidden)", refusedHint), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		// Value: protects=no refusal for a pod that sets a pod-level value: quota admission skips "must specify" (D132); fails_when=PodOf lists Unset for such a pod; why_new=no AT-10 row sets pod-level resources; seam=none; no_claim=hugepages
		{"a pod-level cpu request skips the quota's must-specify check (D132)", map[string]http.HandlerFunc{deploymentsPath: listOf(podLevelCPU), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(memCap)},
			configResult(spec, report.Pass, "config", estimate+"peak fits mem-cap: requests.memory 0 of 1Gi (deployments: api 4->5 (HPA max)); used in mem-cap: none reported", ""), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		// Value: protects=LimitRanger still fills the containers of a pod with pod-level values, so unread LimitRanges SKIP (D132, Codex adversarial); fails_when=the must-specify exemption also hides the unset values from the forbidden-LimitRange SKIP; why_new=the D132 PASS row reads LimitRanges; seam=none; no_claim=none beyond protects
		{"LimitRanges unreadable while a pod-level pod leaves a tracked value unset: SKIP (D132)", map[string]http.HandlerFunc{deploymentsPath: listOf(podLevelCPU), limitRangesPath: forbidden("limitranges"), quotasPath: listOf(memCap)},
			configResult(spec, report.Skip, "-", estimate+"cannot list limitranges (forbidden), so the peak is incomplete; the partial peak fits mem-cap: requests.memory 0 of 1Gi (deployments: api 4->5 (HPA max)); used in mem-cap: none reported",
				"grant `list` on limitranges in tenant-a to this identity, or run with --as <admin>"), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
		{"a refused workload and a shortfall are both named", map[string]http.HandlerFunc{deploymentsPath: listOf(unsized), limitRangesPath: listOf[corev1.LimitRange](), quotasPath: listOf(cpuCap("100m"))},
			configResult(spec, report.Fail, "config", estimate+"cpu-cap refuses the pods of deployment api: a container sets no requests.cpu; cpu-cap requests.cpu: peak needs 500m, hard is 100m, short by 400m (deployments: api 4->5 (HPA max)); used in cpu-cap: none reported", refusedHint+"; "+at10FailHint), notCounted + "; 1 container left a cpu or memory request or limit unset; counted as 0"},
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
			configResult(spec, report.Fail, "config", estimate+"cpu-cap refuses the pods of deployment api: a container sets no requests.cpu; cpu-cap refuses the pods of deployment batch: a container sets no requests.cpu (deployments: api 4->5 (HPA max), batch 1->2); used in cpu-cap: none reported", refusedHint), notCounted + "; 3 containers left a cpu or memory request or limit unset; counted as 0"},
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
	routes[quotasPath] = testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "context canceled")
	if got := at10(ctx, configClient(t, routes), at10Spec(), ignore); len(got) != 0 {
		t.Errorf("at10() = %+v; want no result, so the runner says why the run ended", got)
	}
}

func TestAT10LeavesARunEndedDuringTheWorkloadListsToTheRunner(t *testing.T) {
	// In a default run AT-06 fills the quota cache first, so an interrupt lands on the deployments LIST.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	routes := at10Routes()
	routes[deploymentsPath] = func(_ http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }
	if got := at10(ctx, configClient(t, routes), at10Spec(), ignore); len(got) != 0 {
		t.Errorf("at10() = %+v; want no result when the run ends during the deployments LIST", got)
	}
}

func TestAT10NamesAtMostThreeRuntimeClassWorkloads(t *testing.T) {
	// Notes are never cut, so a namespace where every pod uses gVisor must not print every name (D114).
	runtimeClass := "gvisor"
	var ds []appsv1.Deployment
	for _, name := range []string{"a", "b", "c", "d"} {
		d := api()
		d.Name = name
		d.Spec.Template.Spec.RuntimeClassName = &runtimeClass
		ds = append(ds, d)
	}
	routes := at10Routes()
	routes[deploymentsPath] = listOf(ds...)
	routes[hpasPath] = listOf[autoscalingv2.HorizontalPodAutoscaler]()
	got := at10(t.Context(), configClient(t, routes), at10Spec(), ignore)
	if want := notCounted + "; pod overhead of a RuntimeClass (a, b, c and 1 more)"; len(got) != 1 || got[0].Note != want {
		t.Errorf("at10() = %+v;\nwant note %q", got, want)
	}
}

func TestAT10NamesAtMostThreeWorkloadsWithSeveralHPAs(t *testing.T) {
	// Notes are never cut (D114); a cpu and a memory HPA on every Deployment must not list them all.
	var ds []appsv1.Deployment
	var hs []autoscalingv2.HorizontalPodAutoscaler
	for _, n := range []string{"a", "b", "c", "d"} {
		d := api()
		d.Name = n
		ds = append(ds, d)
		for _, metric := range []string{"-cpu", "-mem"} {
			h := apiHPA()
			h.Name, h.Spec.ScaleTargetRef.Name = n+metric, n
			hs = append(hs, h)
		}
	}
	routes := at10Routes()
	routes[deploymentsPath], routes[hpasPath] = listOf(ds...), listOf(hs...)
	got := at10(t.Context(), configClient(t, routes), at10Spec(), ignore)
	want := notCounted + "; several HPAs target deployment a (a-cpu, a-mem), deployment b (b-cpu, b-mem), deployment c (c-cpu, c-mem) and 1 more; the HPA controller scales none of them (AmbiguousSelector)"
	if len(got) != 1 || got[0].Note != want {
		t.Errorf("at10() = %+v;\nwant note %q", got, want)
	}
}
