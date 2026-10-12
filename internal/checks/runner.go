package checks

import (
	"context"
	"errors"
	"fmt"
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
			reason, hint := notRun(ctx, timeout)
			if crashed != nil {
				reason, hint = fmt.Sprintf("the check crashed: %v", crashed), hintBug
			}
			out = append(out, s.result(r, report.Error, "-", reason, hint))
		}
	}
	return out
}

// runOne runs a check on its own goroutine, so a request the context cannot cancel (a hung
// credential helper) still ends at the deadline, and a panic becomes an ERROR, not a crash.
func runOne(ctx context.Context, c *cluster.Client, s Spec) (results []report.Result, crashed any) {
	type outcome struct {
		results []report.Result
		crashed any
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{crashed: p}
			}
		}()
		done <- outcome{results: s.Run(ctx, c, s)}
	}()
	select {
	case o := <-done:
		return o.results, o.crashed
	case <-ctx.Done():
		// shortcut: 100ms grace for a check handing back its partial results; a check that ignores ctx is abandoned, upgrade if a check ever needs longer to unwind
		select {
		case o := <-done:
			return o.results, o.crashed
		case <-time.After(100 * time.Millisecond):
			return nil, nil
		}
	}
}

// notRun explains a result the check did not produce.
func notRun(ctx context.Context, timeout time.Duration) (reason, hint string) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("not run: timeout after %s", timeout), "raise --timeout, or run fewer checks with --checks"
	case ctx.Err() != nil:
		return "interrupted", "re-run; the cluster was not changed"
	}
	return "no result from the check", hintBug
}
