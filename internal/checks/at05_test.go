package checks

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at05Spec() Spec { return Registry[4] }

func TestAT05(t *testing.T) {
	const kyverno = "admission webhook \"validate.kyverno.svc-fail\" denied the request: \n\nresource Service/tenant-a/audit-tenant-probe-48zq4 was blocked due to the following policies \n\nrestrict-nodeport:\n  validate-nodeport: 'validation error: Services of type NodePort are not allowed. rule validate-nodeport failed at path /spec/type/'\n"
	const nodeportQuota = "exceeded quota: qa-nodeports, requested: services.nodeports=1, used: services.nodeports=0, limited: services.nodeports=0"
	const rejectAll = `ValidatingAdmissionPolicy 'require-team-label' with binding 'require-team-label' denied request: every Service needs a team label`
	mayCreate := map[string]string{"list nodes": denied, "create services": allowed}
	const probedOnCreate = "tested on create only: changing an existing Service to NodePort, and type LoadBalancer (which also opens node ports), were not tested"
	noted := func(r report.Result, note string) report.Result { r.Note = note; return r }
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
			noted(result(report.Pass, "admission (webhook: validate.kyverno.svc-fail)", kyverno, ""), probedOnCreate), []string{"control-service", "AT-05"}},
		{"a quota allows no node ports", mayCreate,
			map[string]http.HandlerFunc{"AT-05": testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, `services "audit-tenant-probe-w27db" is forbidden: `+nodeportQuota)},
			noted(result(report.Pass, "admission (quota: qa-nodeports)", nodeportQuota, ""), probedOnCreate), []string{"control-service", "AT-05"}},
		{"a server error on the control", mayCreate,
			map[string]http.HandlerFunc{"control-service": testutil.Status(http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable, "etcdserver: leader changed")},
			result(report.Error, "-", "etcdserver: leader changed", "re-run; if it repeats, run with --debug and report the server message"), []string{"control-service"}},
		{"the control Service is rejected", mayCreate,
			map[string]http.HandlerFunc{"control-service": testutil.Status(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid, `services "audit-tenant-probe-q5zgg" is forbidden: `+rejectAll)},
			result(report.Skip, "-", "the compliant control Service was rejected, so the probes prove nothing: "+rejectAll, "the namespace rejects even a plain ClusterIP Service; fix that first"),
			[]string{"control-service"}},
		{"the NodePort Service is accepted", mayCreate, nil,
			result(report.Fail, "-", "a NodePort Service was accepted in a dry run", "add a policy that rejects type: NodePort, or remove `create services` from the tenant role"),
			[]string{"control-service", "AT-05"}},
		{"RBAC denies create and update but allows patch", map[string]string{"list nodes": denied, "create services": denied, "update services": denied, "patch services": allowed}, nil,
			result(report.Skip, "-", "RBAC denies create services but allows patch services; an existing Service can be changed to type NodePort, so admission must be the fence and it was not exercised",
				"run with --as <an identity that may create services>, or confirm your policy engine rejects type: NodePort on update"), nil},
		{"RBAC denies create but allows update", map[string]string{"list nodes": denied, "create services": denied, "update services": allowed}, nil,
			result(report.Skip, "-", "RBAC denies create services but allows update services; an existing Service can be changed to type NodePort, so admission must be the fence and it was not exercised",
				"run with --as <an identity that may create services>, or confirm your policy engine rejects type: NodePort on update"), nil},
		{"RBAC denies create, update and patch", map[string]string{"list nodes": denied, "create services": denied, "update services": denied, "patch services": denied, "list pods": allowed}, nil,
			noted(result(report.Pass, "RBAC", "RBAC denies create, update and patch of services; admission not exercised", ""),
				"not tested: ways to gain service creation inside the namespace (RoleBindings, service account tokens), grants limited to named Services (resourceNames), and operators that create Services for the tenant"), nil},
		{"an identity with no rights", map[string]string{"list nodes": denied, "create services": denied, "update services": denied, "patch services": denied, "list pods": denied}, nil,
			result(report.Skip, "-", "this identity may not even list pods in tenant-a, so an RBAC-only PASS would prove nothing", hintNoRights), nil},
		{"the create services question fails", map[string]string{"list nodes": denied}, nil,
			result(report.Error, "-", `could not ask "may I create services in tenant-a?": an error on the server ("no canned answer for create services") has prevented the request from succeeding (post selfsubjectaccessreviews.authorization.k8s.io)`, hintRequestFailed), nil},
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

