package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at04Spec() Spec { return Registry[3] }

var at04IDs = []string{"AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5"}

const psaDenial = `pods "audit-tenant-probe-g54wp" is forbidden: violates PodSecurity "restricted:latest": privileged (container "probe" must not set securityContext.privileged=true)`

// controllerAnswers denies the 31 questions AT-04 asks when RBAC denies create pods, and allows the
// "list pods" anchor of an RBAC-only PASS (D69).
func controllerAnswers() map[string]string {
	m := map[string]string{"list nodes": denied, "create pods": denied, "list pods": allowed,
		"update pods": denied, "patch pods": denied} // pods/ephemeralcontainers; ssarServer keys by verb and resource, so /scale shares the kind's answer
	for _, kind := range []string{"deployments", "replicasets", "statefulsets", "daemonsets", "jobs", "cronjobs", "replicationcontrollers"} {
		for _, verb := range []string{"create", "update", "patch"} {
			m[verb+" "+kind] = denied
		}
	}
	return m
}

func TestAT04PassWhenAdmissionRejectsEveryProbe(t *testing.T) {
	ps := &probeServer{answers: map[string]http.HandlerFunc{}}
	for _, id := range at04IDs {
		ps.answers[id] = testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, psaDenial)
	}
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)

	if !slices.Equal(ids(got), at04IDs) {
		t.Fatalf("result IDs = %v, want %v", ids(got), at04IDs)
	}
	for _, r := range got {
		if r.Verdict != report.Pass || r.Layer != "admission (PSA)" || !strings.HasPrefix(r.Reason, "violates PodSecurity") || r.Hint != "" {
			t.Errorf("%s = %+v, want PASS by PSA with the violated controls in the reason", r.ID, r)
		}
	}
	if want := []string{"control", "AT-04.1", "AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5"}; !slices.Equal(ps.sent, want) {
		t.Errorf("sent %v, want %v: the control first, then one probe per result, AT-04.1 in both forms", ps.sent, want)
	}
	// Added by the /ship test coverage audit, pass 1 (Phase 2).
	// Value: protects=each admission PASS names what its probe did not test (D86); fails_when=at04 drops a PASS note or gives a result another result's note;
	// why_new=no unit test or e2e line reads AT-04 notes; seam=none; no_claim=the full note wording, or AT-04.3 (it has no note)
	for id, untested := range map[string]string{"AT-04.1": "initContainers", "AT-04.2": "allowed path prefixes", "AT-04.4": "hostPort", "AT-04.5": "initContainers"} {
		if i := slices.Index(at04IDs, id); !strings.Contains(got[i].Note, untested) {
			t.Errorf("%s note = %q, want it to name what was not tested (%s)", id, got[i].Note, untested)
		}
	}
}

func TestAT04AnAcceptedProbeIsAFailProvedAtOnce(t *testing.T) {
	ps := &probeServer{} // a namespace with no PSA label and no policy: every probe is accepted
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
	var proven []string
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), func(r report.Result) { proven = append(proven, r.ID) })

	want := map[string]string{
		"AT-04.1": "a privileged container was accepted in a dry run",
		"AT-04.2": "a pod with a hostPath volume was accepted in a dry run",
		"AT-04.3": "a pod with hostPID was accepted in a dry run",
		"AT-04.4": "a pod with hostNetwork was accepted in a dry run",
		"AT-04.5": "a pod with allowPrivilegeEscalation: true was accepted in a dry run",
	}
	for _, r := range got {
		if r.Verdict != report.Fail || r.Layer != "-" || r.Reason != want[r.ID] || !strings.Contains(r.Hint, "pod-security.kubernetes.io/enforce=") {
			t.Errorf("%s = %+v, want FAIL %q with the PSA hint", r.ID, r, want[r.ID])
		}
	}
	if h := got[4].Hint; !strings.Contains(h, "restricted") || !strings.Contains(h, "baseline does not block") || !strings.Contains(h, "allowPrivilegeEscalation") {
		t.Errorf("AT-04.5 hint = %q, want both fixes: restricted, or a policy against escalation", h)
	}
	if !slices.Equal(proven, at04IDs) { // each result proves once: AT-04.1 stops at its first accepted form (D84)
		t.Errorf("proved %v, want every FAIL handed to the runner at once (D61)", proven)
	}
}

