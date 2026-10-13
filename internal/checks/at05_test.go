package checks

import (
	"net/http"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at05Spec() Spec { return Registry[4] }

func TestAT05(t *testing.T) {
	const kyverno = "admission webhook \"validate.kyverno.svc-fail\" denied the request: \n\nresource Service/tenant-a/audit-tenant-probe-48zq4 was blocked due to the following policies \n\nrestrict-nodeport:\n  validate-nodeport: 'validation error: Services of type NodePort are not allowed. rule validate-nodeport failed at path /spec/type/'\n"
	const rejectAll = `ValidatingAdmissionPolicy 'require-team-label' with binding 'require-team-label' denied request: every Service needs a team label`
	mayCreate := map[string]string{"list nodes": denied, "create services": allowed}
	base := report.Result{ID: "AT-05", Name: "NodePort services blocked", Kind: report.Behavioral, MTB: "MTB-PL1-BC-HI-1", Severity: report.Medium}
	result := func(v report.Verdict, layer, reason, hint string) report.Result {
		r := base
		r.Verdict, r.Layer, r.Reason, r.Hint = v, layer, reason, hint
		return r
	}
	tests := []struct {
		name    string
		ssar    map[string]string
		answers map[string]http.HandlerFunc
		want    report.Result
		sent    []string
	}{
		{"a webhook rejects the NodePort Service", mayCreate,
			map[string]http.HandlerFunc{"AT-05": testutil.Status(http.StatusBadRequest, "", kyverno)},
			result(report.Pass, "admission (webhook: validate.kyverno.svc-fail)", kyverno, ""), []string{"control-service", "AT-05"}},
		{"the control Service is rejected", mayCreate,
			map[string]http.HandlerFunc{"control-service": testutil.Status(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid, `services "audit-tenant-probe-q5zgg" is forbidden: `+rejectAll)},
			result(report.Skip, "-", "the compliant control Service was rejected, so the probes prove nothing: "+rejectAll, "the namespace rejects even a plain ClusterIP Service; fix that first"),
			[]string{"control-service"}},
		{"the NodePort Service is accepted", mayCreate, nil,
			result(report.Fail, "-", "a NodePort Service was accepted in a dry run", "add a policy that rejects type: NodePort, or remove `create services` from the tenant role"),
			[]string{"control-service", "AT-05"}},
		{"RBAC denies create but allows update", map[string]string{"list nodes": denied, "create services": denied, "update services": allowed}, nil,
			result(report.Skip, "-", "RBAC denies create services but allows update services; an existing Service can be changed to type NodePort, so admission must be the fence and it was not exercised",
				"run with --as <an identity that may create services>, or confirm your policy engine rejects type: NodePort on update"), nil},
		{"RBAC denies create, update and patch", map[string]string{"list nodes": denied, "create services": denied, "update services": denied, "patch services": denied}, nil,
			result(report.Pass, "RBAC", "RBAC denies create, update and patch of services; admission not exercised", ""), nil},
		{"the create services question fails", map[string]string{"list nodes": denied}, nil,
			result(report.Error, "-", `could not ask "may I create services?": an error on the server ("no canned answer for create services") has prevented the request from succeeding (post selfsubjectaccessreviews.authorization.k8s.io)`, hintRequestFailed), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := &probeServer{answers: tt.answers}
			var proven []report.Result
			got := at05(t.Context(), connectWith(t, &ssarServer{answers: tt.ssar}, ps.routes()), at05Spec(), func(r report.Result) { proven = append(proven, r) })
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at05() = %+v,\nwant %+v", got, tt.want)
			}
			if !slices.Equal(ps.sent, tt.sent) {
				t.Errorf("sent %v, want %v", ps.sent, tt.sent)
			}
			if (tt.want.Verdict == report.Fail) != (len(proven) == 1) {
				t.Errorf("proved %v; a FAIL, and only a FAIL, is handed to the runner at once (D61)", proven)
			}
		})
	}
}
