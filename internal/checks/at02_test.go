package checks

import (
	"slices"
	"testing"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

func at02Spec() Spec { return Registry[1] }

func TestAT02(t *testing.T) {
	base := report.Result{ID: "AT-02", Name: "No cross-namespace secret access", Kind: report.Behavioral, MTB: "MTB-PL1-CC-TI-2", Severity: report.High, Layer: "RBAC"}
	pass, fail, explicit := base, base, base
	pass.Verdict, pass.Reason = report.Pass, "no general get, list or watch on secrets in kube-system (named-secret grants are not tested)"
	explicit.Verdict, explicit.Reason = report.Pass, pass.Reason+" (explicit deny: get secrets in kube-system)"
	fail.Verdict, fail.Reason = report.Fail, "RBAC allows list secrets in kube-system"
	fail.Hint = "remove the RoleBinding or ClusterRoleBinding that lets this identity read secrets in kube-system; a tenant reads secrets only in its own namespace"
	watch := fail
	watch.Reason = "RBAC allows watch secrets in kube-system" // a watch streams every secret body (D66)
	tests := []struct {
		name    string
		answers map[string]string
		want    report.Result
	}{
		{"all denied", map[string]string{"list nodes": denied, "get secrets": denied, "list secrets": denied, "watch secrets": denied}, pass},
		{"an explicit deny is noted", map[string]string{"list nodes": denied, "get secrets": `{"allowed":false,"denied":true}`, "list secrets": denied, "watch secrets": denied}, explicit},
		{"list allowed", map[string]string{"list nodes": denied, "get secrets": denied, "list secrets": allowed, "watch secrets": denied}, fail},
		{"watch allowed", map[string]string{"list nodes": denied, "get secrets": denied, "list secrets": denied, "watch secrets": allowed}, watch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ssarServer{answers: tt.answers}
			got := at02(t.Context(), connect(t, srv), at02Spec(), ignore)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at02() = %+v,\nwant %+v", got, tt.want)
			}
			wantAsked := []cluster.Attributes{
				{Verb: "get", Resource: "secrets", Namespace: "kube-system"},
				{Verb: "list", Resource: "secrets", Namespace: "kube-system"},
				{Verb: "watch", Resource: "secrets", Namespace: "kube-system"},
			}
			if !slices.Equal(srv.asked[1:4], wantAsked) { // asked[0] is startup's question; a PASS then asks the anchor (D80)
				t.Errorf("asked %+v, want get, list and watch secrets in --other-namespace", srv.asked[1:])
			}
		})
	}
}
