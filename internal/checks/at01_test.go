package checks

import (
	"slices"
	"strings"
	"testing"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

func TestSSARReadsAnswersAsTheAPIDefines(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		allowed bool
		denied  bool
		wantErr string
	}{
		{"allowed", allowed, true, false, ""},
		{"allowed false is denied", denied, false, false, ""},
		{"explicit deny", `{"allowed":false,"denied":true}`, false, true, ""},
		{"evaluationError is an error with the server text", `{"allowed":false,"evaluationError":"webhook timed out"}`, false, false, "webhook timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ssarServer{answers: map[string]string{"list nodes": denied, "get pods": tt.status}}
			c := connect(t, srv)
			got, err := can(t.Context(), c, "get", "", "pods", "tenant-a")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("can() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.allowed != tt.allowed || got.denied != tt.denied {
				t.Errorf("can() = %+v, %v; want allowed=%v denied=%v", got, err, tt.allowed, tt.denied)
			}
			if asked := srv.asked[len(srv.asked)-1]; asked != (cluster.Attributes{Verb: "get", Resource: "pods", Namespace: "tenant-a"}) {
				t.Errorf("asked %+v, want get pods in tenant-a", asked)
			}
		})
	}
	t.Run("a failed request is an error", func(t *testing.T) {
		c := connect(t, &ssarServer{answers: map[string]string{"list nodes": denied}})
		if _, err := can(t.Context(), c, "get", "", "pods", "tenant-a"); err == nil {
			t.Error("can() with an HTTP 500 answer should fail")
		}
	})
}

func at01Spec() Spec { return Registry[0] }

func TestAT01PassWhenAllFourActionsAreDenied(t *testing.T) {
	srv := &ssarServer{answers: map[string]string{"list nodes": denied, "create namespaces": denied, "create clusterrolebindings": denied, "create persistentvolumes": denied}}
	c := connect(t, srv)

	got := at01(t.Context(), c, at01Spec())

	want := report.Result{ID: "AT-01", Name: "No cluster-scoped access", Kind: report.Behavioral, MTB: "MTB-PL1-CC-CPI-1", Severity: report.High,
		Verdict: report.Pass, Layer: "RBAC", Reason: "RBAC denied all 4 actions"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("at01() = %+v, want %+v", got, want)
	}
	wantAsked := []cluster.Attributes{
		{Verb: "list", Resource: "nodes"},
		{Verb: "create", Resource: "namespaces"},
		{Verb: "create", Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings"},
		{Verb: "create", Resource: "persistentvolumes"},
	}
	if !slices.Equal(srv.asked, wantAsked) {
		t.Errorf("asked %+v, want the four cluster-scoped questions once each (startup's answer reused)", srv.asked)
	}
}

func TestAT01FailNamesTheAllowedActions(t *testing.T) {
	srv := &ssarServer{answers: map[string]string{"list nodes": denied, "create namespaces": allowed, "create clusterrolebindings": allowed, "create persistentvolumes": denied}}
	got := at01(t.Context(), connect(t, srv), at01Spec())[0]
	if got.Verdict != report.Fail || got.Layer != "RBAC" || got.Hint == "" {
		t.Errorf("at01() = %+v, want FAIL on layer RBAC with a hint", got)
	}
	for _, action := range []string{"create namespaces", "create clusterrolebindings"} {
		if !strings.Contains(got.Reason, action) {
			t.Errorf("reason %q should name %q", got.Reason, action)
		}
	}
	if strings.Contains(got.Reason, "nodes") {
		t.Errorf("reason %q should not name a denied action", got.Reason)
	}
}

func TestAT01NotesAnExplicitDeny(t *testing.T) {
	srv := &ssarServer{answers: map[string]string{"list nodes": `{"allowed":false,"denied":true}`, "create namespaces": denied, "create clusterrolebindings": denied, "create persistentvolumes": denied}}
	got := at01(t.Context(), connect(t, srv), at01Spec())[0]
	if got.Verdict != report.Pass || !strings.Contains(got.Reason, "explicit deny") || !strings.Contains(got.Reason, "list nodes") {
		t.Errorf("at01() = %+v, want PASS whose reason notes the explicit deny of list nodes", got)
	}
}

func TestAT01ErrorsCarryTheServerTextAndAHint(t *testing.T) {
	for name, answers := range map[string]map[string]string{
		"evaluationError": {"list nodes": denied, "create namespaces": denied, "create clusterrolebindings": `{"allowed":false,"evaluationError":"authorizer webhook unavailable"}`},
		"request failure": {"list nodes": denied, "create namespaces": denied},
	} {
		t.Run(name, func(t *testing.T) {
			got := at01(t.Context(), connect(t, &ssarServer{answers: answers}), at01Spec())[0]
			if got.Verdict != report.Error || got.Hint == "" || !strings.Contains(got.Reason, "clusterrolebindings") {
				t.Errorf("at01() = %+v, want ERROR naming the failed question with a hint", got)
			}
			if name == "evaluationError" && !strings.Contains(got.Reason, "authorizer webhook unavailable") {
				t.Errorf("reason %q should carry the server text", got.Reason)
			}
		})
	}
}
