package checks

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at04Spec() Spec { return Registry[3] }

var at04IDs = []string{"AT-04.1", "AT-04.2", "AT-04.3", "AT-04.4", "AT-04.5"}

const psaDenial = `pods "audit-tenant-probe-g54wp" is forbidden: violates PodSecurity "restricted:latest": privileged (container "probe" must not set securityContext.privileged=true)`

// controllerAnswers answers the 18 questions AT-04 asks when RBAC denies create pods.
func controllerAnswers(answer string) map[string]string {
	m := map[string]string{"list nodes": denied, "create pods": denied}
	for _, kind := range []string{"deployments", "replicasets", "statefulsets", "daemonsets", "jobs", "cronjobs"} {
		for _, verb := range []string{"create", "update", "patch"} {
			m[verb+" "+kind] = answer
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
	if want := append([]string{"control"}, at04IDs...); !slices.Equal(ps.sent, want) {
		t.Errorf("sent %v, want %v: the control first, then one probe per result", ps.sent, want)
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
		"AT-04.5": "a pod running as root with privilege escalation was accepted in a dry run",
	}
	for _, r := range got {
		if r.Verdict != report.Fail || r.Layer != "-" || r.Reason != want[r.ID] || !strings.Contains(r.Hint, "pod-security.kubernetes.io/enforce=") {
			t.Errorf("%s = %+v, want FAIL %q with the PSA hint", r.ID, r, want[r.ID])
		}
	}
	if h := got[4].Hint; !strings.Contains(h, "restricted") || !strings.Contains(h, "baseline does not block") || !strings.Contains(h, "runAsUser: 0") {
		t.Errorf("AT-04.5 hint = %q, want both fixes: restricted, or a policy against runAsUser: 0 and escalation", h)
	}
	if !slices.Equal(proven, at04IDs) {
		t.Errorf("proved %v, want every FAIL handed to the runner at once (D61)", proven)
	}
}

func TestAT04RBACDeniesPodsAndEveryController(t *testing.T) {
	ps := &probeServer{}
	ssar := &ssarServer{answers: controllerAnswers(denied)}
	got := at04(t.Context(), connectWith(t, ssar, ps.routes()), at04Spec(), ignore)

	for _, r := range got {
		if r.Verdict != report.Pass || r.Layer != "RBAC" || r.Reason != "RBAC denies create pods and any create, update or patch of a pod controller; admission not exercised" {
			t.Errorf("%s = %+v, want PASS by RBAC", r.ID, r)
		}
	}
	if len(got) != 5 || len(ps.sent) != 0 || len(ssar.asked) != 1+1+18 {
		t.Errorf("%d results, %d probes, %d questions; want 5 results, no probe, startup + create pods + 18", len(got), len(ps.sent), len(ssar.asked))
	}
}

func TestAT04AControllerTheTenantMayUseIsASkip(t *testing.T) {
	answers := controllerAnswers(denied)
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
		if r.Verdict != report.Skip || !strings.Contains(r.Reason, "exceeded quota") || r.Hint != "the namespace rejects even a compliant pod; fix that first" {
			t.Errorf("%s = %+v, want SKIP with the control's rejection text and the fix-that-first hint", r.ID, r)
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
		answers := controllerAnswers(denied)
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
