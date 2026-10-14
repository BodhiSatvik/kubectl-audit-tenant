package checks

import (
	"context"
	"errors"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

const hintPSA = "set the namespace label pod-security.kubernetes.io/enforce=baseline (or restricted), or add a policy that blocks "

// at04Probes maps each AT-04 result to its probe forms (all must be rejected for a PASS), to what
// a FAIL says, and to the note on what a PASS did not test (D78, D86).
var at04Probes = map[string]struct {
	forms                 []func(*corev1.Pod)
	accepted, hint, limit string
}{
	"AT-04.1": {[]func(*corev1.Pod){probe.Privileged, probe.PrivilegedWithEscalation}, "a privileged container", hintPSA + "privileged containers",
		"tested in a regular container only (not initContainers or ephemeral containers)"},
	"AT-04.2": {[]func(*corev1.Pod){probe.HostPath}, "a pod with a hostPath volume", hintPSA + "hostPath volumes",
		"tested with a hostPath of / only (allowed path prefixes not tested)"},
	"AT-04.3": {[]func(*corev1.Pod){probe.HostPID}, "a pod with hostPID", hintPSA + "hostPID", ""},
	"AT-04.4": {[]func(*corev1.Pod){probe.HostNetwork}, "a pod with hostNetwork", hintPSA + "hostNetwork", "hostPort was not tested"},
	"AT-04.5": {[]func(*corev1.Pod){probe.Escalation}, "a pod with allowPrivilegeEscalation: true",
		"set the namespace label pod-security.kubernetes.io/enforce=restricted (baseline does not block this), or add a policy that forbids allowPrivilegeEscalation (Kyverno disallow-privilege-escalation, or a ValidatingAdmissionPolicy)",
		"tested in a regular container only (not initContainers or ephemeral containers)"},
}

// worse orders the verdicts of one result's probe forms: a proven FAIL wins, then ERROR, SKIP, PASS.
var worse = map[report.Verdict]int{report.Fail: 3, report.Error: 2, report.Skip: 1, report.Pass: 0}

// podControllers are the kinds whose pods RBAC on pods does not cover (ADR 0005, D66).
var podControllers = []struct{ group, resource string }{
	{"apps", "deployments"}, {"apps", "replicasets"}, {"apps", "statefulsets"}, {"apps", "daemonsets"},
	{"batch", "jobs"}, {"batch", "cronjobs"}, {"", "replicationcontrollers"},
}

// scalable are the controllers whose /scale subresource makes them create pods from their template (D77).
var scalable = []struct{ group, resource string }{{"apps", "deployments"}, {"apps", "replicasets"}, {"apps", "statefulsets"}, {"", "replicationcontrollers"}}

const (
	at04RBACReason = "RBAC denies create pods; create, update, patch and scale of the built-in pod controllers; and adding ephemeral containers; admission not exercised"
	at04RBACNote   = "not tested: ways to gain pod creation inside the namespace (creating Secrets, RoleBindings or service account tokens, impersonating its service accounts), grants limited to named objects (resourceNames), and controllers from CRDs (such as Argo Workflows or Tekton)"
)

// at04 follows DESIGN.md section 5: RBAC first by SSAR, so a probe rejection is never RBAC; then a
// compliant control pod, verified as a dry run (D79); then the dry-run probes of each selected
// result (AT-04.1 sends two forms and keeps the worse verdict, D78).
func at04(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	ns := c.Namespace
	may, err := can(ctx, c, question{verb: "create", resource: "pods", namespace: ns})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return spec.all(report.Error, "-", err.Error(), hintFor(err))
	}
	if !may.allowed {
		return at04Controllers(ctx, c, spec)
	}
	ranges, err := c.LimitRanges()
	if err != nil && !apierrors.IsForbidden(err) { // only a 403 falls back to 1m/1Mi sizes; any other API error is ERROR
		if ctx.Err() != nil {
			return nil
		}
		return spec.all(report.Error, "-", "could not list limitranges to size the probes: "+err.Error(), hintRequestFailed)
	}
	if res := storedEarlier(c, spec); res != nil {
		return res
	}
	size := probe.Size(ranges) // a 403 leaves ranges nil: 1m and 1Mi
	pods := c.Clientset.CoreV1().Pods(ns)
	control, err := pods.Create(ctx, probe.ControlPod(size), dryRun)
	if err != nil {
		return controlFailed(ctx, spec, err, "pod", "the namespace rejects even a compliant pod; fix that first")
	}
	if res := notDryRun(c, spec, control, "control pod"); res != nil {
		return res
	}
	var out []report.Result
	for i, r := range spec.Results {
		p := at04Probes[r.ID]
		var res report.Result
		for f, form := range p.forms {
			pod := probe.ControlPod(size)
			form(pod)
			created, err := pods.Create(ctx, pod, dryRun)
			if err != nil && ctx.Err() != nil {
				return out // interrupted or out of time: the runner says so for the rest
			}
			if unavailable(err) { // it would repeat for every later probe, each with client-go's retries, and spend the --timeout (D91)
				rest := spec
				rest.Results = spec.Results[i:]
				o := probe.Classify(err)
				return append(out, rest.all(report.Error, "-", o.Reason, o.Hint)...)
			}
			if err == nil {
				rest := spec
				rest.Results = spec.Results[i:]
				if stored := notDryRun(c, rest, created, "probe pod"); stored != nil {
					return append(out, stored...) // no probe after one the server kept (D84)
				}
			}
			if got := probed(spec, r, err, p.accepted, p.hint, prove); f == 0 || worse[got.Verdict] > worse[res.Verdict] {
				res = got
			}
			if res.Verdict == report.Fail {
				break // proven; another form would be one more dangerous request (D84)
			}
		}
		if res.Verdict == report.Pass {
			res.Note = p.limit
		}
		out = append(out, res)
	}
	return out
}

