package checks

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

// fake builds a spec with n results whose Run returns what produce gives back.
func fake(id string, n int, produce func(ctx context.Context, s Spec) []report.Result) Spec {
	s := Spec{ID: id, Name: id, Kind: report.Config, Severity: report.Low}
	for i := range n {
		rid := id
		if n > 1 {
			rid = id + "." + string(rune('1'+i))
		}
		s.Results = append(s.Results, ResultSpec{ID: rid, Name: rid, MTB: "-"})
	}
	s.Run = func(ctx context.Context, _ *cluster.Client, s Spec) []report.Result { return produce(ctx, s) }
	return s
}

func allPass(_ context.Context, s Spec) []report.Result {
	var out []report.Result
	for _, r := range s.Results {
		out = append(out, s.result(r, report.Pass, "config", "fine", ""))
	}
	return out
}

func ids(results []report.Result) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.ID)
	}
	return out
}

func TestRunnerRunsChecksInOrderAndKeepsEveryResult(t *testing.T) {
	specs := []Spec{fake("AT-01", 1, allPass), fake("AT-04", 3, allPass), fake("AT-05", 1, allPass)}
	got := Run(t.Context(), nil, specs, time.Minute)
	want := []string{"AT-01", "AT-04.1", "AT-04.2", "AT-04.3", "AT-05"}
	if !slices.Equal(ids(got), want) {
		t.Errorf("result IDs = %v, want %v", ids(got), want)
	}
	for _, r := range got {
		if r.Verdict != report.Pass || r.Kind != report.Config || r.Severity != report.Low || r.MTB != "-" {
			t.Errorf("%s = %+v, want a PASS carrying the spec's kind, severity and MTB", r.ID, r)
		}
	}
}

func TestRunnerSkipsChecksWithoutAnImplementation(t *testing.T) {
	catalogOnly := Spec{ID: "AT-10", Results: []ResultSpec{{ID: "AT-10"}}}
	got := Run(t.Context(), nil, []Spec{catalogOnly, fake("AT-01", 1, allPass)}, time.Minute)
	if want := []string{"AT-01"}; !slices.Equal(ids(got), want) {
		t.Errorf("result IDs = %v, want %v", ids(got), want)
	}
}

func TestRunnerCancelMidRunPrintsAPartialReport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failed := fake("AT-01", 1, func(_ context.Context, s Spec) []report.Result {
		return []report.Result{s.result(s.Results[0], report.Fail, "-", "accepted", "fix it")}
	})
	interrupted := fake("AT-04", 3, func(_ context.Context, s Spec) []report.Result {
		cancel() // Ctrl-C arrives after the first probe
		return []report.Result{s.result(s.Results[0], report.Pass, "admission (PSA)", "rejected", "")}
	})
	never := fake("AT-05", 1, func(context.Context, Spec) []report.Result {
		t.Fatal("AT-05 must not run after the cancel")
		return nil
	})

	got := Run(ctx, nil, []Spec{failed, interrupted, never}, time.Minute)

	want := []string{"AT-01", "AT-04.1", "AT-04.2", "AT-04.3", "AT-05"}
	if !slices.Equal(ids(got), want) {
		t.Fatalf("result IDs = %v, want %v", ids(got), want)
	}
	if got[0].Verdict != report.Fail || got[1].Verdict != report.Pass {
		t.Errorf("results so far must be kept: %+v", got[:2])
	}
	for _, r := range got[2:] {
		if r.Verdict != report.Error || r.Reason != "interrupted" || r.Hint != "re-run; the cluster was not changed" {
			t.Errorf("%s = %+v, want ERROR interrupted with the re-run hint", r.ID, r)
		}
	}
}

func TestRunnerDeadlineMarksUnfinishedChecksNotRun(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"u"}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.JSON(http.StatusCreated, `{"status":{"allowed":false}}`),
		"GET /api/v1/namespaces/tenant-a/resourcequotas":              func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
	}
	srv := testutil.NewServer(t, routes)
	const timeout = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	c, err := cluster.Connect(ctx, &rest.Config{Host: srv.URL}, cluster.Options{Namespace: "tenant-a", Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	blocked := fake("AT-06", 1, func(context.Context, Spec) []report.Result {
		if _, err := c.Quotas(); err == nil {
			t.Error("the blocked list should fail at the deadline")
		}
		return nil // the check ran out of time before it could decide
	})
	after := fake("AT-07", 1, func(context.Context, Spec) []report.Result {
		_, err := c.Clientset.CoreV1().LimitRanges("tenant-a").List(ctx, metav1.ListOptions{})
		if err == nil {
			t.Error("a request after the deadline must fail")
		}
		return nil
	})

	got := Run(ctx, c, []Spec{blocked, after}, timeout)

	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	for _, r := range got {
		if r.Verdict != report.Error || r.Reason != "not run: timeout after 200ms" || r.Hint == "" {
			t.Errorf("%s = %+v, want ERROR 'not run: timeout after 200ms' with a hint", r.ID, r)
		}
	}
}

func TestRunnerFillsAMissingResultAsAToolBug(t *testing.T) {
	short := fake("AT-04", 2, func(_ context.Context, s Spec) []report.Result {
		return []report.Result{s.result(s.Results[0], report.Pass, "-", "ok", "")}
	})
	got := Run(t.Context(), nil, []Spec{short}, time.Minute)
	if len(got) != 2 || got[1].ID != "AT-04.2" || got[1].Verdict != report.Error || got[1].Hint == "" {
		t.Errorf("results = %+v, want AT-04.2 filled as an ERROR with a hint", got)
	}
}
