package checks

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/headroom"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at10Estimate starts every AT-10 reason: the peak is an estimate, not a bound (ADR 0006).
const at10Estimate = "estimate: active pods during a rollout; terminating pods and uncounted kinds can add to usage; see used. "

// at10 sums the rollout peak of the namespace's Deployments and StatefulSets (internal/headroom)
// and compares it with every unscoped ResourceQuota, edge cases in the DESIGN.md section 5 order:
// no unscoped quota is FAIL; no workloads is PASS; a list this identity may not read (or the
// LimitRanges while a container has no explicit size) is SKIP, unless the partial sum is
// already over a hard value, which is FAIL.
func at10(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	ns := c.Namespace
	note := "not counted: DaemonSets, Jobs, CronJobs, bare Pods and Argo Rollouts"
	result := func(v report.Verdict, layer, reason, hint string) []report.Result {
		res := spec.result(spec.Results[0], v, layer, at10Estimate+reason, hint)
		res.Note = note
		return []report.Result{res}
	}
	grant := func(resources []string) string {
		return "grant `list` on " + andList(resources) + " in " + ns + " to this identity, or run with --as <admin>"
	}

	quotas, err := c.Quotas()
	if apierrors.IsForbidden(err) {
		return result(report.Skip, "-", "cannot list resourcequotas (forbidden), so there is nothing to compare against", grant([]string{"resourcequotas"}))
	} else if err != nil {
		return listFailed(ctx, c, spec, err, "resourcequotas")
	}
	var unscoped []corev1.ResourceQuota
	var scopedNames []string
	for _, q := range byName(quotas) {
		if len(q.Spec.Scopes) > 0 || q.Spec.ScopeSelector != nil {
			scopedNames = append(scopedNames, q.Name)
		} else {
			unscoped = append(unscoped, q)
		}
	}
	if len(unscoped) == 0 {
		reason := "no ResourceQuota to compare against"
		if len(scopedNames) > 0 {
			reason += " (scoped: " + strings.Join(scopedNames, ", ") + ")"
		}
		return result(report.Fail, layerConfig, reason, "add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods (see AT-06)")
	}

	// A list this identity may not read leaves the sum partial; any other error ends the check.
	var forbidden []string
	unreadable := func(err error, resource string) bool {
		if apierrors.IsForbidden(err) {
			forbidden = append(forbidden, resource)
			return true
		}
		return false
	}
	deployments, err := c.Clientset.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil && !unreadable(err, "deployments") {
		return listFailed(ctx, c, spec, err, "deployments")
	} else if err != nil {
		deployments = &appsv1.DeploymentList{}
	}
	statefulSets, err := c.Clientset.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil && !unreadable(err, "statefulsets") {
		return listFailed(ctx, c, spec, err, "statefulsets")
	} else if err != nil {
		statefulSets = &appsv1.StatefulSetList{}
	}
	counted := len(deployments.Items) + len(statefulSets.Items)
	if counted == 0 && len(forbidden) == 0 {
		return result(report.Pass, layerConfig, "no Deployments or StatefulSets; nothing to compare", "")
	}
	var hpas []autoscalingv2.HorizontalPodAutoscaler
	if counted > 0 { // HPAs only change the replicas of a counted workload
		list, err := c.Clientset.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
		switch {
		case err == nil:
			hpas = list.Items
		case !unreadable(err, "horizontalpodautoscalers"):
			return listFailed(ctx, c, spec, err, "horizontalpodautoscalers")
		}
	}
	ranges, err := c.LimitRanges()
	rangesForbidden := apierrors.IsForbidden(err)
	if err != nil && !rangesForbidden {
		return listFailed(ctx, c, spec, err, "limitranges")
	}

	defaults := headroom.DefaultsOf(ranges) // none when the list is forbidden
	var ws []headroom.Workload
	for _, d := range byName(deployments.Items) {
		ws = append(ws, headroom.Deployment(d, hpas, defaults))
	}
	for _, s := range byName(statefulSets.Items) {
		ws = append(ws, headroom.StatefulSet(s, hpas, defaults))
	}
	unsized, defaulted := 0, 0
	var runtimeClass []string
	for _, w := range ws {
		unsized += w.Pod.Unsized
		defaulted += w.Pod.Defaulted
		if w.Pod.RuntimeClass {
			runtimeClass = append(runtimeClass, w.Name)
		}
	}
	if rangesForbidden && unsized > 0 { // their LimitRange defaults are unknown
		forbidden = append(forbidden, "limitranges")
	}
	if len(runtimeClass) > 0 {
		note += "; pod overhead of a RuntimeClass (" + strings.Join(runtimeClass, ", ") + ")"
	}
	if unsized > 0 {
		note += "; " + containers(unsized) + " had no request or limit; counted as 0"
	}

	total := headroom.Total(ws)
	breakdown := headroom.Breakdown(ws)
	if breakdown == "" {
		breakdown = "no workloads counted"
	}
	var short, fits, used []string
	for _, q := range unscoped {
		for _, s := range headroom.Compare(q, total) {
			short = append(short, q.Name+" "+s.String())
		}
		var fit, use []string
		for _, key := range headroom.QuotaKeys(q) {
			k, _ := headroom.Canonical(key)
			need, hard := total[k], q.Spec.Hard[key]
			fit = append(fit, fmt.Sprintf("%s %s of %s", key, need.String(), hard.String()))
			if u, ok := q.Status.Used[key]; ok {
				use = append(use, fmt.Sprintf("%s %s", key, u.String()))
			}
		}
		fits = append(fits, q.Name+": "+strings.Join(fit, ", "))
		if len(use) == 0 {
			use = []string{"none reported"}
		}
		used = append(used, "used in "+q.Name+": "+strings.Join(use, ", "))
	}
	tail := " (" + breakdown + "); " + strings.Join(used, "; ")
	if defaulted > 0 {
		tail += "; defaults from LimitRange " + defaults.Name + " for " + containers(defaulted)
	}
	switch {
	case len(short) > 0: // a partial sum can only grow, so it is FAIL even with lists missing
		reason := strings.Join(short, "; ") + tail
		if len(forbidden) > 0 {
			reason += "; partial: cannot list " + strings.Join(forbidden, ", ") + " (forbidden)"
		}
		return result(report.Fail, layerConfig, reason,
			"raise the quota's hard values by the shortfall, or lower replicas, HPA maxReplicas or maxSurge; the estimate assumes every workload rolls out at once")
	case len(forbidden) > 0:
		return result(report.Skip, "-", "cannot list "+strings.Join(forbidden, ", ")+" (forbidden), so the peak is incomplete; the partial peak fits "+strings.Join(fits, "; ")+tail, grant(forbidden))
	}
	return result(report.Pass, layerConfig, "peak fits "+strings.Join(fits, "; ")+tail, "")
}

// containers reads "1 container" or "3 containers".
func containers(n int) string {
	if n == 1 {
		return "1 container"
	}
	return fmt.Sprintf("%d containers", n)
}
