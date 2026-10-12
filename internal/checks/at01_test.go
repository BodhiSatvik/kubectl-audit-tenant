package checks

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
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
		{"allowed wins over an evaluationError", `{"allowed":true,"evaluationError":"webhook timed out"}`, true, false, ""},
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
	for name, body := range map[string]string{
		"no echoed question":      `{"status":{"allowed":false}}`, // a proxy or an empty object decodes to allowed=false
		"another question echoed": `{"spec":{"resourceAttributes":{"verb":"list","resource":"nodes"}},"status":{"allowed":false}}`,
	} {
		t.Run(name+" is an error", func(t *testing.T) {
			c := connect(t, testutil.JSON(http.StatusCreated, body))
			_, err := can(t.Context(), c, "get", "", "pods", "tenant-a")
			if err == nil || !strings.Contains(err.Error(), "does not repeat the question") || hintFor(err) != hintNotAPIServer {
				t.Errorf("can() error = %v, want a not-the-API-server error: such an answer must never read as a denial", err)
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
		{Verb: "create", Group: "authorization.k8s.io", Resource: "selfsubjectaccessreviews"},
		{Verb: "create", Resource: "namespaces"},
		{Verb: "create", Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings"},
		{Verb: "create", Resource: "persistentvolumes"},
	}
	if !slices.Equal(srv.asked, wantAsked) {
		t.Errorf("asked %+v, want the control and the four cluster-scoped questions once each (startup's answer reused)", srv.asked)
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
	for name, tt := range map[string]struct {
		answers map[string]string
		hint    string // a broken authorizer and a failed request need different advice
	}{
		"evaluationError": {map[string]string{"list nodes": denied, "create namespaces": denied, "create clusterrolebindings": `{"allowed":false,"evaluationError":"authorizer webhook unavailable"}`}, hintEvaluation},
		"request failure": {map[string]string{"list nodes": denied, "create namespaces": denied}, hintRequestFailed},
	} {
		t.Run(name, func(t *testing.T) {
			got := at01(t.Context(), connect(t, &ssarServer{answers: tt.answers}), at01Spec())[0]
			if got.Verdict != report.Error || got.Hint != tt.hint || !strings.Contains(got.Reason, "clusterrolebindings") {
				t.Errorf("at01() = %+v, want ERROR naming the failed question with hint %q", got, tt.hint)
			}
			if name == "evaluationError" && !strings.Contains(got.Reason, "authorizer webhook unavailable") {
				t.Errorf("reason %q should carry the server text", got.Reason)
			}
		})
	}
}

func TestAT01AProvenGrantIsAFailWhateverElseGoesWrong(t *testing.T) {
	evalErr := `{"allowed":false,"evaluationError":"webhook timed out"}`
	tests := []struct {
		name      string
		answers   map[string]string
		wantGrant string
		wantAsked int // startup's question, the control, then AT-01's questions not reused
	}{
		{"a failed request after the grant", map[string]string{"list nodes": allowed}, "list nodes", 3},
		{"an evaluationError before the grant", map[string]string{"list nodes": denied, "create namespaces": evalErr, "create clusterrolebindings": allowed, "create persistentvolumes": denied},
			"create clusterrolebindings", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &ssarServer{answers: tt.answers}
			got := at01(t.Context(), connect(t, srv), at01Spec())[0]
			if got.Verdict != report.Fail || !strings.Contains(got.Reason, tt.wantGrant) || len(srv.asked) != tt.wantAsked {
				t.Errorf("at01() = %+v after %d questions; want FAIL naming %s after %d", got, len(srv.asked), tt.wantGrant, tt.wantAsked)
			}
		})
	}
	t.Run("the deadline before any grant", func(t *testing.T) {
		// Without a proven grant, an unfinished AT-01 is the runner's ERROR (exit 2), never PASS.
		srv := &ssarServer{answers: map[string]string{"list nodes": denied}}
		hang := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body) // read the body, or the server never notices the client leaving
			if strings.Contains(string(body), "namespaces") {
				<-r.Context().Done()
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			srv.ServeHTTP(w, r)
		})
		c := connect(t, hang)
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		if got := at01(ctx, c, at01Spec()); len(got) != 0 {
			t.Errorf("at01() = %+v; want no result, so the runner reports the timeout", got)
		}
	})
	t.Run("the deadline after the grant", func(t *testing.T) {
		srv := &ssarServer{answers: map[string]string{"list nodes": allowed}}
		hang := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body) // read the body, or the server never notices the client leaving
			if strings.Contains(string(body), "namespaces") {
				<-r.Context().Done() // the second question hangs past the deadline
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			srv.ServeHTTP(w, r)
		})
		c := connect(t, hang)
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		if got := at01(ctx, c, at01Spec()); len(got) != 1 || got[0].Verdict != report.Fail || !strings.Contains(got[0].Reason, "list nodes") {
			t.Errorf("at01() = %+v; want FAIL naming list nodes: the deadline must not hide a proven grant", got)
		}
	})
}

func TestAT01StopsAskingAfterAFailedRequest(t *testing.T) {
	// create namespaces gets HTTP 500; a 429 or 5xx would repeat, and client-go retries each one.
	srv := &ssarServer{answers: map[string]string{"list nodes": denied}}
	got := at01(t.Context(), connect(t, srv), at01Spec())[0]
	if got.Verdict != report.Error || got.Hint != hintRequestFailed || len(srv.asked) != 3 {
		t.Errorf("at01() = %+v after %d questions; want ERROR with the re-run hint after 3 (startup, control, create namespaces)", got, len(srv.asked))
	}
}

func TestAT01PositiveControlAcceptsAnAuthorizerThatDenies(t *testing.T) {
	// An access proxy with its own roles (Teleport-style) answers every question "no, denied",
	// the control included. A reflected request cannot carry denied, so this is a real answer (D57).
	deny := `{"allowed":false,"denied":true,"reason":"access denied by role"}`
	srv := &ssarServer{answers: map[string]string{"list nodes": deny, "create selfsubjectaccessreviews": deny, "create namespaces": deny, "create clusterrolebindings": deny, "create persistentvolumes": deny}}
	if got := at01(t.Context(), connect(t, srv), at01Spec())[0]; got.Verdict != report.Pass || !strings.Contains(got.Reason, "explicit deny") {
		t.Errorf("at01() = %+v; want PASS with the explicit denies noted", got)
	}
}

func TestAT01PositiveControlCatchesAnAnswerNotFromTheAPIServer(t *testing.T) {
	// A proxy that reflects the request body: the echo check passes and every answer reads denied.
	reflect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		testutil.JSON(http.StatusCreated, string(body))(w, r)
	})
	for name, h := range map[string]http.Handler{
		"a reflecting proxy":    reflect,
		"a server that says no": &ssarServer{answers: map[string]string{"list nodes": denied, "create selfsubjectaccessreviews": denied}},
	} {
		t.Run(name, func(t *testing.T) {
			got := at01(t.Context(), connect(t, h), at01Spec())[0]
			if got.Verdict != report.Error || got.Hint != hintNotAPIServer {
				t.Errorf("at01() = %+v; want ERROR with the not-the-API-server hint, never PASS", got)
			}
		})
	}
}
