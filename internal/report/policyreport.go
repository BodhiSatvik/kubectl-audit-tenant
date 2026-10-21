package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// The wgpolicyk8s.io/v1alpha2 fields -o policyreport writes (ADR 0008); scope is a Namespace reference.
type (
	policyReport struct {
		APIVersion string            `json:"apiVersion"`
		Kind       string            `json:"kind"`
		Metadata   prMetadata        `json:"metadata"`
		Scope      map[string]string `json:"scope"`
		Summary    map[string]int    `json:"summary"`
		Results    []prResult        `json:"results"`
	}
	prMetadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	}
	prResult struct {
		Policy     string            `json:"policy"`
		Rule       string            `json:"rule,omitempty"`
		Result     string            `json:"result"`
		Severity   Severity          `json:"severity"`
		Category   string            `json:"category"`
		Source     string            `json:"source"`
		Message    string            `json:"message"`
		Timestamp  prTimestamp       `json:"timestamp"`
		Properties map[string]string `json:"properties"`
	}
	prTimestamp struct {
		Seconds int64 `json:"seconds"`
		Nanos   int   `json:"nanos"` // 0 to 999999999; the CRD's int32 holds it
	}
)

// PolicyReport writes the report as a PolicyReport in YAML (DESIGN.md section 6) for the user
// to apply; the tool never writes it to the cluster.
func PolicyReport(w io.Writer, r Report) error {
	id := r.Identity
	groups, err := json.Marshal(append([]string{}, id.Groups...)) // a JSON array, [] when none: a group name may hold a comma (an LDAP DN)
	if err != nil {
		return err
	}
	annotations := map[string]string{
		"audit-tenant.dev/identity":        id.Username,
		"audit-tenant.dev/groups":          string(groups),
		"audit-tenant.dev/verified":        strconv.FormatBool(id.Verified),
		"audit-tenant.dev/tool-version":    r.Tool.Version,
		"audit-tenant.dev/other-namespace": r.OtherNamespace,
	}
	if len(r.Warnings) > 0 { // the run's caveats travel with the report (D146)
		warnings, err := json.Marshal(r.Warnings)
		if err != nil {
			return err
		}
		annotations["audit-tenant.dev/warnings"] = string(warnings)
	}
	pr := policyReport{
		APIVersion: "wgpolicyk8s.io/v1alpha2",
		Kind:       "PolicyReport",
		Metadata: prMetadata{
			Name:        reportName(id),
			Namespace:   r.Namespace,
			Labels:      map[string]string{"app.kubernetes.io/managed-by": "audit-tenant"},
			Annotations: annotations,
		},
		Scope:   map[string]string{"apiVersion": "v1", "kind": "Namespace", "name": r.Namespace},
		Summary: map[string]int{"pass": r.Summary.Pass, "fail": r.Summary.Fail, "warn": 0, "error": r.Summary.Error, "skip": r.Summary.Skip},
	}
	for _, res := range r.Results {
		props := map[string]string{"layer": res.Layer, "name": res.Name, "identity": id.Username}
		if res.Hint != "" {
			props["hint"] = res.Hint
		}
		if res.Note != "" {
			props["note"] = res.Note
		}
		rule := res.MTB
		if rule == "-" {
			rule = "" // no MTB ID: the rule is omitted
		}
		pr.Results = append(pr.Results, prResult{
			Policy: res.ID, Rule: rule, Result: strings.ToLower(string(res.Verdict)), Severity: res.Severity,
			Category: "multi-tenancy", Source: "audit-tenant", Message: res.Reason,
			Timestamp:  prTimestamp{r.Timestamp.Unix(), r.Timestamp.Nanosecond()},
			Properties: props,
		})
	}
	out, err := yaml.Marshal(pr)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// reportName is audit-tenant-<slug>-<hash>, so a re-run replaces the report of its own identity and
// reports of other identities stay (D27). The slug is the username lowercased with every character
// outside a-z and 0-9 turned into -, cut to 54 characters; the hash is 32 hex characters of the
// SHA-256 of the requested identity (requestedIdentity), so case, replaced characters and group sets
// (the --as audit-probe --as-group <group> pattern) never share a name (D144, D147).
func reportName(id Identity) string {
	key, _ := json.Marshal(requestedIdentity(id)) // a []string always marshals
	sum := sha256.Sum256(key)
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(id.Username))
	slug = strings.Trim(slug[:min(len(slug), 54)], "-")
	return strings.Join(slices.DeleteFunc([]string{"audit-tenant", slug, hex.EncodeToString(sum[:16])}, func(s string) bool { return s == "" }), "-")
}

// requestedIdentity is the username and, when impersonating, the sorted groups without the ones the
// API server adds itself: system:authenticated, and a service account's own two groups when they are
// its only groups (the server adds them only when no group is given). So a run whose
// SelfSubjectReview failed names the same report as a verified one (D147), and a service account with
// --as-group keeps a report apart from one with its own groups too. In self mode the username alone
// names it: an identity provider's groups belong to the user. An impersonated user (not a service
// account) is keyed apart from that user's own login, which has the identity provider's groups (D151).
func requestedIdentity(id Identity) []string {
	if id.Mode != ModeImpersonation {
		return []string{id.Username}
	}
	groups := slices.DeleteFunc(slices.Clone(id.Groups), func(g string) bool { return g == "system:authenticated" })
	slices.Sort(groups)
	rest, sa := strings.CutPrefix(id.Username, "system:serviceaccount:")
	if !sa {
		return append([]string{ModeImpersonation, id.Username}, groups...)
	}
	ns, _, _ := strings.Cut(rest, ":")
	if slices.Equal(groups, []string{"system:serviceaccounts", "system:serviceaccounts:" + ns}) {
		groups = nil // the API server's own pair: the same identity as the service account's token
	}
	return append([]string{id.Username}, groups...)
}