func TestAT04RBACDeniesPodsAndEveryController(t *testing.T) {
	ps := &probeServer{}
	ssar := &ssarServer{answers: controllerAnswers()}
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)

	for _, r := range got {
		if r.Verdict != report.Pass || r.Layer != "RBAC" || r.Reason != at04RBACReason {
			t.Errorf("%s = %+v, want PASS by RBAC", r.ID, r)
		}
	}
	if len(got) != 5 || len(ps.sent) != 0 || len(ssar.asked) != 1+1+31+1 {
		t.Errorf("%d results, %d probes, %d questions; want 5 results, no probe, startup + create pods + 31 + list pods", len(got), len(ps.sent), len(ssar.asked))
	}
	// Added by the /ship test coverage audit, pass 1 (Phase 2).
	// Value: protects=every RBAC-only AT-04 PASS carries the note naming the pod paths it did not test (D86); fails_when=the note loop is dropped or skips a result;
	// why_new=this test checked the reason only; the e2e has no RBAC-only AT-04 run; seam=none; no_claim=which pod paths the note lists
	for _, r := range got {
		if r.Note != at04RBACNote {
			t.Errorf("%s note = %q, want the not-tested note of an RBAC-only PASS", r.ID, r.Note)
		}
	}
}

func TestAT04AControllerTheTenantMayUseIsASkip(t *testing.T) {
	answers := controllerAnswers()
	answers["update deployments"] = allowed
	ps := &probeServer{}
	ssar := &ssarServer{answers: answers}
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)

	for _, r := range got {
		if r.Verdict != report.Skip || !strings.Contains(r.Reason, "RBAC denies create pods but allows update deployments") || !strings.Contains(r.Hint, "--as") {
			t.Errorf("%s = %+v, want SKIP naming update deployments, with a hint", r.ID, r)
		}
	}
	if len(got) != 5 || len(ps.sent) != 0 || len(ssar.asked) != 4 {
		t.Errorf("%d results, %d probes, %d questions; want 5, none, and no question after the grant (startup, create pods, create and update deployments)", len(got), len(ps.sent), len(ssar.asked))
	}
}

func TestAT04ARejectedControlPodIsASkipWithItsText(t *testing.T) {
	quota := `pods "audit-tenant-probe-nk6zp" is forbidden: exceeded quota: q, requested: limits.cpu=5, used: limits.cpu=0, limited: limits.cpu=1`
	ps := &probeServer{answers: map[string]http.HandlerFunc{"control": testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, quota)}}
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)

	for _, r := range got {
		if r.Verdict != report.Skip || !strings.Contains(r.Reason, "exceeded quota") || !strings.Contains(r.Hint, "ResourceQuota") {
			t.Errorf("%s = %+v, want SKIP with the control's rejection text and the quota hint", r.ID, r)
		}
	}
	if len(got) != 5 || !slices.Equal(ps.sent, []string{"control"}) {
		t.Errorf("%d results after sending %v; want 5 SKIPs after the control alone", len(got), ps.sent)
	}
}

func TestAT04OneSubCheckSendsOnlyItsProbe(t *testing.T) {
	specs, err := Select([]string{"AT-04.2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ps := &probeServer{}
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), specs[0], ignore)
	if !slices.Equal(ids(got), []string{"AT-04.2"}) || !slices.Equal(ps.sent, []string{"control", "AT-04.2"}) {
		t.Errorf("results %v after sending %v; want AT-04.2 alone after the control", ids(got), ps.sent)
	}
}

func TestAT04ErrorsAndInterruption(t *testing.T) {
	t.Run("the create pods question fails", func(t *testing.T) {
		got := at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied}}, (&probeServer{}).routes()), at04Spec(), ignore)
		for _, r := range got {
			if r.Verdict != report.Error || r.Hint != hintRequestFailed || !strings.Contains(r.Reason, "create pods") {
				t.Errorf("%s = %+v, want ERROR naming the question, with the re-run hint", r.ID, r)
			}
		}
		if len(got) != 5 {
			t.Errorf("%d results, want 5", len(got))
		}
	})
	t.Run("a controller question fails", func(t *testing.T) {
		answers := controllerAnswers()
		delete(answers, "patch statefulsets") // HTTP 500
		got := at04(t.Context(), connectWith(t, &ssarServer{answers: answers}, (&probeServer{}).routes()), at04Spec(), ignore)
		if len(got) != 5 || got[0].Verdict != report.Error || !strings.Contains(got[0].Reason, "patch statefulsets") {
			t.Errorf("results = %+v, want 5 ERRORs naming patch statefulsets", got)
		}
	})
	t.Run("a probe that cannot be read is an error, not a guess", func(t *testing.T) {
		ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.3": testutil.Status(http.StatusConflict, metav1.StatusReasonConflict, "")}}
		ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
		got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)
		if got[2].Verdict != report.Error || got[2].Reason != "HTTP 409 Conflict" || got[2].Hint == "" || got[3].Verdict != report.Fail {
			t.Errorf("results = %+v, want AT-04.3 ERROR \"HTTP 409 Conflict\" and the run to go on", got)
		}
	})
	t.Run("interrupted midway", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.3": func(_ http.ResponseWriter, r *http.Request) {
			cancel() // Ctrl-C while the third probe is in flight
			<-r.Context().Done()
		}}}
		ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
		c := connectWith(t, ssar, ps.routes())
		if got := at04(ctx, c, at04Spec(), ignore); !slices.Equal(ids(got), []string{"AT-04.1", "AT-04.2"}) {
			t.Errorf("results %v, want the two finished probes; the runner marks the rest interrupted", ids(got))
		}
	})
}

