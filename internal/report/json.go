package report

import (
	"encoding/json"
	"io"
)

// JSON writes the report as -o json (DESIGN.md section 6, docs/report.schema.json): the full
// server text, JSON-escaped, never color.
func JSON(w io.Writer, r Report) error {
	if r.Identity.Groups == nil {
		r.Identity.Groups = []string{} // the schema wants an array when no group is known
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep "--as <admin>" readable
	return enc.Encode(struct {
		APIVersion string `json:"apiVersion"` // one-way after release
		Report
	}{"audit-tenant.dev/v1alpha1", r})
}
