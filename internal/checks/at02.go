package checks

import (
	"context"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at02 asks whether the identity may get or list secrets in --other-namespace; both must be denied.
// An SSAR names no secret, so grants limited by resourceNames are not detected (DESIGN.md section 5).
func at02(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	other := c.OtherNamespace
	questions := []question{
		{verb: "get", resource: "secrets", namespace: other},
		{verb: "list", resource: "secrets", namespace: other},
	}
	explicit, res, stop := denials(ctx, c, spec, prove, questions,
		"remove the RoleBinding or ClusterRoleBinding that lets this identity read secrets in "+other+"; a tenant reads secrets only in its own namespace")
	if stop {
		return res
	}
	return []report.Result{spec.result(spec.Results[0], report.Pass, "RBAC",
		"no general get or list on secrets in "+other+" (named-secret grants are not tested)"+explicitNote(explicit), "")}
}