func TestAT04EveryPodControllerPathIsAsked(t *testing.T) {
	// D66: a ReplicationController and an ephemeral container also put a pod spec past create-pods RBAC.
	for _, grant := range []struct{ key, action string }{
		{"create replicationcontrollers", "create replicationcontrollers in tenant-a"},
		{"patch pods", "patch pods/ephemeralcontainers in tenant-a"},
	} {
		answers := controllerAnswers()
		answers[grant.key] = allowed
		got := at04(t.Context(), connect(t, &ssarServer{answers: answers}), at04Spec(), ignore)
		if len(got) != 5 || got[0].Verdict != report.Skip || !strings.Contains(got[0].Reason, "allows "+grant.action) {
			t.Errorf("%s allowed: %+v, want 5 SKIPs naming %q", grant.key, got, grant.action)
		}
	}
}

func TestAT04ControlErrorsAreErrorsNotSkips(t *testing.T) {
	// D65: an API failure on the control says nothing about the namespace (DESIGN.md section 6).
	for name, h := range map[string]http.HandlerFunc{
		"500":               testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out"),
		"401":               testutil.Status(http.StatusUnauthorized, metav1.StatusReasonUnauthorized, "Unauthorized"),
		"malformed control": testutil.Status(http.StatusUnprocessableEntity, metav1.StatusReasonInvalid, `Pod "x" is invalid: spec.containers[0].image: Required value`),
	} {
		ps := &probeServer{answers: map[string]http.HandlerFunc{"control": h}}
		ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}
		got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)
		if len(got) != 5 {
			t.Fatalf("%s: %d results, want 5", name, len(got))
		}
		for _, r := range got {
			if r.Verdict != report.Error || r.Hint == "" || strings.Contains(r.Hint, "fix that first") {
				t.Errorf("%s: %s = %+v, want ERROR with the classifier's hint", name, r.ID, r)
			}
		}
	}
	// A control rejected by admission is a real rejection: SKIP with the fix-that-first hint.
	psa := testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, psaDenial)
	ps := &probeServer{answers: map[string]http.HandlerFunc{"control": psa}}
	got := at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}, ps.routes()), at04Spec(), ignore)
	if got[0].Verdict != report.Skip || got[0].Hint != "the namespace rejects even a compliant pod; fix that first" {
		t.Errorf("admission-rejected control = %+v, want SKIP with the fix-that-first hint", got[0])
	}
}

func TestAT04AnIdentityWithNoRightsIsNotAnRBACPass(t *testing.T) {
	// D69: a misspelled --as user is denied everything; an RBAC-only PASS would prove nothing.
	answers := controllerAnswers()
	answers["list pods"] = denied
	got := at04(t.Context(), connect(t, &ssarServer{answers: answers}), at04Spec(), ignore)
	for _, r := range got {
		if r.Verdict != report.Skip || r.Reason != "this identity may not even list pods in tenant-a, so an RBAC-only PASS would prove nothing" || !strings.Contains(r.Hint, "--as") {
			t.Errorf("%s = %+v, want SKIP naming the missing rights, with a hint naming -n and --as", r.ID, r)
		}
	}
	if len(got) != 5 {
		t.Errorf("%d results, want 5", len(got))
	}
}

func TestEveryAT04ResultHasAProbe(t *testing.T) {
	// D72: a new or renamed AT-04 result without a probe would crash at run time.
	spec := at04Spec()
	for _, r := range spec.Results {
		if p, ok := at04Probes[r.ID]; !ok || len(p.forms) == 0 || slices.ContainsFunc(p.forms, func(f func(*corev1.Pod)) bool { return f == nil }) || p.accepted == "" || p.hint == "" {
			t.Errorf("%s has no complete entry in at04Probes", r.ID)
		}
	}
	if len(at04Probes) != len(spec.Results) {
		t.Errorf("at04Probes has %d entries, the Registry %d AT-04 results", len(at04Probes), len(spec.Results))
	}
}

func TestAT04ALimitRangeListErrorOtherThan403IsAnError(t *testing.T) {
	// Only a 403 falls back to 1m/1Mi sizes (DESIGN.md section 5); any other API error is ERROR (section 6).
	ps := &probeServer{}
	routes := ps.routes()
	routes["GET /api/v1/namespaces/tenant-a/limitranges"] = testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out")
	got := at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}, routes), at04Spec(), ignore)
	if len(got) != 5 || got[0].Verdict != report.Error || !strings.Contains(got[0].Reason, "limitranges") || len(ps.sent) != 0 {
		t.Errorf("results = %+v after %v; want 5 ERRORs naming the LimitRange list and no probe sent", got, ps.sent)
	}
	routes["GET /api/v1/namespaces/tenant-a/limitranges"] = testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, `limitranges is forbidden: User "team-a-dev" cannot list resource "limitranges"`)
	if got := at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}, routes), at04Spec(), ignore); got[0].Verdict != report.Fail {
		t.Errorf("403 on the LimitRange list: %+v, want the probes sent with 1m/1Mi sizes (FAIL: every probe accepted)", got[0])
	}
}

