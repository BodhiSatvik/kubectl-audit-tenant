package report

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
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
	pr := policyReport{
		APIVersion: "wgpolicyk8s.io/v1alpha2",
		Kind:       "PolicyReport",
		Metadata: prMetadata{
			Name:      reportName(id.Username),
			Namespace: r.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "audit-tenant"},
			Annotations: map[string]string{
				"audit-tenant.dev/identity":     id.Username,
				"audit-tenant.dev/groups":       strings.Join(id.Groups, ","),
				"audit-tenant.dev/verified":     strconv.FormatBool(id.Verified),
				"audit-tenant.dev/tool-version": r.Tool.Version,
			},
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

// reportName is audit-tenant-<identity slug>, so a re-run replaces the report of its own identity
// and reports of other identities stay (D27). The slug is the identity lowercased with every
// character outside a-z, 0-9 and - turned into -; one longer than 54 characters is cut to 54 plus
// - and 8 hex characters of the SHA-256 of the full identity. A name ends with a letter or digit.
func reportName(identity string) string {
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(identity))
	if len(slug) > 54 {
		sum := sha256.Sum256([]byte(identity))
		slug = slug[:54] + "-" + hex.EncodeToString(sum[:4])
	}
	return strings.TrimRight("audit-tenant-"+slug, "-")
}
