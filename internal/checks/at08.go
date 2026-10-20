package checks

import (
	"context"
	"slices"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// at08 lists the namespace's NetworkPolicies. It passes when every pod is denied ingress by a
// policy with an empty podSelector, Ingress in policyTypes and no ingress rules, and denied
// egress likewise, by the same policy or another. Other policies do not change the verdict; the
// PASS note names those that select every pod and allow traffic, since allows add up (D110).
func at08(ctx context.Context, c *cluster.Client, spec Spec, _ func(report.Result)) []report.Result {
	list, err := c.Clientset.NetworkingV1().NetworkPolicies(c.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return listFailed(ctx, c, spec, err, "networkpolicies")
	}
	policies := byName(list.Items)
	in, inAllows := defaultDeny(policies, networkingv1.PolicyTypeIngress)
	out, outAllows := defaultDeny(policies, networkingv1.PolicyTypeEgress)
	r := spec.Results[0]
	if in != "" && out != "" {
		reason := "default deny for ingress by " + in + " and egress by " + out
		if in == out {
			reason = "default deny for ingress and egress by " + in
		}
		pass := spec.result(r, report.Pass, layerConfig, reason, "")
		// Allows add up, so a policy that selects every pod and allows traffic weakens the deny (D110).
		pass.Note = strings.Join(slices.Concat(inAllows, outAllows, []string{"not tested: whether the cluster's network plugin enforces NetworkPolicy"}), "; ")
		return []report.Result{pass}
	}
	var missing []string
	if in == "" {
		missing = append(missing, "ingress")
	}
	if out == "" {
		missing = append(missing, "egress")
	}
	var allows []string // only the directions with no deny explain the FAIL
	if in == "" {
		allows = inAllows
	}
	if out == "" {
		allows = slices.Concat(allows, outAllows)
	}
	reason := "no default deny for " + strings.Join(missing, " or ")
	if len(allows) > 0 {
		reason += ": " + strings.Join(allows, "; ")
	} else if len(policies) == 0 {
		reason = "no NetworkPolicy in " + c.Namespace
	}
	return []report.Result{spec.result(r, report.Fail, layerConfig, reason,
		"add one NetworkPolicy per direction with podSelector: {} and no rules (policyTypes [Ingress], and policyTypes [Egress]); put allow rules, such as DNS egress, in separate policies")}
}

// defaultDeny returns the first policy that denies every pod all traffic of type t: an empty
// podSelector, t in policyTypes and no rules of type t. allows names the policies that select
// every pod and list t but carry rules of it, which allow that traffic.
func defaultDeny(policies []networkingv1.NetworkPolicy, t networkingv1.PolicyType) (name string, allows []string) {
	for _, p := range policies {
		sel := p.Spec.PodSelector
		if len(sel.MatchLabels) > 0 || len(sel.MatchExpressions) > 0 || !slices.Contains(p.Spec.PolicyTypes, t) {
			continue
		}
		rules := len(p.Spec.Ingress)
		if t == networkingv1.PolicyTypeEgress {
			rules = len(p.Spec.Egress)
		}
		if rules == 0 && name == "" {
			name = p.Name
		} else if rules > 0 {
			allows = append(allows, p.Name+" selects every pod but its "+strings.ToLower(string(t))+" rules allow traffic")
		}
	}
	return name, allows
}
