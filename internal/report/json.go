package report

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
)

// JSON writes the report as -o json (DESIGN.md section 6, docs/report.schema.json): the full
// server text with its control characters marked (D170), never color.
func JSON(w io.Writer, r Report) error {
	r = forMachines(r)
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

// adminHint is the caveat of a self run whose identity looks like a cluster admin.
const adminHint = "this identity looks like a cluster admin; add --as to test a tenant"

// forMachines is r as -o json and -o policyreport print it: the admin hint joins the warnings
// (D173), and every string a consumer may display has its control characters marked (D170).
func forMachines(r Report) Report {
	if r.AdminHint {
		r.Warnings = append(slices.Clip(r.Warnings), adminHint)
	}
	r.Tool.Version = mark(r.Tool.Version)
	r.Cluster = Cluster{Context: mark(r.Cluster.Context), Server: mark(r.Cluster.Server), Version: mark(r.Cluster.Version)}
	r.Identity.Username, r.Identity.Groups = mark(r.Identity.Username), markAll(r.Identity.Groups)
	r.Warnings = markAll(r.Warnings)
	r.Results = slices.Clone(r.Results)
	for i, res := range r.Results {
		r.Results[i].Layer, r.Results[i].Reason, r.Results[i].Hint, r.Results[i].Note = mark(res.Layer), mark(res.Reason), mark(res.Hint), mark(res.Note)
	}
	return r
}

// mark shows each control or format character except newline and tab as <U+XXXX>, so server
// text cannot hide or reorder what a terminal or dashboard displays (D170).
func mark(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c != '\n' && c != '\t' && (unicode.IsControl(c) || unicode.Is(unicode.Cf, c)) {
			fmt.Fprintf(&b, "<U+%04X>", c)
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func markAll(ss []string) []string {
	ss = slices.Clone(ss) // nil stays nil
	for i, s := range ss {
		ss[i] = mark(s)
	}
	return ss
}
