package checks

import (
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at07Spec() Spec { return Registry[6] }

const limitRangesPath = "GET /api/v1/namespaces/tenant-a/limitranges"

// limitItem is one LimitRange item of the type; each "default.cpu"-style key sets that value to 1.
func limitItem(typ corev1.LimitType, keys ...string) corev1.LimitRangeItem {
	item := corev1.LimitRangeItem{Type: typ}
	for _, k := range keys {
		field, name, _ := strings.Cut(k, ".")
		list := map[string]*corev1.ResourceList{"default": &item.Default, "defaultRequest": &item.DefaultRequest, "min": &item.Min, "max": &item.Max}[field]
		if *list == nil {
			*list = corev1.ResourceList{}
		}
		(*list)[corev1.ResourceName(name)] = resource.MustParse("1")
	}
	return item
}

func limitRange(name string, items ...corev1.LimitRangeItem) corev1.LimitRange {
	return corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tenant-a"}, Spec: corev1.LimitRangeSpec{Limits: items}}
}

var fourDefaults = []string{"default.cpu", "default.memory", "defaultRequest.cpu", "defaultRequest.memory"}

func TestAT07(t *testing.T) {
	spec := at07Spec()
	const failHint = "add a LimitRange with a type: Container item that sets default and defaultRequest for cpu and memory, so containers without sizes get them"
	tests := []struct {
		name   string
		answer http.HandlerFunc
		want   report.Result
	}{
		{"a Container item with the four defaults", listOf(limitRange("tenant-a-limits", limitItem(corev1.LimitTypeContainer, fourDefaults...))),
			configResult(spec, report.Pass, "config", "LimitRange tenant-a-limits gives containers default and defaultRequest for cpu and memory", "")},
		{"no LimitRange", listOf[corev1.LimitRange](),
			configResult(spec, report.Fail, "config", "no LimitRange in tenant-a", failHint)},
		{"a Pod item only", listOf(limitRange("pod-max", limitItem(corev1.LimitTypePod, "max.cpu", "max.memory"))),
			configResult(spec, report.Fail, "config", "no LimitRange has a type: Container item (pod-max)", failHint)},
		{"missing defaults", listOf(limitRange("cpu-only", limitItem(corev1.LimitTypeContainer, "default.cpu", "defaultRequest.cpu"))),
			configResult(spec, report.Fail, "config", "no Container item sets default and defaultRequest for cpu and memory: cpu-only lacks default.memory, defaultRequest.memory", failHint)},
		{"several LimitRanges, the defaults split across them", listOf(
			limitRange("b-requests", limitItem(corev1.LimitTypeContainer, "defaultRequest.cpu", "defaultRequest.memory")),
			limitRange("a-limits", limitItem(corev1.LimitTypePod, "max.cpu"), limitItem(corev1.LimitTypeContainer, "default.cpu", "default.memory"))),
			configResult(spec, report.Fail, "config", "no Container item sets default and defaultRequest for cpu and memory: a-limits lacks defaultRequest.cpu, defaultRequest.memory; b-requests lacks default.cpu, default.memory", failHint)},
		{"several LimitRanges, one complete", listOf(
			limitRange("b-full", limitItem(corev1.LimitTypeContainer, fourDefaults...)),
			limitRange("a-partial", limitItem(corev1.LimitTypeContainer, "default.cpu"))),
			configResult(spec, report.Pass, "config", "LimitRange b-full gives containers default and defaultRequest for cpu and memory", "")},
		{"forbidden", forbidden("limitranges"),
			configResult(spec, report.Skip, "-", "cannot list limitranges in tenant-a (forbidden)", "grant `list` on limitranges in tenant-a to this identity, or run with --as <admin>")},
		{"a server error", testutil.Status(http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable, "the server is currently unable to handle the request"),
			configResult(spec, report.Error, "-", "could not list limitranges: the server is currently unable to handle the request", hintRequestFailed)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := at07(t.Context(), configClient(t, map[string]http.HandlerFunc{limitRangesPath: tt.answer}), spec, ignore)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at07() = %+v,\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestAT06And07ShareTheRunsLists(t *testing.T) {
	// One LIST per kind per run (DESIGN.md section 4): AT-04's sizing, AT-06, AT-07 and AT-10 read the cache.
	srv := map[string]http.HandlerFunc{quotasPath: listOf(quota("q", fiveKeys...)), limitRangesPath: listOf(limitRange("l", limitItem(corev1.LimitTypeContainer, fourDefaults...)))}
	c := configClient(t, srv)
	for range 2 {
		at06(t.Context(), c, at06Spec(), ignore)
		at07(t.Context(), c, at07Spec(), ignore)
	}
	n := 0
	for _, r := range c.Recorder.Requests() {
		if r.Method == http.MethodGet && (strings.Contains(r.Path, "/resourcequotas") || strings.Contains(r.Path, "/limitranges")) {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d LISTs of resourcequotas and limitranges, want one each per run", n)
	}
}
