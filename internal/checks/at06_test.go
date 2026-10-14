package checks

import (
	"context"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at06Spec() Spec { return Registry[5] }

const quotasPath = "GET /api/v1/namespaces/tenant-a/resourcequotas"

// quota is a ResourceQuota whose hard sets each key to 1.
func quota(name string, keys ...string) corev1.ResourceQuota {
	q := corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tenant-a"}, Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{}}}
	for _, k := range keys {
		q.Spec.Hard[corev1.ResourceName(k)] = resource.MustParse("1")
	}
	return q
}

func scoped(q corev1.ResourceQuota) corev1.ResourceQuota {
	q.Spec.Scopes = []corev1.ResourceQuotaScope{corev1.ResourceQuotaScopeNotTerminating}
	return q
}

func selected(q corev1.ResourceQuota) corev1.ResourceQuota {
	q.Spec.ScopeSelector = &corev1.ScopeSelector{MatchExpressions: []corev1.ScopedResourceSelectorRequirement{
		{ScopeName: corev1.ResourceQuotaScopePriorityClass, Operator: corev1.ScopeSelectorOpIn, Values: []string{"high"}},
	}}
	return q
}

var fiveKeys = []string{"requests.cpu", "requests.memory", "limits.cpu", "limits.memory", "pods"}

// configResult is the result a config check returns, built from the check's own spec.
func configResult(spec Spec, v report.Verdict, layer, reason, hint string) report.Result {
	return spec.result(spec.Results[0], v, layer, reason, hint)
}

// configClient connects with the config check's LIST routes; SSARs are not asked.
func configClient(t *testing.T, routes map[string]http.HandlerFunc) *cluster.Client {
	t.Helper()
	return connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied}}, routes)
}

func forbidden(resource string) http.HandlerFunc {
	return testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden,
		resource+` is forbidden: User "system:serviceaccount:tenant-a:tenant-a-dev" cannot list resource "`+resource+`" in API group "" in the namespace "tenant-a"`)
}

func TestAT06(t *testing.T) {
	spec := at06Spec()
	const failHint = "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods"
	tests := []struct {
		name   string
		answer http.HandlerFunc
		want   report.Result
	}{
		{"one unscoped quota with the five keys", listOf(quota("tenant-a-quota", fiveKeys...)),
			configResult(spec, report.Pass, "config", "ResourceQuota tenant-a-quota has no scopes and sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods", "")},
		{"the aliases cpu, memory and count/pods count", listOf(quota("short", "cpu", "memory", "limits.cpu", "limits.memory", "count/pods")),
			configResult(spec, report.Pass, "config", "ResourceQuota short has no scopes and sets cpu, memory, limits.cpu, limits.memory and count/pods", "")},
		{"no quota", listOf[corev1.ResourceQuota](),
			configResult(spec, report.Fail, "config", "no ResourceQuota in tenant-a", failHint)},
		{"scoped quotas only", listOf(scoped(quota("long-running", fiveKeys...)), selected(quota("high-priority", fiveKeys...))),
			configResult(spec, report.Fail, "config", "no ResourceQuota without scopes in tenant-a; a scoped quota covers only some pods (scoped: high-priority, long-running)", failHint)},
		{"missing keys", listOf(quota("requests-only", "requests.cpu", "requests.memory"), quota("pods-only", "pods")),
			configResult(spec, report.Fail, "config", "no ResourceQuota without scopes sets all five keys: pods-only lacks requests.cpu, requests.memory, limits.cpu, limits.memory; requests-only lacks limits.cpu, limits.memory, pods", failHint)},
		{"missing keys next to a complete scoped quota", listOf(scoped(quota("long-running", fiveKeys...)), quota("cpu-only", "cpu", "limits.cpu")),
			configResult(spec, report.Fail, "config", "no ResourceQuota without scopes sets all five keys: cpu-only lacks requests.memory, limits.memory, pods (scoped: long-running)", failHint)},
		{"the first complete quota by name passes", listOf(quota("zz-full", fiveKeys...), quota("partial", "pods"), quota("aa-full", fiveKeys...)),
			configResult(spec, report.Pass, "config", "ResourceQuota aa-full has no scopes and sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods", "")},
		{"forbidden", forbidden("resourcequotas"),
			configResult(spec, report.Skip, "-", "cannot list resourcequotas in tenant-a (forbidden)", "grant `list` on resourcequotas in tenant-a to this identity, or run with --as <admin>")},
		{"a server error", testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out"),
			configResult(spec, report.Error, "-", "could not list resourcequotas: etcdserver: request timed out", hintRequestFailed)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := at06(t.Context(), configClient(t, map[string]http.HandlerFunc{quotasPath: tt.answer}), spec, ignore)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at06() = %+v,\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestConfigListsLeaveAFailedListToTheRunnerWhenTheRunEnded(t *testing.T) {
	// The cached LIST runs under the run's context; when that ends, the runner says timeout or interrupted.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	broken := testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "context canceled")
	c := configClient(t, map[string]http.HandlerFunc{quotasPath: broken, limitRangesPath: broken, netpolPath: broken})
	for _, tc := range []struct {
		run  RunFunc
		spec Spec
	}{{at06, at06Spec()}, {at07, at07Spec()}, {at08, at08Spec()}} {
		if got := tc.run(ctx, c, tc.spec, ignore); len(got) != 0 {
			t.Errorf("%s = %+v; want no result, so the runner says why the run ended", tc.spec.ID, got)
		}
	}
}

func TestAndList(t *testing.T) {
	for _, tt := range []struct {
		items []string
		want  string
	}{{nil, ""}, {[]string{"a"}, "a"}, {[]string{"a", "b"}, "a and b"}, {[]string{"a", "b", "c"}, "a, b and c"}} {
		if got := andList(tt.items); got != tt.want {
			t.Errorf("andList(%q) = %q, want %q", tt.items, got, tt.want)
		}
	}
}