func TestAT04AsksEachControllerByItsAPIGroupAndSubresource(t *testing.T) {
	// D83: ssarServer keys answers by verb and resource only, so pin the questions themselves.
	ssar := &ssarServer{answers: controllerAnswers()}
	at04(t.Context(), connect(t, ssar), at04Spec(), ignore)
	var want []cluster.Attributes
	for _, k := range []struct{ group, resource string }{
		{"apps", "deployments"}, {"apps", "replicasets"}, {"apps", "statefulsets"}, {"apps", "daemonsets"},
		{"batch", "jobs"}, {"batch", "cronjobs"}, {"", "replicationcontrollers"},
	} {
		for _, verb := range []string{"create", "update", "patch"} {
			want = append(want, cluster.Attributes{Verb: verb, Group: k.group, Resource: k.resource, Namespace: "tenant-a"})
		}
	}
	for _, verb := range []string{"update", "patch"} {
		want = append(want, cluster.Attributes{Verb: verb, Resource: "pods", Subresource: "ephemeralcontainers", Namespace: "tenant-a"})
	}
	for _, k := range []struct{ group, resource string }{{"apps", "deployments"}, {"apps", "replicasets"}, {"apps", "statefulsets"}, {"", "replicationcontrollers"}} {
		for _, verb := range []string{"update", "patch"} { // D77: scaling a template up creates its pods
			want = append(want, cluster.Attributes{Verb: verb, Group: k.group, Resource: k.resource, Subresource: "scale", Namespace: "tenant-a"})
		}
	}
	if got := ssar.asked[2 : len(ssar.asked)-1]; !slices.Equal(got, want) { // startup, create pods ... list pods
		t.Errorf("asked %+v,\nwant %+v", got, want)
	}
}

func TestAT04ScalingAControllerIsAPodPath(t *testing.T) {
	// D77: update deployments/scale makes the controller create pods from its template.
	answers := controllerAnswers()
	scale := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in authzv1.SelfSubjectAccessReview
		_ = json.NewDecoder(r.Body).Decode(&in)
		a := *in.Spec.ResourceAttributes
		body := denied
		if a.Subresource == "scale" && a.Resource == "deployments" && a.Verb == "update" {
			body = allowed
		} else if b, ok := answers[a.Verb+" "+a.Resource]; ok && a.Subresource == "" || ok && a.Subresource == "ephemeralcontainers" {
			body = b
		}
		spec, _ := json.Marshal(in.Spec)
		testutil.JSON(http.StatusCreated, `{"spec":`+string(spec)+`,"status":`+body+`}`)(w, r)
	})
	got := at04(t.Context(), connect(t, scale), at04Spec(), ignore)
	if len(got) != 5 || got[0].Verdict != report.Skip || !strings.Contains(got[0].Reason, "allows update deployments/scale in tenant-a") {
		t.Errorf("results = %+v, want 5 SKIPs naming update deployments/scale", got)
	}
}

func TestAT04PrivilegedIsBlockedOnlyWhenBothFormsAre(t *testing.T) {
	// D78: a policy that only demands the escalation field rejects one form and accepts the other.
	var n int
	var mu sync.Mutex
	psa := testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, psaDenial)
	ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.1": func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			psa(w, r) // the escalation-unset form is rejected ...
			return
		}
		testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"}}`)(w, r) // ... the escalation-allowed form is not
	}}}
	got := at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}, ps.routes()), at04Spec(), ignore)
	if got[0].ID != "AT-04.1" || got[0].Verdict != report.Fail {
		t.Errorf("AT-04.1 = %+v, want FAIL: one privileged form was accepted", got[0])
	}
}

func TestAT04AStoredControlStopsTheCheck(t *testing.T) {
	// D79: an answer with a resourceVersion means the server did not honor dryRun=All.
	stored := testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p","resourceVersion":"4711"}}`)
	ps := &probeServer{answers: map[string]http.HandlerFunc{"control": stored}}
	got := at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}, ps.routes()), at04Spec(), ignore)
	if len(got) != 5 || got[0].Verdict != report.Error || !strings.Contains(got[0].Reason, "audit-tenant-probe-x7k2p") || !slices.Equal(ps.sent, []string{"control"}) {
		t.Errorf("results = %+v after %v; want 5 ERRORs naming the stored pod, and no dangerous probe sent", got, ps.sent)
	}
	nameless := testutil.JSON(http.StatusCreated, `{}`) // not the API server: no generated name
	ps = &probeServer{answers: map[string]http.HandlerFunc{"control": nameless}}
	got = at04(t.Context(), connectWith(t, &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed}}, ps.routes()), at04Spec(), ignore)
	if got[0].Verdict != report.Error || got[0].Hint != hintNotAPIServer {
		t.Errorf("a nameless answer = %+v, want ERROR with the not-the-API-server hint", got[0])
	}
}

