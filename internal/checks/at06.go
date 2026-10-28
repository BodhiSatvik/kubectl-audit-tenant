package checks

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/headroom"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at06 reads the namespace's ResourceQuotas from the run's cache. It passes when the quotas with
// no scopes and no scopeSelector together set all five keys: the API server enforces each quota,
// so two that split the keys bound the namespace as one does (D107). A scoped quota covers only
// some pods. A quota that sets all five alone is named alone.
func at06(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	quotas, err := c.Quotas()
	if err != nil {
		return listFailed(ctx, c, spec, err, "resourcequotas")
	}
	var scopedNames, supplied []string
	unscoped := 0
	covered := map[corev1.ResourceName]bool{} // each key is credited to the first unscoped quota by name that sets it
	for _, q := range byName(quotas) {
		if scoped(q) {
			scopedNames = append(scopedNames, q.Name)
			continue
		}
		unscoped++
		seen := map[corev1.ResourceName]bool{}
		var all, fresh []string // this quota's spelling of each key it sets; the keys no earlier quota set
		for _, key := range headroom.QuotaKeys(q) {
			if k := headroom.Canonical(key); !seen[k] {
				seen[k] = true
				all = append(all, string(key))
				if !covered[k] {
					covered[k] = true
					fresh = append(fresh, string(key))
				}
			}
		}
		if len(all) == len(headroom.Keys) {
			return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "ResourceQuota "+q.Name+" has no scopes and sets "+andList(all), "")}
		}
		if len(fresh) > 0 {
			supplied = append(supplied, q.Name+" sets "+andList(fresh))
		}
	}
	var missing []string
	for _, k := range headroom.Keys {
		if !covered[k] {
			missing = append(missing, string(k))
		}
	}
	if len(missing) == 0 {
		return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "unscoped ResourceQuotas set all five keys together: "+strings.Join(supplied, "; "), "")}
	}
	var reason string
	switch {
	case len(quotas) == 0:
		reason = "no ResourceQuota in " + c.Namespace
	case unscoped == 0:
		reason = "no ResourceQuota without scopes in " + c.Namespace + "; a scoped quota covers only some pods"
	default:
		reason = "no ResourceQuota without scopes sets " + strings.Join(missing, ", ")
		if len(supplied) > 0 {
			reason += "; " + strings.Join(supplied, "; ")
		}
	}
	if len(scopedNames) > 0 {
		reason += " (scoped: " + strings.Join(scopedNames, ", ") + ")"
	}
	return []report.Result{spec.result(spec.Results[0], report.Fail, layerConfig, reason,
		"add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods; first make sure a LimitRange gives defaults (AT-07): with this quota, the API server refuses new pods that set no cpu or memory")} // D197
}

// andList joins items as "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
