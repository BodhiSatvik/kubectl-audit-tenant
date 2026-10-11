package checks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// Run executes the specs one after another, in order, under one context. Every selected result
// comes back: a result a check did not produce is an ERROR that says why (the deadline, an
// interruption, or a bug in the check).
func Run(ctx context.Context, c *cluster.Client, specs []Spec, timeout time.Duration) []report.Result {
	var out []report.Result
	for _, s := range specs {
		if s.Run == nil {
			continue // shortcut: catalog-only until its task lands; at v0.1.0 drop unshipped checks from Registry instead (D28)
		}
		var got []report.Result
		if ctx.Err() == nil {
			got = s.Run(ctx, c, s)
		}
		out = append(out, got...)
		for _, r := range s.Results[min(len(got), len(s.Results)):] {
			reason, hint := notRun(ctx, timeout)
			out = append(out, s.result(r, report.Error, "-", reason, hint))
		}
	}
	return out
}

// notRun explains a result the check did not produce.
func notRun(ctx context.Context, timeout time.Duration) (reason, hint string) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("not run: timeout after %s", timeout), "raise --timeout, or run fewer checks with --checks"
	case ctx.Err() != nil:
		return "interrupted", "re-run; the cluster was not changed"
	}
	return "no result from the check", "a bug in audit-tenant; please report it with --debug output"
}
