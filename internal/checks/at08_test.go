package checks

import (
	"net/http"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

func at08Spec() Spec { return Registry[7] }

const netpolPath = "GET /apis/networking.k8s.io/v1/namespaces/tenant-a/networkpolicies"

var (
	ingress = networkingv1.PolicyTypeIngress
	egress  = networkingv1.PolicyTypeEgress
	dnsPort = intstr.FromInt32(53)
	dns     = networkingv1.NetworkPolicyEgressRule{Ports: []networkingv1.NetworkPolicyPort{{Port: &dnsPort}}}
	fromAPI = networkingv1.NetworkPolicyIngressRule{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}}}}
)

// netpol is a policy that selects every pod, with the policy types and rules given.
func netpol(name string, types []networkingv1.PolicyType, in []networkingv1.NetworkPolicyIngressRule, out []networkingv1.NetworkPolicyEgressRule) networkingv1.NetworkPolicy {
	return networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "tenant-a"},
		Spec: networkingv1.NetworkPolicySpec{PolicyTypes: types, Ingress: in, Egress: out}}
}

// selecting is p narrowed to the pods with app=api.
func selecting(p networkingv1.NetworkPolicy) networkingv1.NetworkPolicy {
	p.Spec.PodSelector = metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
	return p
}

func TestAT08(t *testing.T) {
	spec := at08Spec()
	const (
		failHint = "add one NetworkPolicy per direction with podSelector: {} and no rules (policyTypes [Ingress], and policyTypes [Egress]); put allow rules, such as DNS egress, in separate policies"
		note     = "not tested: whether the cluster's network plugin enforces NetworkPolicy"
	)
	noted := func(r report.Result) report.Result { r.Note = note; return r }
	withNote := func(r report.Result, n string) report.Result { r.Note = n; return r }
	denyIn := netpol("default-deny-ingress", []networkingv1.PolicyType{ingress}, nil, nil)
	denyOut := netpol("default-deny-egress", []networkingv1.PolicyType{egress}, nil, nil)
	tests := []struct {
		name   string
		answer http.HandlerFunc
		want   report.Result
	}{
		{"two policies", listOf(denyIn, denyOut),
			noted(configResult(spec, report.Pass, "config", "default deny for ingress by default-deny-ingress and egress by default-deny-egress", ""))},
		{"one combined policy", listOf(netpol("default-deny-all", []networkingv1.PolicyType{ingress, egress}, nil, nil)),
			noted(configResult(spec, report.Pass, "config", "default deny for ingress and egress by default-deny-all", ""))},
		{"allow policies next to the denies do not change the verdict; the note names those that select every pod (D110)",
			listOf(netpol("allow-dns", []networkingv1.PolicyType{egress}, nil, []networkingv1.NetworkPolicyEgressRule{dns}), denyOut, denyIn, selecting(netpol("api-ingress", []networkingv1.PolicyType{ingress}, []networkingv1.NetworkPolicyIngressRule{fromAPI}, nil))),
			withNote(configResult(spec, report.Pass, "config", "default deny for ingress by default-deny-ingress and egress by default-deny-egress", ""), "allow-dns selects every pod but its egress rules allow traffic; "+note)},
		{"an allow-all policy next to the denies is named in the note (D110)",
			listOf(denyIn, denyOut, netpol("allow-all", []networkingv1.PolicyType{ingress, egress}, []networkingv1.NetworkPolicyIngressRule{{}}, []networkingv1.NetworkPolicyEgressRule{{}})),
			withNote(configResult(spec, report.Pass, "config", "default deny for ingress by default-deny-ingress and egress by default-deny-egress", ""),
				"allow-all selects every pod but its ingress rules allow traffic; allow-all selects every pod but its egress rules allow traffic; "+note)},
		{"a deny whose selector uses matchExpressions selects some pods only", listOf(func() networkingv1.NetworkPolicy {
			p := netpol("deny-labelled", []networkingv1.PolicyType{ingress, egress}, nil, nil)
			p.Spec.PodSelector = metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: metav1.LabelSelectorOpExists}}}
			return p
		}()),
			configResult(spec, report.Fail, "config", "no default deny for ingress or egress", failHint)},
		{"inline DNS egress", listOf(netpol("default-deny", []networkingv1.PolicyType{ingress, egress}, nil, []networkingv1.NetworkPolicyEgressRule{dns})),
			configResult(spec, report.Fail, "config", "no default deny for egress: default-deny selects every pod but its egress rules allow traffic", failHint)},
		{"no policy", listOf[networkingv1.NetworkPolicy](),
			configResult(spec, report.Fail, "config", "no NetworkPolicy in tenant-a", failHint)},
		{"ingress only", listOf(denyIn),
			configResult(spec, report.Fail, "config", "no default deny for egress", failHint)},
		// Value: protects=a FAIL reason names only allows of the direction with no deny (D110); fails_when=allows of both directions are joined again; why_new=no FAIL row has a deny plus an allow in one direction; seam=none; no_claim=PASS note content
		{"an ingress allow next to the ingress deny does not explain the missing egress deny (D110)",
			listOf(denyIn, netpol("allow-web", []networkingv1.PolicyType{ingress}, []networkingv1.NetworkPolicyIngressRule{fromAPI}, nil)),
			configResult(spec, report.Fail, "config", "no default deny for egress", failHint)},
		{"a deny that selects some pods only", listOf(selecting(denyIn), selecting(denyOut)),
			configResult(spec, report.Fail, "config", "no default deny for ingress or egress", failHint)},
		{"allow rules in both directions", listOf(netpol("allow-some", []networkingv1.PolicyType{ingress, egress}, []networkingv1.NetworkPolicyIngressRule{fromAPI}, []networkingv1.NetworkPolicyEgressRule{dns})),
			configResult(spec, report.Fail, "config", "no default deny for ingress or egress: allow-some selects every pod but its ingress rules allow traffic; allow-some selects every pod but its egress rules allow traffic", failHint)},
		{"forbidden", forbidden("networkpolicies"),
			configResult(spec, report.Skip, "-", "cannot list networkpolicies in tenant-a (forbidden)", "grant `list` on networkpolicies in tenant-a to this identity, or run with --as <admin>")},
		{"a server error", testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcdserver: request timed out"),
			configResult(spec, report.Error, "-", "could not list networkpolicies: etcdserver: request timed out", hintRequestFailed)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := at08(t.Context(), configClient(t, map[string]http.HandlerFunc{netpolPath: tt.answer}), spec, ignore)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("at08() = %+v,\nwant %+v", got, tt.want)
			}
		})
	}
}
