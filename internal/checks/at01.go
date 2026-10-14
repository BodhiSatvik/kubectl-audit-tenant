package checks

import (
	"context"
	"fmt"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at01 asks four cluster-scoped "may I?" questions with no namespace; all four must be denied.
// A positive control comes before a PASS (D55).
func at01(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	questions := []question{
		{verb: "list", resource: "nodes"},
		{verb: "create", resource: "namespaces"},
		{verb: "create", group: "rbac.authorization.k8s.io", resource: "clusterrolebindings"},
		{verb: "create", resource: "persistentvolumes"},
	}
	explicit, res, stop := denials(ctx, c, spec, prove, questions,
		"remove the cluster-scoped grants from this identity's ClusterRoleBindings; tenant permissions belong in Roles bound inside the namespace")
	if stop {
		return res
	}
	r := spec.Results[0]
	// Positive control (D55), asked only before a PASS so it can never hide a grant: any API server
	// allows creating the SSAR it just served. A bare "no" means something else answered. A "no"
	// with denied set came from an authorizer (an access proxy with its own roles, D57); a
	// reflected request cannot carry it, so the denials stand and the reason says so.
	control, err := can(ctx, c, question{verb: "create", group: "authorization.k8s.io", resource: "selfsubjectaccessreviews"})
	if err == nil && !control.allowed && !control.denied {
		err = &notAPIServerError{"the answer to \"may I create selfsubjectaccessreviews?\" was no, although the request was just served; something other than the API server answered"}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil // interrupted or out of time: the runner says so
		}
		return []report.Result{spec.result(r, report.Error, "-", err.Error(), hintFor(err))}
	}
	reason := fmt.Sprintf("RBAC denied all %d actions", len(questions)) + explicitNote(explicit)
	if control.denied {
		reason += "; the access path also denied create selfsubjectaccessreviews (an access proxy with its own roles?)"
	}
	return rbacPass(ctx, c, spec, []report.Result{spec.result(r, report.Pass, "RBAC", reason, "")})
}
