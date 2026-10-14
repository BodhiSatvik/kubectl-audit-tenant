package checks

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// dryRun is the only way a check creates anything: the API server runs admission and stores
// nothing. The safety transport refuses a create without it.
var dryRun = metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}

// hintNoRights explains an RBAC-only PASS refused because the identity has no rights at all.
const hintNoRights = "check -n, --as and --as-group: the identity may be misspelled, miss its identity-provider prefix, or have no role in this namespace"

// controlFailed turns a control probe that did not get through into the results of every
// selected result: SKIP when admission, a quota or a LimitRange rejected it (the probes would
// prove nothing), ERROR when the classifier calls it an API failure (5xx, 401, a malformed
// control, an unknown answer, or no answer: DESIGN.md section 6, D65), nil when ctx ended.
func controlFailed(ctx context.Context, spec Spec, err error, what, hint string) []report.Result {
	if ctx.Err() != nil {
		return nil
	}
	o := probe.Classify(err)
	if o.Verdict == report.Error {
		return spec.all(report.Error, "-", o.Reason, o.Hint)
	}
	return spec.all(report.Skip, "-", "the compliant control "+what+" was rejected, so the probes prove nothing: "+o.Reason, cmp.Or(o.Hint, hint))
}

// notDryRun stops a check whose answer to a dry-run create shows the server stored the object (a
// resourceVersion: dryRun=All was dropped on the way) or did not come from the API server (no
// generated name). Every accepted answer is checked, the control's first (D79, D84); the results of
// spec are those not decided yet.
func notDryRun(spec Spec, m metav1.Object, what string) []report.Result {
	switch {
	case m.GetResourceVersion() != "":
		return spec.all(report.Error, "-", fmt.Sprintf("the server stored the %s %s: it did not honor dryRun=All, so the check stopped; delete that object", what, m.GetName()),
			"something between you and the API server drops dryRun=All (a proxy or gateway); run against the API server directly")
	case !strings.HasPrefix(m.GetName(), probe.NamePrefix):
		return spec.all(report.Error, "-", "the answer to the dry-run "+what+" has no generated name, so it did not come from the API server", hintNotAPIServer)
	}
	return nil
}

// rbacPass returns pass, the results of a check that RBAC alone decided, only when the identity
// holds some right in the namespace; an identity denied everything (a misspelled --as user)
// would PASS without proving anything (D69). AT-01 to AT-05 share one answer per run (D80).
func rbacPass(ctx context.Context, c *cluster.Client, spec Spec, pass []report.Result) []report.Result {
	a := c.AnchorQuestion()
	q := question{verb: a.Verb, group: a.Group, resource: a.Resource, subresource: a.Subresource, namespace: a.Namespace} // every field, so the client's once-per-run match holds
	may, err := can(ctx, c, q)
	switch {
	case err != nil && ctx.Err() != nil:
		return nil
	case err != nil:
		return spec.all(report.Error, "-", err.Error(), hintFor(err))
	case !may.allowed:
		return spec.all(report.Skip, "-", "this identity may not even "+q.action()+", so an RBAC-only PASS would prove nothing", hintNoRights)
	}
	return pass
}

// probed is one probe's result. An accepted probe is a FAIL that says what was accepted; it is
// handed to prove at once (D61).
func probed(spec Spec, r ResultSpec, err error, accepted, failHint string, prove func(report.Result)) report.Result {
	o := probe.Classify(err)
	if o.Verdict == report.Fail {
		o.Reason, o.Hint = accepted+" was accepted in a dry run", failHint
	}
	res := spec.result(r, o.Verdict, o.Layer, o.Reason, o.Hint)
	if res.Verdict == report.Fail {
		prove(res)
	}
	return res
}
