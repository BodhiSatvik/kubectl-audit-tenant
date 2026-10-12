package checks

import (
	"slices"
	"testing"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

func at03Spec() Spec { return Registry[2] }

// at03Denied answers every AT-03 question with a denial.
func at03Denied() map[string]string {
	return map[string]string{
		"list nodes":            denied, // startup's question
		"escalate clusterroles": denied, "bind clusterroles": denied,
		"impersonate users": denied, "impersonate groups": denied, "impersonate serviceaccounts": denied, "* *": denied,
	}
}

func TestAT03PassAsksTheSixClusterScopedQuestionsAndNamesWhatIsNotTested(t *testing.T) {
	srv := &ssarServer{answers: at03Denied()}
	got := at03(t.Context(), connect(t, srv), at03Spec(), ignore)

	want := report.Result{ID: "AT-03", Name: "No privilege escalation", Kind: report.Behavioral, MTB: "MTB-PL2-CC-TI-1", Severity: report.High,
		Verdict: report.Pass, Layer: "RBAC",
		Reason: "RBAC denied all 6 actions; not tested: rights that stay inside the namespace (a namespace-wide *, escalate and bind on its roles, impersonating its service accounts), and grants limited to named roles or users (resourceNames)"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("at03() = %+v,\nwant %+v", got, want)
	}
	const rbac = "rbac.authorization.k8s.io"
	wantAsked := []cluster.Attributes{
		{Verb: "escalate", Group: rbac, Resource: "clusterroles"},
		{Verb: "bind", Group: rbac, Resource: "clusterroles"},
		{Verb: "impersonate", Resource: "users"},
		{Verb: "impersonate", Resource: "groups"},
		{Verb: "impersonate", Resource: "serviceaccounts"},
		{Verb: "*", Group: "*", Resource: "*"},
	}
	if !slices.Equal(srv.asked[1:], wantAsked) { // asked[0] is startup's question
		t.Errorf("asked %+v,\nwant %+v", srv.asked[1:], wantAsked)
	}
}

func TestAT03FailNamesEveryGrantAndTheAdminSignal(t *testing.T) {
	answers := at03Denied()
	answers["impersonate groups"], answers["* *"] = allowed, allowed
	got := at03(t.Context(), connect(t, &ssarServer{answers: answers}), at03Spec(), ignore)

	want := report.Result{ID: "AT-03", Name: "No privilege escalation", Kind: report.Behavioral, MTB: "MTB-PL2-CC-TI-1", Severity: report.High,
		Verdict: report.Fail, Layer: "RBAC",
		Reason: "RBAC allows impersonate groups, every verb on every resource cluster-wide (*)",
		Hint:   "remove the escalate, bind, impersonate and wildcard grants from this identity's ClusterRoleBindings; a tenant admin needs only a Role inside its namespace"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("at03() = %+v,\nwant %+v", got, want)
	}
	if !LooksLikeAdmin(got) {
		t.Error("LooksLikeAdmin() = false for an allowed cluster-wide wildcard")
	}
}

func TestLooksLikeAdminNeedsTheWildcard(t *testing.T) {
	answers := at03Denied()
	answers["impersonate users"] = allowed
	if got := at03(t.Context(), connect(t, &ssarServer{answers: answers}), at03Spec(), ignore); got[0].Verdict != report.Fail || LooksLikeAdmin(got) {
		t.Errorf("at03() = %+v; want FAIL without the admin signal: impersonation alone is not cluster admin", got)
	}
	if LooksLikeAdmin(at03(t.Context(), connect(t, &ssarServer{answers: at03Denied()}), at03Spec(), ignore)) {
		t.Error("LooksLikeAdmin() = true for a PASS")
	}
}
