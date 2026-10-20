package checks

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at09Spec() Spec { return Registry[8] }

// namespaceWith answers the startup GET of tenant-a with the labels, a JSON object body.
func namespaceWith(labels string) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{"GET /api/v1/namespaces/tenant-a": testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a","labels":`+labels+`}}`)}
}

// at04Passed is AT-04's five results, each a PASS by layer.
func at04Passed(layer string) []report.Result {
	var out []report.Result
	for _, r := range at04Spec().Results {
		out = append(out, at04Spec().result(r, report.Pass, layer, "rejected", ""))
	}
	return out
}

func TestAT09(t *testing.T) {
	const (
		failHint      = "kubectl label --overwrite namespace tenant-a pod-security.kubernetes.io/enforce=restricted (baseline also passes, but does not block AT-04.5)"
		missing       = "namespace tenant-a has no pod-security.kubernetes.io/enforce label"
		mayUpdateNote = "this identity may update namespace tenant-a, so it can change or remove this label"
	)
	denyBoth := map[string]string{"list nodes": denied, "update namespaces": denied, "patch namespaces": denied}
	spec := at09Spec()
	result := func(v report.Verdict, layer, reason, hint, note string) report.Result {
		r := configResult(spec, v, layer, reason, hint)
		r.Note = note
		return r
	}
	psaAndPolicy := at04Passed("admission (PSA)") // a baseline default blocks four probes; Kyverno blocks privilege escalation
	psaAndPolicy[4].Layer = "admission (webhook: validate.kyverno.svc-fail)"
	mixed := at04Passed("admission (PSA)")
	mixed[2].Verdict = report.Fail
	tests := []struct {
		name    string
		labels  string
		ssar    map[string]string
		earlier []report.Result
		want    report.Result
	}{
		{"restricted", `{"pod-security.kubernetes.io/enforce":"restricted"}`, denyBoth, nil,
			result(report.Pass, "config", "pod-security.kubernetes.io/enforce=restricted on namespace tenant-a", "", "")},
		{"baseline", `{"pod-security.kubernetes.io/enforce":"baseline","pod-security.kubernetes.io/enforce-version":"v1.37"}`, denyBoth, nil,
			result(report.Pass, "config", "pod-security.kubernetes.io/enforce=baseline on namespace tenant-a", "", "")},
		{"privileged", `{"pod-security.kubernetes.io/enforce":"privileged"}`, denyBoth, nil,
			result(report.Fail, "config", "pod-security.kubernetes.io/enforce=privileged on namespace tenant-a; only baseline or restricted blocks dangerous pods", failHint, "")},
		{"warn and audit labels only", `{"pod-security.kubernetes.io/warn":"restricted","pod-security.kubernetes.io/audit":"restricted"}`, denyBoth, nil,
			result(report.Fail, "config", missing, failHint, "")},
		{"no labels", `{}`, denyBoth, nil,
			result(report.Fail, "config", missing, failHint, "")},
		{"missing label, AT-04 shows PSA blocking every probe (a cluster default)", `{}`, denyBoth, at04Passed("admission (PSA)"),
			result(report.Fail, "config", missing+"; AT-04 shows PSA blocking every probe anyway, so a cluster-wide default level applies; the label is what states the level on the namespace", failHint, "")},
		{"missing label, AT-04 shows a policy engine blocking every probe", `{}`, denyBoth, at04Passed("admission (webhook: validate.kyverno.svc-fail)"),
			result(report.Fail, "config", missing+"; AT-04 shows admission (webhook: validate.kyverno.svc-fail) blocking every probe anyway, so the gap is the label: the built-in gate as a second fence", failHint, "")},
		{"missing label, in a full run: the other checks' results do not count", `{}`, denyBoth,
			slices.Concat([]report.Result{at01Spec().result(at01Spec().Results[0], report.Fail, "RBAC", "RBAC allows list nodes", "fix")}, at04Passed("admission (PSA)"),
				[]report.Result{at05Spec().result(at05Spec().Results[0], report.Pass, "admission (webhook: validate.kyverno.svc-fail)", "denied", "")}),
			result(report.Fail, "config", missing+"; AT-04 shows PSA blocking every probe anyway, so a cluster-wide default level applies; the label is what states the level on the namespace", failHint, "")},
		{"missing label, AT-04 with one FAIL says nothing about AT-04", `{}`, denyBoth, mixed,
			result(report.Fail, "config", missing, failHint, "")},
		{"missing label, an RBAC-only AT-04 PASS says nothing about AT-04", `{}`, denyBoth, at04Passed("RBAC"),
			result(report.Fail, "config", missing, failHint, "")},
		{"missing label, AT-04 shows PSA and a policy engine together: PSA proves a cluster default (D122)", `{}`, denyBoth, psaAndPolicy,
			result(report.Fail, "config", missing+"; AT-04 shows admission (PSA), admission (webhook: validate.kyverno.svc-fail) blocking every probe anyway, so a cluster-wide default level applies; the label is what states the level on the namespace", failHint, "")},
		{"missing label, AT-04 partly skipped cannot speak for every probe (D110)", `{}`, denyBoth, at04Passed("admission (PSA)")[:4],
			result(report.Fail, "config", missing, failHint, "")},
		{"restricted, but the identity may update its namespace (D105)", `{"pod-security.kubernetes.io/enforce":"restricted"}`,
			map[string]string{"list nodes": denied, "update namespaces": allowed}, nil,
			result(report.Pass, "config", "pod-security.kubernetes.io/enforce=restricted on namespace tenant-a", "", mayUpdateNote)},
		{"restricted, but the identity may patch its namespace (D105)", `{"pod-security.kubernetes.io/enforce":"restricted"}`,
			map[string]string{"list nodes": denied, "update namespaces": denied, "patch namespaces": allowed}, nil,
			result(report.Pass, "config", "pod-security.kubernetes.io/enforce=restricted on namespace tenant-a", "", "this identity may patch namespace tenant-a, so it can change or remove this label")},
		{"restricted, but the identity may patch its namespace's status, which changes labels too (D119)", `{"pod-security.kubernetes.io/enforce":"restricted"}`,
			map[string]string{"list nodes": denied, "update namespaces": denied, "patch namespaces": denied, "patch namespaces/status": allowed}, nil,
			result(report.Pass, "config", "pod-security.kubernetes.io/enforce=restricted on namespace tenant-a", "", "this identity may patch namespace tenant-a/status, so it can change or remove this label")},
		{"restricted, but the identity may update its namespace through finalize (D119)", `{"pod-security.kubernetes.io/enforce":"restricted"}`,
			map[string]string{"list nodes": denied, "update namespaces": denied, "patch namespaces": denied, "update namespaces/finalize": allowed}, nil,
			result(report.Pass, "config", "pod-security.kubernetes.io/enforce=restricted on namespace tenant-a", "", "this identity may update namespace tenant-a/finalize, so it can change or remove this label")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := spec
			s.earlier = tt.earlier
			got := at09(t.Context(), connectWith(t, &ssarServer{answers: tt.ssar}, namespaceWith(tt.labels)), s, ignore)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at09() = %+v,\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestAT09AsksAboutTheNamespaceObjectByName(t *testing.T) {
	// RBAC reads a request for the namespace object as a request inside it, so a Role with
	// resourceNames [tenant-a] can grant it; the question names the object to catch that grant.
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "update namespaces": denied, "patch namespaces": denied}}
	at09(t.Context(), connectWith(t, ssar, namespaceWith(`{"pod-security.kubernetes.io/enforce":"restricted"}`)), at09Spec(), ignore)
	want := []cluster.Attributes{ // the status and finalize subresources can change labels too (proven on kind, D119)
		{Verb: "update", Resource: "namespaces", Name: "tenant-a", Namespace: "tenant-a"},
		{Verb: "patch", Resource: "namespaces", Name: "tenant-a", Namespace: "tenant-a"},
		{Verb: "update", Resource: "namespaces", Subresource: "status", Name: "tenant-a", Namespace: "tenant-a"},
		{Verb: "patch", Resource: "namespaces", Subresource: "status", Name: "tenant-a", Namespace: "tenant-a"},
		{Verb: "update", Resource: "namespaces", Subresource: "finalize", Name: "tenant-a", Namespace: "tenant-a"},
	}
	if got := ssar.asked[1:]; !slices.Equal(got, want) { // asked[0] is startup's question
		t.Errorf("asked %+v, want %+v", got, want)
	}
}

func TestAT09AFailedQuestionIsANoteNotAVerdict(t *testing.T) {
	// D105: whether the identity may change the label is a note, so a question that fails leaves the PASS.
	c := connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied}}, namespaceWith(`{"pod-security.kubernetes.io/enforce":"restricted"}`))
	got := at09(t.Context(), c, at09Spec(), ignore)
	if len(got) != 1 || got[0].Verdict != report.Pass || !strings.HasPrefix(got[0].Note, `not tested: whether this identity may change this label: could not ask "may I update namespace tenant-a?"`) {
		t.Errorf("at09() = %+v; want PASS with a note naming the failed question", got)
	}
}

