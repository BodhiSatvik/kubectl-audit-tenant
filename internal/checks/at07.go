package checks

import (
	"context"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at07 reads the namespace's LimitRanges from the run's cache. It passes when their Container
// items together set default and defaultRequest for both cpu and memory, so a container without
// sizes gets them: LimitRanger fills each missing value from every LimitRange (D112). A range
// that sets all four alone is named alone. The API server fills default and defaultRequest from
// max when they are unset, so a range with only max passes too: its containers do get sizes.
func at07(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	ranges, err := c.LimitRanges()
	if err != nil {
		return listFailed(ctx, c, spec, err, "limitranges")
	}
	var names, supplied []string
	containerItems := false
	covered := map[string]bool{} // each default is credited to the first LimitRange by name that sets it
	for _, lr := range byName(ranges) {
		names = append(names, lr.Name)
		var all, fresh []string // the defaults this range sets; those no earlier range set
		for _, d := range containerDefaults {
			if slices.ContainsFunc(lr.Spec.Limits, func(item corev1.LimitRangeItem) bool { return setsDefault(item, d) }) {
				all = append(all, d)
				if !covered[d] {
					covered[d] = true
					fresh = append(fresh, d)
				}
			}
		}
		containerItems = containerItems || slices.ContainsFunc(lr.Spec.Limits, func(item corev1.LimitRangeItem) bool { return item.Type == corev1.LimitTypeContainer })
		if len(all) == len(containerDefaults) {
			return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "LimitRange "+lr.Name+" gives containers default and defaultRequest for cpu and memory", "")}
		}
		if len(fresh) > 0 {
			supplied = append(supplied, lr.Name+" sets "+andList(fresh))
		}
	}
	var missing []string
	for _, d := range containerDefaults {
		if !covered[d] {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "LimitRanges give containers default and defaultRequest for cpu and memory together: "+strings.Join(supplied, "; "), "")}
	}
	var reason string
	switch {
	case len(ranges) == 0:
		reason = "no LimitRange in " + c.Namespace
	case !containerItems:
		reason = "no LimitRange has a type: Container item (" + strings.Join(names, ", ") + ")"
	default:
		reason = "no LimitRange sets " + strings.Join(missing, ", ")
		if len(supplied) > 0 {
			reason += "; " + strings.Join(supplied, "; ")
		}
	}
	return []report.Result{spec.result(spec.Results[0], report.Fail, layerConfig, reason,
		"add a LimitRange with a type: Container item that sets default and defaultRequest for cpu and memory, so containers without sizes get them")}
}

// containerDefaults are the four values AT-07 asks of Container items, in reason order.
var containerDefaults = []string{"default.cpu", "default.memory", "defaultRequest.cpu", "defaultRequest.memory"}

// setsDefault reports whether item is a Container item that sets d, such as "defaultRequest.cpu".
func setsDefault(item corev1.LimitRangeItem, d string) bool {
	field, r, _ := strings.Cut(d, ".")
	list := item.Default
	if field == "defaultRequest" {
		list = item.DefaultRequest
	}
	_, ok := list[corev1.ResourceName(r)]
	return item.Type == corev1.LimitTypeContainer && ok
}
