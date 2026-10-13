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
		may, err := can(ctx, c, verb, "", "services", ns)
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
	return spec.all(report.Pass, "RBAC", "RBAC denies create, update and patch of services; admission not exercised", "")
}

func at05Probe(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	services := c.Clientset.CoreV1().Services(c.Namespace)
	if _, err := services.Create(ctx, probe.ControlService(), dryRun); err != nil {
		return controlFailed(ctx, spec, err, "Service", "the namespace rejects even a plain ClusterIP Service; fix that first")
	}
	_, err := services.Create(ctx, probe.NodePortService(), dryRun)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return []report.Result{probed(spec, spec.Results[0], err, "a NodePort Service", "add a policy that rejects type: NodePort, or remove `create services` from the tenant role", prove)}
}