func TestAnRBACOnlyPassWhoseAnchorQuestionFailsIsAnError(t *testing.T) {
	// D83: a failed "list pods" question is ERROR, never the no-rights SKIP.
	answers := controllerAnswers()
	answers["list pods"] = `{"allowed":false,"evaluationError":"authorizer webhook unavailable"}`
	got := at04(t.Context(), connect(t, &ssarServer{answers: answers}), at04Spec(), ignore)
	if len(got) != 5 || got[0].Verdict != report.Error || got[0].Hint != hintEvaluation || !strings.Contains(got[0].Reason, "list pods") {
		t.Errorf("results = %+v, want 5 ERRORs naming list pods", got)
	}
}

// nth answers the i-th request with hs[i]; the last handler repeats.
func nth(hs ...http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := hs[min(n, len(hs)-1)]
		n++
		mu.Unlock()
		h(w, r)
	}
}

var (
	acceptProbe = testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"}}`)
	rejectProbe = testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, psaDenial)
	mayPods     = map[string]string{"list nodes": denied, "create pods": allowed}
)

func TestAT04InterruptAfterTheFirstPrivilegedFormKeepsTheFail(t *testing.T) {
	// D84(1), D61, D63: the first form was accepted; Ctrl-C arrives while the second is in flight.
	// The FAIL is shown again only if AT-04.1 keeps sending its second form; with D84(2) it stops,
	// so hold the AT-04.2 probe instead.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hold := func(_ http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }
	ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.1": acceptProbe, "AT-04.2": hold}}
	got := Run(ctx, connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), []Spec{at04Spec()}, time.Minute)
	if got[0].ID != "AT-04.1" || got[0].Verdict != report.Fail || got[1].Reason != "interrupted" {
		t.Errorf("results = %+v, want AT-04.1 FAIL kept and AT-04.2 interrupted", got[:2])
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	hold = func(_ http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }
	ps = &probeServer{answers: map[string]http.HandlerFunc{"AT-04.1": nth(rejectProbe, hold)}}
	if got := Run(ctx, connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), []Spec{at04Spec()}, time.Minute); got[0].Reason != "interrupted" {
		t.Errorf("interrupted during the second form after a rejection: %+v, want interrupted, never a PASS", got[0])
	}
}

// Added by the /ship pre-landing review, pass 3 (Phase 2).
// Value: protects=a control send cut by a signal returns nothing, so the runner reports "interrupted" (or the timeout) with its hint (D65, DESIGN.md
// failure rules); fails_when=controlFailed loses its ctx guard and reports "could not send the probe"; why_new=no test held a control; seam=none;
// no_claim=the exit code (ERROR either way)
func TestAnInterruptedControlLeavesTheResultsToTheRunner(t *testing.T) {
	for _, tt := range []struct {
		control string
		spec    Spec
	}{{"control", at04Spec()}, {"control-service", at05Spec()}} {
		ctx, cancel := context.WithCancel(t.Context())
		hold := func(_ http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }
		ps := &probeServer{answers: map[string]http.HandlerFunc{tt.control: hold}}
		ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed, "create services": allowed}}
		got := Run(ctx, connectWith(t, ssar, ps.routes()), []Spec{tt.spec}, time.Minute)
		cancel()
		if len(got) == 0 || slices.ContainsFunc(got, func(r report.Result) bool { return r.Reason != "interrupted" }) {
			t.Errorf("%s held: %+v, want every result interrupted, as the runner reports it", tt.control, got)
		}
	}
}

// Added by the /ship pre-landing review, final pass (Phase 2).
// Value: protects=once AT-04 finds a stored object (dryRun=All dropped), AT-05 sends no create at all (D101); fails_when=the latch is dropped and AT-05
// sends its control Service through the same connection; why_new=each check stopped only itself; seam=none; no_claim=which proxies drop dryRun
func TestAStoredObjectStopsEveryLaterCreate(t *testing.T) {
	stored := testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-h0st1","resourceVersion":"4712"}}`)
	ps := &probeServer{answers: map[string]http.HandlerFunc{"control": stored}}
	ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed, "create services": allowed}}
	got := Run(t.Context(), connectWith(t, ssar, ps.routes()), []Spec{at04Spec(), at05Spec()}, time.Minute)
	at05 := got[len(got)-1]
	if at05.ID != "AT-05" || at05.Verdict != report.Error || !strings.Contains(at05.Reason, "no further create") || at05.Hint == "" {
		t.Errorf("AT-05 = %+v, want ERROR: no create after a stored object", at05)
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if !slices.Equal(ps.sent, []string{"control"}) {
		t.Errorf("sent %v, want only the control pod: AT-05 must not send its control Service", ps.sent)
	}
}

