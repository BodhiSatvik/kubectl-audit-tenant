// Package checks holds the check registry, check selection and the ten checks.
package checks

import (
	"context"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// ResultSpec names one result a check produces. Most checks produce one; AT-04 produces five.
type ResultSpec struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	MTB  string `json:"mtb"` // "-" when none
}

// RunFunc runs one check against the cluster and returns one Result per entry of spec.Results,
// in order. When ctx ends it returns the results it has; the runner fills in the rest. A check
// passes each FAIL to prove the moment it is proven, so the runner can still report it when the
// check leaves it out, never returns or crashes (D61, D84); a later call for the same result ID
// replaces it, and a result the check returns wins over it.
type RunFunc func(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result)) []report.Result

// Spec is the static description of a check, as list-checks prints it, plus its Run function.
type Spec struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Kind     report.Kind     `json:"kind"`
	Severity report.Severity `json:"severity"`
	Method   string          `json:"method"`
	Results  []ResultSpec    `json:"results"`
	Run      RunFunc         `json:"-"`

	// earlier holds the results of the checks that ran before this one in the run; the runner
	// sets it. AT-09 reads AT-04's (ADR 0007).
	earlier []report.Result
}

// result builds one Result of this check.
func (s Spec) result(r ResultSpec, verdict report.Verdict, layer, reason, hint string) report.Result {
	return report.Result{ID: r.ID, Name: r.Name, Kind: s.Kind, MTB: r.MTB, Severity: s.Severity,
		Verdict: verdict, Layer: layer, Reason: reason, Hint: hint}
}

// all gives every selected result of this check the same verdict.
func (s Spec) all(verdict report.Verdict, layer, reason, hint string) []report.Result {
	out := make([]report.Result, 0, len(s.Results))
	for _, r := range s.Results {
		out = append(out, s.result(r, verdict, layer, reason, hint))
	}
	return out
}

func one(id, name, mtb string) []ResultSpec { return []ResultSpec{{ID: id, Name: name, MTB: mtb}} }

// Registry lists the ten checks in run order (DESIGN.md section 5). The MTB IDs match kubectl-mtb's
// benchmark configs (kubernetes-sigs/multi-tenancy 44dad58); upstream gives hostPath and NodePort one ID.
var Registry = []Spec{
	{ID: "AT-01", Name: "No cluster-scoped access", Kind: report.Behavioral, Severity: report.High,
		Method:  "SSAR with no namespace: list nodes, create namespaces, create clusterrolebindings, create persistentvolumes; before a PASS, a control: create selfsubjectaccessreviews must be allowed",
		Results: one("AT-01", "No cluster-scoped access", "MTB-PL1-CC-CPI-1"), Run: at01},
	{ID: "AT-02", Name: "No cross-namespace secret access", Kind: report.Behavioral, Severity: report.High,
		Method:  "SSAR: get, list and watch secrets in --other-namespace",
		Results: one("AT-02", "No cross-namespace secret access", "MTB-PL1-CC-TI-2"), Run: at02},
	{ID: "AT-03", Name: "No privilege escalation", Kind: report.Behavioral, Severity: report.High,
		Method:  "SSAR with no namespace: escalate and bind on clusterroles; impersonate users, groups, serviceaccounts; * on * in every API group",
		Results: one("AT-03", "No privilege escalation", "MTB-PL2-CC-TI-1"), Run: at03},
	{ID: "AT-04", Name: "Dangerous pod settings blocked", Kind: report.Behavioral, Severity: report.High,
		Method:  "dry-run pods after a compliant control pod: privileged (two forms), hostPath, hostPID, hostNetwork, allowPrivilegeEscalation",
		Results: at04Results, Run: at04},
	{ID: "AT-05", Name: "NodePort services blocked", Kind: report.Behavioral, Severity: report.Medium,
		Method:  "dry-run Service of type NodePort after a ClusterIP control Service",
		Results: one("AT-05", "NodePort services blocked", "MTB-PL1-BC-HI-1"), Run: at05},
	{ID: "AT-06", Name: "ResourceQuota present and complete", Kind: report.Config, Severity: report.Medium,
		Method:  "LIST resourcequotas: the unscoped quotas together set requests.cpu, requests.memory, limits.cpu, limits.memory, pods",
		Results: one("AT-06", "ResourceQuota present and complete", "MTB-PL1-CC-FNS-1"), Run: at06},
	{ID: "AT-07", Name: "LimitRange with defaults", Kind: report.Config, Severity: report.Low,
		Method:  "LIST limitranges: the Container items together set default and defaultRequest for cpu and memory",
		Results: one("AT-07", "LimitRange with defaults", "-"), Run: at07},
	{ID: "AT-08", Name: "Default-deny NetworkPolicy", Kind: report.Config, Severity: report.Medium,
		Method:  "LIST networkpolicies: an empty-selector deny for Ingress and one for Egress",
		Results: one("AT-08", "Default-deny NetworkPolicy", "-"), Run: at08},
	{ID: "AT-09", Name: "Pod Security Admission enforced", Kind: report.Config, Severity: report.High,
		Method:  "GET namespace labels: pod-security.kubernetes.io/enforce is baseline or restricted",
		Results: one("AT-09", "Pod Security Admission enforced", "-"), Run: at09},
	{ID: "AT-10", Name: "Quota covers rollout peak", Kind: report.Computed, Severity: report.Low,
		Method:  "LIST deployments, statefulsets, horizontalpodautoscalers, resourcequotas, limitranges; sum the rollout peak",
		Results: one("AT-10", "Quota covers rollout peak", "-"), Run: at10},
}

// at04Results are AT-04's sub-results, one per probe; AT-09 counts them (D110).
var at04Results = []ResultSpec{
	{ID: "AT-04.1", Name: "Privileged container blocked", MTB: "MTB-PL1-BC-CPI-5"},
	{ID: "AT-04.2", Name: "hostPath volume blocked", MTB: "MTB-PL1-BC-HI-1"},
	{ID: "AT-04.3", Name: "hostPID blocked", MTB: "MTB-PL1-BC-HI-4"},
	{ID: "AT-04.4", Name: "hostNetwork blocked", MTB: "MTB-PL1-BC-HI-3"},
	{ID: "AT-04.5", Name: "Privilege escalation blocked", MTB: "MTB-PL1-BC-CPI-6"},
}

// ResultIDs flattens the result IDs of specs in order.
func ResultIDs(specs []Spec) []string {
	var ids []string
	for _, s := range specs {
		for _, r := range s.Results {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
