package checks

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at07 reads the namespace's LimitRanges from the run's cache. It passes when one item of type
// Container sets default and defaultRequest for both cpu and memory, so a container without sizes
// gets them. The API server fills default and defaultRequest from max when they are unset, so a
// range with only max passes too: its containers do get sizes.
func at07(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	ranges, err := c.LimitRanges()
	if err != nil {
		return listFailed(ctx, c, spec, err, "limitranges")
	}
	var names, gaps []string
	for _, lr := range byName(ranges) {
		names = append(names, lr.Name)
		for _, item := range lr.Spec.Limits {
			if item.Type != corev1.LimitTypeContainer {
				continue
			}
			var missing []string
			for _, field := range []struct {
				name string
				list corev1.ResourceList
			}{{"default", item.Default}, {"defaultRequest", item.DefaultRequest}} {
				for _, r := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
					if _, ok := field.list[r]; !ok {
						missing = append(missing, field.name+"."+string(r))
					}
				}
			}
			if len(missing) == 0 {
				return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "LimitRange "+lr.Name+" gives containers default and defaultRequest for cpu and memory", "")}
			}
			gaps = append(gaps, lr.Name+" lacks "+strings.Join(missing, ", "))
		}
	}
	var reason string
	switch {
	case len(ranges) == 0:
		reason = "no LimitRange in " + c.Namespace
	case len(gaps) == 0:
		reason = "no LimitRange has a type: Container item (" + strings.Join(names, ", ") + ")"
	default:
		reason = "no Container item sets default and defaultRequest for cpu and memory: " + strings.Join(gaps, "; ")
	}
	return []report.Result{spec.result(spec.Results[0], report.Fail, layerConfig, reason,
		"add a LimitRange with a type: Container item that sets default and defaultRequest for cpu and memory, so containers without sizes get them")}
}