// unavailable reports an answer that says nothing about the probe: a failed send, 429 Too Many
// Requests or a 5xx.
func unavailable(err error) bool {
	if err == nil {
		return false
	}
	s, ok := errors.AsType[*apierrors.StatusError](err)
	return !ok || s.ErrStatus.Code == http.StatusTooManyRequests || s.ErrStatus.Code >= http.StatusInternalServerError
}

// at04Controllers decides AT-04 when RBAC denies create pods: a controller the tenant may create
// or change still makes pods that only admission can stop, and so does an ephemeral container
// added to a running pod; admission was not exercised.
func at04Controllers(ctx context.Context, c *cluster.Client, spec Spec) []report.Result {
	var questions []question
	for _, k := range podControllers {
		for _, verb := range []string{"create", "update", "patch"} {
			questions = append(questions, question{verb: verb, group: k.group, resource: k.resource, namespace: c.Namespace})
		}
	}
	for _, verb := range []string{"update", "patch"} {
		questions = append(questions, question{verb: verb, resource: "pods", subresource: "ephemeralcontainers", namespace: c.Namespace})
	}
	for _, k := range scalable {
		for _, verb := range []string{"update", "patch"} {
			questions = append(questions, question{verb: verb, group: k.group, resource: k.resource, subresource: "scale", namespace: c.Namespace})
		}
	}
	for _, q := range questions {
		may, err := can(ctx, c, q)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return spec.all(report.Error, "-", err.Error(), hintFor(err))
		}
		if may.allowed {
			return spec.all(report.Skip, "-",
				"RBAC denies create pods but allows "+q.action()+"; pods a controller creates (or scales up), and containers added to running pods, bypass the tenant's create-pods RBAC, so admission must be the fence and it was not exercised",
				"run with --as <an identity that may create pods>, or confirm AT-09 and your policy engine cover controller-created pods")
		}
	}
	pass := spec.all(report.Pass, "RBAC", at04RBACReason, "")
	for i := range pass {
		pass[i].Note = at04RBACNote
	}
	return rbacPass(ctx, c, spec, pass)
}
