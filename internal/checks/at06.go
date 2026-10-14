package checks

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// quotaKeys are the five keys AT-06 requires, each with the alias the API also accepts.
var quotaKeys = []struct{ key, alias corev1.ResourceName }{
	{corev1.ResourceRequestsCPU, corev1.ResourceCPU},
	{corev1.ResourceRequestsMemory, corev1.ResourceMemory},
	{corev1.ResourceLimitsCPU, ""},
	{corev1.ResourceLimitsMemory, ""},
	{corev1.ResourcePods, "count/pods"},
}

// at06 reads the namespace's ResourceQuotas from the run's cache. It passes when one quota with
// no scopes and no scopeSelector sets all five keys; a scoped quota covers only some pods.
func at06(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	quotas, err := c.Quotas()
	if err != nil {
		return listFailed(ctx, c, spec, err, "resourcequotas")
	}
	var scopedNames, gaps []string
	for _, q := range byName(quotas) {
		if len(q.Spec.Scopes) > 0 || q.Spec.ScopeSelector != nil {
			scopedNames = append(scopedNames, q.Name)
			continue
		}
		var set, missing []string
		for _, k := range quotaKeys {
			if _, ok := q.Spec.Hard[k.key]; ok {
				set = append(set, string(k.key))
			} else if _, ok := q.Spec.Hard[k.alias]; ok { // an empty alias is never a key
				set = append(set, string(k.alias))
			} else {
				missing = append(missing, string(k.key))
			}
		}
		if len(missing) == 0 {
			return []report.Result{spec.result(spec.Results[0], report.Pass, layerConfig, "ResourceQuota "+q.Name+" has no scopes and sets "+andList(set), "")}
		}
		gaps = append(gaps, q.Name+" lacks "+strings.Join(missing, ", "))
	}
	var reason string
	switch {
	case len(quotas) == 0:
		reason = "no ResourceQuota in " + c.Namespace
	case len(gaps) == 0:
		reason = "no ResourceQuota without scopes in " + c.Namespace + "; a scoped quota covers only some pods"
	default:
		reason = "no ResourceQuota without scopes sets all five keys: " + strings.Join(gaps, "; ")
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
