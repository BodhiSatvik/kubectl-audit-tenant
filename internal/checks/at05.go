package checks

import (
	"context"
	"fmt"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at05 follows DESIGN.md section 5: RBAC first by SSAR, then a plain ClusterIP control Service,
// then the NodePort probe.
func at05(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	ns := c.Namespace
	for _, verb := range []string{"create", "update", "patch"} {
		may, err := can(ctx, c, question{verb: verb, resource: "services", namespace: ns})
		switch {
		case err != nil && ctx.Err() != nil:
			return nil
		case err != nil:
			return spec.all(report.Error, "-", err.Error(), hintFor(err))
		case may.allowed && verb == "create":
			return at05Probe(ctx, c, spec, prove)
		case may.allowed:
			return spec.all(report.Skip, "-",
				fmt.Sprintf("RBAC denies create services but allows %s services; an existing Service can be changed to type NodePort, so admission must be the fence and it was not exercised", verb),
				"run with --as <an identity that may create services>, or confirm your policy engine rejects type: NodePort on update")
		}
	}
	pass := spec.result(spec.Results[0], report.Pass, "RBAC", "RBAC denies create, update and patch of services; admission not exercised", "")
	pass.Note = "not tested: ways to gain service creation inside the namespace (RoleBindings, service account tokens), grants limited to named Services (resourceNames), and operators that create Services for the tenant"
	return rbacPass(ctx, c, spec, []report.Result{pass})
}

func at05Probe(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	services := c.Clientset.CoreV1().Services(c.Namespace)
	control, err := services.Create(ctx, probe.ControlService(), dryRun)
	if err != nil {
		return controlFailed(ctx, spec, err, "Service", "the namespace rejects even a plain ClusterIP Service; fix that first")
	}
	if res := notDryRun(spec, control, "control Service"); res != nil {
		return res
	}
	created, err := services.Create(ctx, probe.NodePortService(), dryRun)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	if err == nil {
		if stored := notDryRun(spec, created, "probe Service"); stored != nil {
			return stored // D84
		}
	}
	res := probed(spec, spec.Results[0], err, "a NodePort Service", "add a policy that rejects type: NodePort, or remove `create services` from the tenant role", prove)
	if res.Verdict == report.Pass {
		res.Note = "tested on create only: changing an existing Service to NodePort, and type LoadBalancer (which also opens node ports), were not tested" // D67, D74, D86
	}
	return []report.Result{res}
}