func TestAT05AsksCreateUpdateAndPatchOnTheRBACBranch(t *testing.T) {
	// D72: dropping one verb must fail a test; a patch grant alone can turn a Service into a NodePort.
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create services": denied, "update services": denied, "patch services": denied, "list pods": allowed}}
	at05(t.Context(), connect(t, ssar), at05Spec(), ignore)
	var asked []string
	for _, a := range ssar.asked[1:] { // asked[0] is startup's question
		asked = append(asked, a.Verb+" "+a.Resource+" "+a.Namespace)
	}
	if want := []string{"create services tenant-a", "update services tenant-a", "patch services tenant-a", "list pods tenant-a"}; !slices.Equal(asked, want) {
		t.Errorf("asked %v, want %v", asked, want)
	}
}

func TestAReflectingProxyIsNeverAPass(t *testing.T) {
	// D64: a proxy that sends every SSAR request back; AT-01's control is not there to catch it.
	reflect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		testutil.JSON(http.StatusCreated, string(body))(w, r)
	})
	for _, tc := range []struct {
		run  RunFunc
		spec Spec
	}{{at02, at02Spec()}, {at03, at03Spec()}, {at04, at04Spec()}, {at05, at05Spec()}} {
		got := tc.run(t.Context(), connectWith(t, reflect, (&probeServer{}).routes()), tc.spec, ignore)
		if len(got) == 0 || got[0].Verdict != report.Error || got[0].Hint != hintNotAPIServer {
			t.Errorf("%s behind an echoing proxy = %+v, want ERROR with the not-the-API-server hint, never PASS", tc.spec.ID, got)
		}
	}
}

func TestAT05InterruptedDuringTheNodePortProbeReturnsNothing(t *testing.T) {
	// D83: the runner reports "interrupted", not a confusing "could not send the probe".
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-05": func(_ http.ResponseWriter, r *http.Request) {
		cancel() // Ctrl-C while the NodePort probe is in flight
		<-r.Context().Done()
	}}}
	c := connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create services": allowed}}, ps.routes())
	if got := at05(ctx, c, at05Spec(), ignore); len(got) != 0 {
		t.Errorf("at05() = %+v; want no result, so the runner says interrupted", got)
	}
}

func TestTheNoRightsAnchorIsAskedOncePerRun(t *testing.T) {
	// D80: AT-01 to AT-05 share one "list pods" answer.
	answers := controllerAnswers()
	for _, q := range []string{"create namespaces", "create clusterrolebindings", "create persistentvolumes", "get secrets", "list secrets", "watch secrets",
		"escalate clusterroles", "bind clusterroles", "impersonate users", "impersonate groups", "impersonate serviceaccounts", "* *",
		"create services", "update services", "patch services"} {
		answers[q] = denied
	}
	ssar := &ssarServer{answers: answers}
	Run(t.Context(), connect(t, ssar), Registry[:5], time.Minute)
	n := 0
	for _, a := range ssar.asked {
		if a.Verb == "list" && a.Resource == "pods" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("list pods asked %d times, want once per run", n)
	}
}

func TestEveryRBACOnlyPassNeedsTheAnchor(t *testing.T) {
	// D80: a misspelled --as user is denied everything; AT-01 to AT-03 must not PASS either.
	answers := controllerAnswers()
	answers["list pods"] = denied
	for _, q := range []string{"create namespaces", "create clusterrolebindings", "create persistentvolumes", "get secrets", "list secrets", "watch secrets",
		"escalate clusterroles", "bind clusterroles", "impersonate users", "impersonate groups", "impersonate serviceaccounts", "* *",
		"create services", "update services", "patch services"} {
		answers[q] = denied
	}
	for _, r := range Run(t.Context(), connect(t, &ssarServer{answers: answers}), Registry[:5], time.Minute) {
		if r.Verdict != report.Skip || r.Hint != hintNoRights {
			t.Errorf("%s = %+v, want SKIP with the no-rights hint", r.ID, r)
		}
	}
}

func TestAT05AStoredControlOrProbeStopsTheCheck(t *testing.T) {
	// D79, D84(3): a stored or nameless answer means dryRun=All did not reach the API server.
	for name, tt := range map[string]struct {
		answers map[string]http.HandlerFunc
		sent    []string
	}{
		"stored control":   {map[string]http.HandlerFunc{"control-service": testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p","resourceVersion":"4711"}}`)}, []string{"control-service"}},
		"nameless control": {map[string]http.HandlerFunc{"control-service": testutil.JSON(http.StatusCreated, `{}`)}, []string{"control-service"}},
		"stored probe":     {map[string]http.HandlerFunc{"AT-05": testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-np001","resourceVersion":"4713"}}`)}, []string{"control-service", "AT-05"}},
	} {
		ps := &probeServer{answers: tt.answers}
		got := at05(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create services": allowed}}, ps.routes()), at05Spec(), ignore)
		if len(got) != 1 || got[0].Verdict != report.Error || !slices.Equal(ps.sent, tt.sent) {
			t.Errorf("%s: %+v after sending %v; want ERROR after %v", name, got, ps.sent, tt.sent)
		}
	}
}
