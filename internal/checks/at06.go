package checks

import (
	"context"
	"slices"
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
	var scopedNames []string
	var unscoped []corev1.ResourceQuota
	var spelled []map[corev1.ResourceName]string // per unscoped quota: each of the five keys it sets, as it spells it
	for _, q := range byName(quotas) {
		if len(q.Spec.Scopes) > 0 || q.Spec.ScopeSelector != nil {
			scopedNames = append(scopedNames, q.Name)
			continue
		}
		keys := map[corev1.ResourceName]string{}
		for _, key := range headroom.QuotaKeys(q) {
			if k, _ := headroom.Canonical(key); keys[k] == "" {
				keys[k] = string(key)
			}
		}
		if len(keys) == len(headroom.Keys) {
			var set []string
			for _, k := range headroom.Keys {
				set = append(set, keys[k])
			}
			return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "ResourceQuota "+q.Name+" has no scopes and sets "+andList(set), "")}
		}
		unscoped, spelled = append(unscoped, q), append(spelled, keys)
	}
	// Each key comes from the first unscoped quota by name that sets it.
	sets := make([][]string, len(unscoped))
	var missing []string
	for _, k := range headroom.Keys {
		i := slices.IndexFunc(spelled, func(keys map[corev1.ResourceName]string) bool { return keys[k] != "" })
		if i < 0 {
			missing = append(missing, string(k))
		} else {
			sets[i] = append(sets[i], spelled[i][k])
		}
	}
	var supplied []string
	for i, q := range unscoped {
		if len(sets[i]) > 0 {
			supplied = append(supplied, q.Name+" sets "+andList(sets[i]))
		}
	}
	if len(missing) == 0 {
		return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "unscoped ResourceQuotas set all five keys together: "+strings.Join(supplied, "; "), "")}
	}
	var reason string
	switch {
	case len(quotas) == 0:
		reason = "no ResourceQuota in " + c.Namespace
	case len(unscoped) == 0:
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
		"add a ResourceQuota with no scopes whose hard sets requests.cpu, requests.memory, limits.cpu, limits.memory and pods")}
}

// andList joins items as "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