// Added for the PR #3 must-fix items (D103).
// Value: protects=after any control answer that is not a verified dry run, AT-05 sends no create, and client-go resends none; fails_when=the latch
// fires only on a resourceVersion (the red team's nameless answer), or Retry-After reaches client-go; why_new=the test above covers a resourceVersion
// only; seam=none; no_claim=AT-04's own reason text (the classifier's or notDryRun's)
func TestAnUnverifiedCreateStopsEveryLaterCreate(t *testing.T) {
	retryAfter := func(code int32) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "0")
			testutil.Status(code, metav1.StatusReasonServiceUnavailable, "")(w, r)
		}
	}
	for name, control := range map[string]http.HandlerFunc{
		"no generated name":           testutil.JSON(http.StatusCreated, `{"metadata":{"name":"tenant-proxy-object"}}`), // the red team's answer
		"an empty 2xx body":           testutil.JSON(http.StatusCreated, ``),
		"a 2xx body that is not JSON": testutil.JSON(http.StatusOK, `<html>proxy</html>`),
		"a failed send":               func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) },
		"a 503 with Retry-After":      retryAfter(http.StatusServiceUnavailable),
		"a 429 with Retry-After":      retryAfter(http.StatusTooManyRequests),
	} {
		t.Run(name, func(t *testing.T) {
			ps := &probeServer{answers: map[string]http.HandlerFunc{"control": control}}
			ssar := &ssarServer{answers: map[string]string{"list nodes": denied, "create pods": allowed, "create services": allowed}}
			got := Run(t.Context(), connectWith(t, ssar, ps.routes()), []Spec{at04Spec(), at05Spec()}, time.Minute)
			at05 := got[len(got)-1]
			if at05.ID != "AT-05" || at05.Verdict != report.Error || !strings.Contains(at05.Reason, "no further create") || at05.Hint == "" {
				t.Errorf("AT-05 = %+v, want ERROR: no create after an unverified answer", at05)
			}
			ps.mu.Lock()
			defer ps.mu.Unlock()
			if !slices.Equal(ps.sent, []string{"control"}) {
				t.Errorf("sent %v, want the control pod once: no retry, and no create from AT-05", ps.sent)
			}
		})
	}
}

func TestAT04StopsAResultAtItsFirstFail(t *testing.T) {
	// D84(2): once a form is accepted, the FAIL is proven; the other form would be one more dangerous request.
	ps := &probeServer{}
	at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), at04Spec(), ignore)
	if n := len(slices.DeleteFunc(slices.Clone(ps.sent), func(s string) bool { return s != "AT-04.1" })); n != 1 {
		t.Errorf("AT-04.1 sent %d times after its first form was accepted, want 1", n)
	}
}

func TestAT04OneResultTakesTheWorstOfItsForms(t *testing.T) {
	// D78, D84: the verdict is the worst of the two privileged forms, whatever their order.
	unread := testutil.Status(http.StatusConflict, metav1.StatusReasonConflict, "")
	noDryRun := testutil.Status(http.StatusBadRequest, metav1.StatusReasonBadRequest, `admission webhook "privileged-guard.example.com" does not support dry run`) // a webhook that matches privileged pods only
	specs, err := Select([]string{"AT-04.1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name          string
		first, second http.HandlerFunc
		want          report.Verdict
	}{
		{"accepted first", acceptProbe, rejectProbe, report.Fail},
		{"accepted second", rejectProbe, acceptProbe, report.Fail},
		{"unreadable, then rejected", unread, rejectProbe, report.Error},
		{"rejected, then unreadable", rejectProbe, unread, report.Error},
		{"both rejected", rejectProbe, rejectProbe, report.Pass},
		// Added by the /ship test coverage audit, pass 1 (Phase 2).
		// Value: protects=AT-04.1 keeps the worse form: a SKIP beats a PASS, a proven FAIL beats an ERROR (D78, D84); fails_when=the worse map drops or reorders Skip or Fail;
		// why_new=the rows above rank only FAIL and ERROR against PASS; seam=none; no_claim=the reason text of the kept form
		{"rejected, then a webhook that cannot dry-run", rejectProbe, noDryRun, report.Skip},
		{"unreadable, then accepted", unread, acceptProbe, report.Fail},
		// Added by the /ship pre-landing review (Phase 2): the two orders the rows above miss, a FAIL over a SKIP and an ERROR over a SKIP.
		{"a webhook that cannot dry-run, then accepted", noDryRun, acceptProbe, report.Fail},
		{"a webhook that cannot dry-run, then unreadable", noDryRun, unread, report.Error},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.1": nth(tt.first, tt.second)}}
			got := at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), specs[0], ignore)
			if len(got) != 1 || got[0].Verdict != tt.want {
				t.Errorf("AT-04.1 = %+v, want %s", got, tt.want)
			}
		})
	}
}

