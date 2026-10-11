package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at01 asks four cluster-scoped "may I?" questions with no namespace; all four must be denied.
func at01(ctx context.Context, c *cluster.Client, spec Spec) []report.Result {
	questions := []struct{ verb, group, resource string }{
		{"list", "", "nodes"},
		{"create", "", "namespaces"},
		{"create", "rbac.authorization.k8s.io", "clusterrolebindings"},
		{"create", "", "persistentvolumes"},
	}
	r := spec.Results[0]
	var allowed, explicit []string
	for _, q := range questions {
		ans, err := can(ctx, c, q.verb, q.group, q.resource, "")
		if err != nil {
			if ctx.Err() != nil {
				return nil // interrupted or out of time: the runner says so
			}
			return []report.Result{spec.result(r, report.Error, "-", err.Error(), hintFor(err))}
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
	reason := fmt.Sprintf("RBAC denied all %d actions", len(questions))
	if len(explicit) > 0 {
		reason += " (explicit deny: " + strings.Join(explicit, ", ") + ")"
	}
	return []report.Result{spec.result(r, report.Pass, "RBAC", reason, "")}
}