func TestAT09LeavesAnEndedRunToTheRunner(t *testing.T) {
	// D113: the run ends during the update question; the runner says timeout or interrupted, as for every check.
	c := connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied}}, namespaceWith(`{"pod-security.kubernetes.io/enforce":"restricted"}`))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := at09(ctx, c, at09Spec(), ignore); len(got) != 0 {
		t.Errorf("at09() = %+v; want no result, so the runner says why the run ended", got)
	}
}

func TestAT09ReadsTheStartupAnswer(t *testing.T) {
	spec := at09Spec()
	forbiddenNS := testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, `namespaces "tenant-a" is forbidden: User "team-a-dev" cannot get resource "namespaces" in API group "" in the namespace "tenant-a"`)
	c := connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied}}, map[string]http.HandlerFunc{"GET /api/v1/namespaces/tenant-a": forbiddenNS})
	want := configResult(spec, report.Skip, "-", "cannot read namespace labels (forbidden)", "grant `get` on namespace tenant-a to this identity, or run with --as <admin>")
	if got := at09(t.Context(), c, spec, ignore); len(got) != 1 || got[0] != want {
		t.Errorf("a forbidden namespace = %+v, want %+v", got, want)
	}
	// Startup exits 2 on any other error; a client that carries one anyway is ERROR, never a verdict.
	broken := &cluster.Client{Namespace: "tenant-a", NamespaceErr: errors.New("etcdserver: request timed out")}
	want = configResult(spec, report.Error, "-", "could not read namespace tenant-a: etcdserver: request timed out", hintRequestFailed)
	if got := at09(t.Context(), broken, spec, ignore); len(got) != 1 || got[0] != want {
		t.Errorf("another namespace error = %+v, want %+v", got, want)
	}
}

func TestTheRunnerHandsEachCheckTheResultsBeforeIt(t *testing.T) {
	var seen [][]string
	record := func(id string) Spec {
		s := fake(id, 1, allPass)
		run := s.Run
		s.Run = func(ctx context.Context, c *cluster.Client, s Spec, prove func(report.Result)) []report.Result {
			seen = append(seen, ids(s.earlier))
			return run(ctx, c, s, prove)
		}
		return s
	}
	Run(t.Context(), nil, []Spec{record("AT-01"), record("AT-04"), record("AT-09")}, time.Minute)
	if want := [][]string{nil, {"AT-01"}, {"AT-01", "AT-04"}}; !slices.EqualFunc(seen, want, slices.Equal) {
		t.Errorf("earlier results = %v, want %v", seen, want)
	}
}
