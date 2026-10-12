package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at01 asks four cluster-scoped "may I?" questions with no namespace; all four must be denied.
// A positive control comes first (D55).
func at01(ctx context.Context, c *cluster.Client, spec Spec) []report.Result {
	questions := []struct{ verb, group, resource string }{
		{"list", "", "nodes"},
		{"create", "", "namespaces"},
		{"create", "rbac.authorization.k8s.io", "clusterrolebindings"},
		{"create", "", "persistentvolumes"},
	}
	r := spec.Results[0]
	// Positive control (D55): any API server allows creating the SSAR it just served. A "no" means
	// something else answered; a proxy that reflects the request would otherwise read as all-denied.
	control, err := can(ctx, c, "create", "authorization.k8s.io", "selfsubjectaccessreviews", "")
	if err == nil && !control.allowed {
		err = &notAPIServerError{"the answer to \"may I create selfsubjectaccessreviews?\" was no, although the request was just served; something other than the API server answered"}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil // interrupted or out of time: the runner says so
		}
		return []report.Result{spec.result(r, report.Error, "-", err.Error(), hintFor(err))}
	}
	var allowed, explicit []string
	var failed error // the first question whose answer could not be read; an allowed answer still wins (D52)
	ended := false
	for _, q := range questions {
		ans, err := can(ctx, c, q.verb, q.group, q.resource, "")
		if err != nil {
			if ctx.Err() != nil {
				ended = true // a grant already proven is still reported below
				break
			}
			if failed == nil {
				failed = err
			}
			if _, ok := errors.AsType[*evaluationError](err); !ok {
				break // a failed request would repeat for every question, and client-go's retries could spend the whole --timeout
			}
			continue
		}
		action := q.verb + " " + q.resource
		if ans.allowed {
			allowed = append(allowed, action)
		} else if ans.denied {
			explicit = append(explicit, action)
		}
	}
	if len(allowed) > 0 {
		return []report.Result{spec.result(r, report.Fail, "RBAC", "RBAC allows "+strings.Join(allowed, ", "),
			"remove the cluster-scoped grants from this identity's ClusterRoleBindings; tenant permissions belong in Roles bound inside the namespace")}
	}
	if ended {
		return nil // interrupted or out of time: the runner says so
	}
	if failed != nil {
		return []report.Result{spec.result(r, report.Error, "-", failed.Error(), hintFor(failed))}
	}
	reason := fmt.Sprintf("RBAC denied all %d actions", len(questions))
	if len(explicit) > 0 {
		reason += " (explicit deny: " + strings.Join(explicit, ", ") + ")"
	}
	return []report.Result{spec.result(r, report.Pass, "RBAC", reason, "")}
}
