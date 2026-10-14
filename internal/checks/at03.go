package checks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// wildcard names AT-03's cluster-wide "*" question in reasons; LooksLikeAdmin finds it there.
const wildcard = "every verb on every resource cluster-wide (*)"

// at03 asks the cluster-scoped escalation questions of DESIGN.md section 5; all must be denied.
// Rights that stay inside the namespace (a namespace-wide "*" Role, escalate and bind on its
// roles, impersonating its service accounts) are normal tenant-admin rights, so they are not
// asked (D62).
func at03(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	const rbac = "rbac.authorization.k8s.io"
	questions := []question{
		{verb: "escalate", group: rbac, resource: "clusterroles"},
		{verb: "bind", group: rbac, resource: "clusterroles"},
		{verb: "impersonate", resource: "users"},
		{verb: "impersonate", resource: "groups"},
		{verb: "impersonate", resource: "serviceaccounts"},
		{verb: "*", group: "*", resource: "*", label: wildcard},
	}
	explicit, res, stop := denials(ctx, c, spec, prove, questions,
		"remove the escalate, bind, impersonate and wildcard grants from this identity's ClusterRoleBindings; a tenant admin needs only a Role inside its namespace")
	if stop {
		return res
	}
	pass := spec.result(spec.Results[0], report.Pass, "RBAC", fmt.Sprintf("RBAC denied all %d actions", len(questions))+explicitDenies(explicit), "")
	pass.Note = "not tested: rights that stay inside the namespace (a namespace-wide *, escalate and bind on its roles, impersonating its service accounts), grants from RoleBindings in other namespaces (such as impersonating kube-system service accounts), and grants limited to named roles or users (resourceNames)"
	return rbacPass(ctx, c, spec, []report.Result{pass})
}

// LooksLikeAdmin reports whether AT-03 proved the cluster-wide wildcard. In self mode the header
// then suggests --as (DESIGN.md section 6).
func LooksLikeAdmin(results []report.Result) bool {
	return slices.ContainsFunc(results, func(r report.Result) bool {
		return r.ID == "AT-03" && r.Verdict == report.Fail && strings.Contains(r.Reason, wildcard)
	})
}