// Added by the /ship pre-landing review (Phase 2).
// Value: protects=a 429 or 5xx probe answer ends AT-04: no later probe, every unfinished result ERROR (D91); fails_when=the loop goes on probing, so client-go's
// retries repeat per probe; why_new=the 409 test above expects the run to go on; seam=none; no_claim=client-go's own retry count (no Retry-After here)
func TestAT04StopsAtAnAnswerThatWouldRepeat(t *testing.T) {
	for _, code := range []int32{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.1": testutil.Status(code, metav1.StatusReasonServiceUnavailable, "")}}
		got := at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), at04Spec(), ignore)
		if len(got) != 5 || slices.ContainsFunc(got, func(r report.Result) bool { return r.Verdict != report.Error || r.Hint == "" }) {
			t.Errorf("HTTP %d: results = %+v, want five ERRORs with a hint", code, got)
		}
		if want := []string{"control", "AT-04.1"}; !slices.Equal(ps.sent, want) {
			t.Errorf("HTTP %d: sent %v, want %v: no probe after an answer that would repeat", code, ps.sent, want)
		}
	}
}

// Added by the /ship test coverage audit, pass 2 (Phase 2).
// Value: protects=a stop on a failed send, a 500 or a second privileged form keeps the verdicts already decided and makes the rest ERROR (D78, D91);
// fails_when=the stop drops earlier results, does not slice from the current one, misses non-StatusErrors or exactly 500; why_new=the test above stops at the
// first probe with 429 and 503 only; seam=none; no_claim=the reason text of the ERRORs (the classifier's)
func TestAT04AnUnavailableAnswerKeepsWhatWasDecided(t *testing.T) {
	failedSend := func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) } // the connection drops: not a StatusError
	for _, tt := range []struct {
		name    string
		answers map[string]http.HandlerFunc
		want    []report.Verdict
		sent    []string
	}{
		{"a failed send at AT-04.3", map[string]http.HandlerFunc{"AT-04.1": rejectProbe, "AT-04.2": rejectProbe, "AT-04.3": failedSend},
			[]report.Verdict{report.Pass, report.Pass, report.Error, report.Error, report.Error}, []string{"control", "AT-04.1", "AT-04.1", "AT-04.2", "AT-04.3"}},
		{"a 500 at AT-04.2", map[string]http.HandlerFunc{"AT-04.1": rejectProbe, "AT-04.2": testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "")},
			[]report.Verdict{report.Pass, report.Error, report.Error, report.Error, report.Error}, []string{"control", "AT-04.1", "AT-04.1", "AT-04.2"}},
		{"the second privileged form after a rejection", map[string]http.HandlerFunc{"AT-04.1": nth(rejectProbe, testutil.Status(http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable, ""))},
			[]report.Verdict{report.Error, report.Error, report.Error, report.Error, report.Error}, []string{"control", "AT-04.1", "AT-04.1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ps := &probeServer{answers: tt.answers}
			got := at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), at04Spec(), ignore)
			var verdicts []report.Verdict
			for _, r := range got {
				verdicts = append(verdicts, r.Verdict)
				if r.Verdict == report.Error && r.Hint == "" {
					t.Errorf("%s: ERROR without a hint", r.ID)
				}
			}
			ps.mu.Lock() // an aborted connection writes no answer, so only the lock orders the handler's append before this read
			defer ps.mu.Unlock()
			if !slices.Equal(ids(got), at04IDs) || !slices.Equal(verdicts, tt.want) || !slices.Equal(ps.sent, tt.sent) {
				t.Errorf("results %v %v, sent %v; want %v %v, sent %v", ids(got), verdicts, ps.sent, at04IDs, tt.want, tt.sent)
			}
		})
	}
}

