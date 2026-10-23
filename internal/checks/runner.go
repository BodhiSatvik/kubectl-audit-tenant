package checks

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

const hintBug = "a bug in audit-tenant; please report it with --debug output"

// Run executes the specs one after another, in order, under one context. Every selected result
// comes back: a result a check did not produce is an ERROR that says why (the deadline, an
// interruption, a crash, or a check that returned too little).
func Run(ctx context.Context, c *cluster.Client, specs []Spec, timeout time.Duration) []report.Result {
	var out []report.Result
	for _, s := range specs {
		var got []report.Result
		var crashed any
		if ctx.Err() == nil {
			s.earlier = slices.Clone(out) // a copy: an abandoned check may still read it while out grows
			got, crashed = runOne(ctx, c, s)
		}
		byID := map[string]report.Result{} // by ID, not count: a wrong or repeated ID must not hide a result
		for _, g := range got {
			if _, seen := byID[g.ID]; !seen {
				byID[g.ID] = g
			}
		}
		for _, r := range s.Results {
			if g, ok := byID[r.ID]; ok {
				out = append(out, g)
				continue
			}
			reason, hint := notRun(ctx, timeout, c)
			if crashed != nil {
				reason, hint = fmt.Sprintf("the check crashed: %v", crashed), hintBug
			}
			out = append(out, s.result(r, report.Error, "-", reason, hint))
		}
	}
	return out
}

// runOne runs a check on its own goroutine, so a request the context cannot cancel (a hung
// credential helper) still ends at the deadline, and a panic becomes an ERROR, not a crash. A FAIL
// the check proved is kept whenever the check leaves it out, does not return, or crashes (D61, D84).
func runOne(ctx context.Context, c *cluster.Client, s Spec) (results []report.Result, crashed any) {
	var mu sync.Mutex
	proven := map[string]report.Result{} // the latest FAIL per result ID
	prove := func(r report.Result) {
		mu.Lock()
		defer mu.Unlock()
		proven[r.ID] = r
	}
	keep := func() []report.Result {
		mu.Lock()
		defer mu.Unlock()
		return slices.Collect(maps.Values(proven))
	}
	type outcome struct {
		results []report.Result
		crashed any
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{results: keep(), crashed: p}
			}
		}()
		done <- outcome{results: append(s.Run(ctx, c, s, prove), keep()...)} // Run keeps the first result per ID: a returned one wins, a proven FAIL fills a gap (D84)
	}()
	select {
	case o := <-done:
		return o.results, o.crashed
	case <-ctx.Done():
		// shortcut: 100ms grace for a check handing back its partial results; a check that ignores ctx is abandoned with only the FAILs it proved, upgrade if a check ever needs longer to unwind
		select {
		case o := <-done:
			return o.results, o.crashed
		case <-time.After(100 * time.Millisecond):
			return keep(), nil
		}
	}
}

// LeftoverHint names the probe objects a create that was not verified as a dry run may have left (D103).
func LeftoverHint(namespace string) string {
	return "a create was not verified as a dry run, so list leftovers: kubectl get pods,services -n " + namespace + " -l app.kubernetes.io/managed-by=audit-tenant"
}

// notRun explains a result the check did not produce. After a create answer that was not a
// verified dry run, a timeout or an interruption cannot claim the cluster is unchanged (D182).
func notRun(ctx context.Context, timeout time.Duration, c *cluster.Client) (reason, hint string) {
	unverified := c != nil && c.Unverified.Load()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) && unverified:
		return fmt.Sprintf("not run: timeout after %s", timeout), "raise --timeout, or run fewer checks with --checks; " + LeftoverHint(c.Namespace)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("not run: timeout after %s", timeout), "raise --timeout, or run fewer checks with --checks"
	case ctx.Err() != nil && unverified:
		return "interrupted", "re-run; " + LeftoverHint(c.Namespace)
	case ctx.Err() != nil:
		return "interrupted", "re-run; the cluster was not changed"
	}
	return "no result from the check", hintBug
}
