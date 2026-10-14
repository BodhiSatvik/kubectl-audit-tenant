package checks

import (
	"context"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at02 asks whether the identity may get, list or watch secrets in --other-namespace; all must be
// denied. A watch streams every secret body too (D66). An SSAR names no secret, so grants limited
// by resourceNames are not detected (DESIGN.md section 5).
func at02(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	other := c.OtherNamespace
	questions := []question{
		{verb: "get", resource: "secrets", namespace: other},
		{verb: "list", resource: "secrets", namespace: other},
		{verb: "watch", resource: "secrets", namespace: other},
	}
	explicit, res, stop := denials(ctx, c, spec, prove, questions,
		"remove the RoleBinding or ClusterRoleBinding that lets this identity read secrets in "+other+"; a tenant reads secrets only in its own namespace")
	if stop {
		return res
	}
	pass := spec.result(spec.Results[0], report.Pass, "RBAC", "no general get, list or watch on secrets in "+other+explicitDenies(explicit), "")
	pass.Note = "not tested: grants limited to named secrets (resourceNames)" // D86, D92
	return rbacPass(ctx, c, spec, []report.Result{pass})
}
