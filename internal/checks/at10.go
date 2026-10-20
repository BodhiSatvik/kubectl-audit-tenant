package checks

import (
	"context"
	"fmt"
	"maps"
	"slices"
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

// at10Estimate starts every AT-10 PASS, FAIL and SKIP reason: the peak is an estimate, not a
// bound (ADR 0006); an ERROR reports a failed request instead. It is short so the checklist shows
// the numbers; the note says what the estimate leaves out (D106).
const at10Estimate = "estimate: "

// at10 sums the rollout peak of the namespace's Deployments and StatefulSets (internal/headroom)
// and compares it with every unscoped ResourceQuota, edge cases in the DESIGN.md section 5 order:
// no unscoped quota is FAIL; no workloads is PASS; a quota that refuses a workload's pods (a
// tracked value left unset) is FAIL; a list this identity may not read (or the LimitRanges while
// a tracked value is unset) is SKIP, unless the partial sum is already over a hard value, which
// is FAIL; not while the HPAs are unread, since an HPA can lower replicas (D110).
func at10(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	ns := c.Namespace
	note := "the peak counts active pods of Deployments and StatefulSets during a rollout, at their template sizes; terminating pods can add to usage; not counted: other workloads (DaemonSets, Jobs, CronJobs, ReplicaSets, ReplicationControllers, bare Pods, Argo Rollouts) and sizes set at admission (VPA)" // D128
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
		if scoped(q) {
			scopedNames = append(scopedNames, q.Name)
		} else if len(headroom.QuotaKeys(q)) > 0 { // a quota on none of the five keys bounds nothing AT-10 counts
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
	defaultedFrom := map[string]bool{}
	var conflicts []string // workloads that several HPAs target
	for _, w := range ws {
		unsized += w.Pod.Unsized
		defaulted += w.Pod.Defaulted
		for _, name := range w.Pod.DefaultedFrom {
			defaultedFrom[name] = true
		}
		if len(w.ConflictingHPAs) > 0 { // D126
			conflicts = append(conflicts, strings.ToLower(w.Kind)+" "+w.Name+" ("+strings.Join(w.ConflictingHPAs, ", ")+")")
		}
		if w.Pod.RuntimeClass {
			runtimeClass = append(runtimeClass, w.Name)
		}
	}
	// Quota admission refuses a pod that leaves unset a cpu or memory value an unscoped quota
	// tracks ("must specify"), whatever the peak (D111), unless the pod sets a pod-level value
	// (D132). LimitRanger fills such values in either case, so unread LimitRanges SKIP.
	var refused []string
	unsetTracked := false
	for _, q := range unscoped {
		tracked := map[corev1.ResourceName]bool{}
		for _, key := range headroom.QuotaKeys(q) {
			tracked[headroom.Canonical(key)] = true
		}
		for _, w := range ws {
			var keys []string
			for _, k := range w.Pod.Unset {
				if tracked[k] {
					keys = append(keys, string(k))
				}
			}
			if len(keys) > 0 {
				unsetTracked = true
				if !w.Pod.PodLevel {
					refused = append(refused, q.Name+" refuses the pods of "+strings.ToLower(w.Kind)+" "+w.Name+": a container sets no "+strings.Join(keys, ", "))
				}
			}
		}
	}
	if rangesForbidden && unsetTracked { // unread LimitRange defaults may set those values
		forbidden = append(forbidden, "limitranges")
		refused = nil
	}
	if len(conflicts) > 0 {
		note += "; several HPAs target " + firstThree(conflicts) + "; the HPA controller scales none of them (AmbiguousSelector)"
	}
	if len(runtimeClass) > 0 {
		note += "; pod overhead of a RuntimeClass (" + firstThree(runtimeClass) + ")"
	}
	if unsized > 0 {
		note += "; " + containers(unsized) + " left a cpu or memory request or limit unset; counted as 0"
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
			k := headroom.Canonical(key)
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
		names := slices.Sorted(maps.Keys(defaultedFrom)) // only the ranges a container took a value from (D121)
		noun := "LimitRange "
		if len(names) > 1 {
			noun = "LimitRanges "
		}
		tail += "; defaults from " + noun + andList(names) + " for " + containers(defaulted)
	}
	// A partial sum can only grow, so a shortfall is FAIL even with lists missing; not while the
	// HPAs are unread, since an HPA's maxReplicas can be below spec.replicas (D110).
	hpasUnread := slices.Contains(forbidden, "horizontalpodautoscalers")
	proven := short
	if hpasUnread {
		proven = nil
	}
	switch {
	case len(refused) > 0 || len(proven) > 0:
		reason := strings.Join(slices.Concat(refused, proven), "; ") + tail
		if len(forbidden) > 0 {
			reason += "; partial: cannot list " + strings.Join(forbidden, ", ") + " (forbidden)"
		}
		const raise = "raise the quota's hard values by the shortfall, or lower replicas, HPA maxReplicas or maxSurge; the estimate assumes every workload rolls out at once"
		hint := raise
		if len(refused) > 0 {
			hint = "set cpu and memory requests and limits on every container, or give them defaults with a LimitRange (see AT-07); a webhook that sets them only on pods, not in the template, is not seen"
			if len(proven) > 0 {
				hint += "; " + raise
			}
		}
		return result(report.Fail, layerConfig, reason, hint)
	case len(forbidden) > 0:
		partial := "the partial peak fits " + strings.Join(fits, "; ")
		if len(short) > 0 {
			partial = "an HPA can lower replicas, so the partial shortfall is not proven: " + strings.Join(short, "; ")
		}
		return result(report.Skip, "-", "cannot list "+strings.Join(forbidden, ", ")+" (forbidden), so the peak is incomplete; "+partial+tail, grant(forbidden))
	}
	return result(report.Pass, layerConfig, "peak fits "+strings.Join(fits, "; ")+tail, "")
}

// firstThree joins items, naming three and then "and N more": the note is never cut (D114).
func firstThree(items []string) string {
	if more := len(items) - 3; more > 0 {
		return fmt.Sprintf("%s and %d more", strings.Join(items[:3], ", "), more)
	}
	return strings.Join(items, ", ")
}

// containers reads "1 container" or "3 containers".
func containers(n int) string {
	if n == 1 {
		return "1 container"
	}
	return fmt.Sprintf("%d containers", n)
}