// Added by the /ship pre-landing review (Phase 2).
// Value: protects=AT-04.1 sends both privileged forms, escalation unset then true (D78); fails_when=a form is dropped or duplicated, so a policy that rejects only one form gives a false PASS;
// why_new=other tests pick answers by call order and never read the probe body; seam=none; no_claim=how a real policy treats either form (e2e: PSA rejects both)
func TestAT04SendsBothPrivilegedForms(t *testing.T) {
	var mu sync.Mutex
	var forms []string
	routes := map[string]http.HandlerFunc{"POST /api/v1/namespaces/tenant-a/pods": func(w http.ResponseWriter, r *http.Request) {
		var p corev1.Pod
		_ = json.NewDecoder(r.Body).Decode(&p)
		sc := p.Spec.Containers[0].SecurityContext
		if sc == nil || sc.Privileged == nil {
			acceptProbe(w, r) // the control
			return
		}
		form := "escalation unset"
		if e := sc.AllowPrivilegeEscalation; e != nil && *e {
			form = "escalation true"
		} else if e != nil {
			form = "escalation false"
		}
		mu.Lock()
		forms = append(forms, form)
		mu.Unlock()
		rejectProbe(w, r)
	}}
	specs, err := Select([]string{"AT-04.1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, routes), specs[0], ignore)
	if want := []string{"escalation unset", "escalation true"}; !slices.Equal(forms, want) {
		t.Errorf("privileged forms sent = %v, want %v", forms, want)
	}
}

func TestAT04AStoredProbeStopsTheCheck(t *testing.T) {
	// D84(3): a probe answer with a resourceVersion means a real dangerous pod was stored.
	stored := testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-h0st1","resourceVersion":"4712"}}`)
	ps := &probeServer{answers: map[string]http.HandlerFunc{"AT-04.1": rejectProbe, "AT-04.2": stored}}
	got := at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, ps.routes()), at04Spec(), ignore)
	if got[0].Verdict != report.Pass || got[1].Verdict != report.Error || !strings.Contains(got[1].Reason, "audit-tenant-probe-h0st1") || got[4].Verdict != report.Error {
		t.Errorf("results = %+v; want AT-04.1 kept and AT-04.2 to AT-04.5 ERROR naming the stored pod", got)
	}
	if slices.Contains(ps.sent, "AT-04.3") {
		t.Errorf("sent %v: no probe may follow a stored one", ps.sent)
	}
}

func TestAT04EveryPodCarriesTheLimitRangeMin(t *testing.T) {
	// ADR 0005, D85: the control and every probe state the LimitRange min as requests and limits.
	var mu sync.Mutex
	var sizes []string
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/namespaces/tenant-a/limitranges": testutil.JSON(http.StatusOK, `{"items":[{"spec":{"limits":[{"type":"Container","min":{"cpu":"100m","memory":"64Mi"},"max":{"cpu":"2","memory":"4Gi"},"default":{"cpu":"2","memory":"4Gi"},"defaultRequest":{"cpu":"2","memory":"4Gi"}}]}}]}`),
		"POST /api/v1/namespaces/tenant-a/pods": func(w http.ResponseWriter, r *http.Request) {
			var p corev1.Pod
			_ = json.NewDecoder(r.Body).Decode(&p)
			res := p.Spec.Containers[0].Resources
			mu.Lock()
			sizes = append(sizes, res.Requests.Cpu().String()+"/"+res.Requests.Memory().String()+" "+res.Limits.Cpu().String()+"/"+res.Limits.Memory().String())
			mu.Unlock()
			rejectProbe(w, r)
		},
	}
	routes["POST /api/v1/namespaces/tenant-a/pods"] = nth(func(w http.ResponseWriter, r *http.Request) { // the control is accepted
		var p corev1.Pod
		_ = json.NewDecoder(r.Body).Decode(&p)
		res := p.Spec.Containers[0].Resources
		mu.Lock()
		sizes = append(sizes, res.Requests.Cpu().String()+"/"+res.Requests.Memory().String()+" "+res.Limits.Cpu().String()+"/"+res.Limits.Memory().String())
		mu.Unlock()
		acceptProbe(w, r)
	}, routes["POST /api/v1/namespaces/tenant-a/pods"])
	at04(t.Context(), connectWith(t, &ssarServer{answers: mayPods}, routes), at04Spec(), ignore)
	if len(sizes) != 7 || slices.ContainsFunc(sizes, func(s string) bool { return s != "100m/64Mi 100m/64Mi" }) {
		t.Errorf("sizes sent = %v, want the control and all six probes at the LimitRange min, even with server-filled defaults", sizes)
	}
}

func TestTheDeadlineOnAnyQuestionLeavesTheResultsToTheRunner(t *testing.T) {
	// D84: a question that hangs past the deadline returns no result, so the runner says "not run: timeout".
	hangOn := func(key string, srv *ssarServer) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var in authzv1.SelfSubjectAccessReview
			_ = json.Unmarshal(body, &in)
			if a := in.Spec.ResourceAttributes; a != nil && a.Verb+" "+a.Resource == key && a.Subresource == "" {
				<-r.Context().Done()
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			srv.ServeHTTP(w, r)
		}
	}
	answers := map[string]string{"create services": denied, "update services": denied, "patch services": denied}
	maps.Copy(answers, controllerAnswers())
	for _, tt := range []struct {
		name, hang string
		run        RunFunc
		spec       Spec
	}{
		{"AT-04 create pods", "create pods", at04, at04Spec()},
		{"AT-04 a controller", "patch deployments", at04, at04Spec()},
		{"AT-04 the anchor", "list pods", at04, at04Spec()},
		{"AT-05 create services", "create services", at05, at05Spec()},
		{"AT-05 the anchor", "list pods", at05, at05Spec()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := connect(t, hangOn(tt.hang, &ssarServer{answers: answers}))
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			if got := tt.run(ctx, c, tt.spec, ignore); len(got) != 0 {
				t.Errorf("results = %+v; want none, so the runner says \"not run: timeout\"", got)
			}
		})
	}
}
