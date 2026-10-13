package checks

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

const hintPSA = "set the namespace label pod-security.kubernetes.io/enforce=baseline (or restricted), or add a policy that blocks "

// at04Probes maps each AT-04 result to its probe and to what a FAIL says.
var at04Probes = map[string]struct {
	mutate         func(*corev1.Pod)
	accepted, hint string
}{
	"AT-04.1": {probe.Privileged, "a privileged container", hintPSA + "privileged containers"},
	"AT-04.2": {probe.HostPath, "a pod with a hostPath volume", hintPSA + "hostPath volumes"},
	"AT-04.3": {probe.HostPID, "a pod with hostPID", hintPSA + "hostPID"},
	"AT-04.4": {probe.HostNetwork, "a pod with hostNetwork", hintPSA + "hostNetwork"},
	"AT-04.5": {probe.RootWithEscalation, "a pod running as root with privilege escalation",
		"set the namespace label pod-security.kubernetes.io/enforce=restricted (baseline does not block this), or add a policy that forbids runAsUser: 0 and allowPrivilegeEscalation (Kyverno require-run-as-nonroot and disallow-privilege-escalation, or a ValidatingAdmissionPolicy)"},
}

// podControllers are the kinds whose pods RBAC on pods does not cover (ADR 0005).
var podControllers = []struct{ group, resource string }{
	{"apps", "deployments"}, {"apps", "replicasets"}, {"apps", "statefulsets"}, {"apps", "daemonsets"},
	{"batch", "jobs"}, {"batch", "cronjobs"},
}

// at04 follows DESIGN.md section 5: RBAC first by SSAR, so a probe rejection is never RBAC; then a
// compliant control pod; then one dry-run probe per selected result.
func at04(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result {
	ns := c.Namespace
	may, err := can(ctx, c, "create", "", "pods", ns)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return spec.all(report.Error, "-", err.Error(), hintFor(err))
	}
	if !may.allowed {
		return at04Controllers(ctx, c, spec)
	}
	size := probe.Size(c.LimitRanges())
	pods := c.Clientset.CoreV1().Pods(ns)
	if _, err := pods.Create(ctx, probe.ControlPod(size), dryRun); err != nil {
		return controlFailed(ctx, spec, err, "pod", "the namespace rejects even a compliant pod; fix that first")
	}
	var out []report.Result
	for _, r := range spec.Results {
		p := at04Probes[r.ID]
		pod := probe.ControlPod(size)
		p.mutate(pod)
		_, err := pods.Create(ctx, pod, dryRun)
		if err != nil && ctx.Err() != nil {
			return out // interrupted or out of time: the runner says so for the rest
		}
		out = append(out, probed(spec, r, err, p.accepted, p.hint, prove))
	}
	return out
}

// at04Controllers decides AT-04 when RBAC denies create pods: a controller the tenant may create
// or change still makes pods that only admission can stop, and admission was not exercised.
func at04Controllers(ctx context.Context, c *cluster.Client, spec Spec) []report.Result {
	for _, k := range podControllers {
		for _, verb := range []string{"create", "update", "patch"} {
			may, err := can(ctx, c, verb, k.group, k.resource, c.Namespace)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return spec.all(report.Error, "-", err.Error(), hintFor(err))
			}
			if may.allowed {
				return spec.all(report.Skip, "-",
					fmt.Sprintf("RBAC denies create pods but allows %s %s; pods created by a controller bypass the tenant's RBAC, so admission must be the fence and it was not exercised", verb, k.resource),
					"run with --as <an identity that may create pods>, or confirm AT-09 and your policy engine cover controller-created pods")
			}
		}
	}
	return spec.all(report.Pass, "RBAC", "RBAC denies create pods and any create, update or patch of a pod controller; admission not exercised", "")
}
