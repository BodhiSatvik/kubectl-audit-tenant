package report

import (
	"fmt"
	"io"
	"strings"
)

// Checklist is the default printer: a header, one line per result, a fix line under every
// non-PASS, a note line under a result that names what it did not test, and a summary
// (DESIGN.md section 6).
type Checklist struct {
	Out   io.Writer
	Color bool // the caller decides with WantColor
	Plain bool // ASCII only: no color, no "·", no "…"
	Width int  // terminal columns; 0 when Out is not a terminal
}

// Print writes the report. Server text is sanitized and capped; a cut reason adds the
// "full reasons" footer.
func (p Checklist) Print(r Report) error {
	dot, ellipsis := " · ", "…"
	if p.Plain {
		dot, ellipsis = " | ", "..."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "namespace %s%s%s%scluster %s\n", Sanitize(r.Namespace), dot, identity(r.Identity), dot, Sanitize(r.Cluster.Context))
	if r.AdminHint {
		b.WriteString("this identity looks like a cluster admin; add --as to test a tenant\n")
	}
	b.WriteString("\n")

	vw, iw, nw := 0, 0, 0 // column widths: verdict, ID, name
	for _, res := range r.Results {
		vw, iw, nw = max(vw, len(res.Verdict)), max(iw, len(res.ID)), max(nw, len(res.Name))
	}
	limit := p.reasonWidth(vw + 2 + iw + 2 + nw + 2)
	cut := false
	for _, res := range r.Results {
		reason, c := clip(Sanitize(res.Reason), limit, ellipsis)
		cut = cut || c
		fmt.Fprintf(&b, "%s  %-*s  %-*s  %s\n", p.verdict(res.Verdict, vw), iw, res.ID, nw, res.Name, reason)
		if res.Verdict != Pass {
			fmt.Fprintf(&b, "%*s  %-*s  %s\n", vw, "", iw, "fix", Sanitize(res.Hint))
		}
		if res.Note != "" { // after the fix line, which always follows a non-PASS directly
			fmt.Fprintf(&b, "%*s  %-*s  %s\n", vw, "", iw, "note", Sanitize(res.Note))
		}
	}
	b.WriteString("\n" + summaryLine(r, dot) + "\n")
	b.WriteString("MTB IDs and layers: -o wide" + dot + "machine output: -o json, -o policyreport\n")
	if cut {
		b.WriteString("full reasons: -o wide or -o json\n")
	}
	_, err := io.WriteString(p.Out, b.String())
	return err
}

// reasonWidth is the room left for a reason after the fixed columns: at least 60 on a
// terminal, 120 when stdout is not a terminal.
func (p Checklist) reasonWidth(fixed int) int {
	if p.Width == 0 {
		return 120
	}
	return max(60, p.Width-fixed)
}

// verdict pads v to width, then colors it, so the color codes never shift a column.
func (p Checklist) verdict(v Verdict, width int) string {
	s := fmt.Sprintf("%-*s", width, v)
	if !p.Color || p.Plain {
		return s
	}
	return colors[v] + s + reset
}

// identity renders the header's identity; the username and groups come from the server, so
// they are sanitized like reasons.
func identity(id Identity) string {
	s := "as " + Sanitize(id.Username)
	if id.Mode == ModeSelf {
		s += " (self)"
	}
	if !id.Verified {
		s += " (unverified)"
	}
	if len(id.Groups) > 0 {
		groups := make([]string, len(id.Groups))
		for i, g := range id.Groups {
			groups[i] = Sanitize(g)
		}
		s += " (groups: " + strings.Join(groups, ", ") + ")"
	}
	return s
}

// summaryLine: "12 passed · 1 failed (AT-05) · 1 skipped (AT-09) · 0 errors · exit 1".
func summaryLine(r Report, dot string) string {
	ids := func(v Verdict) string {
		var out []string
		for _, res := range r.Results {
			if res.Verdict == v {
				out = append(out, res.ID)
			}
		}
		if len(out) == 0 {
			return ""
		}
		if more := len(out) - 3; more > 0 {
			return fmt.Sprintf(" (%s and %d more)", strings.Join(out[:3], ", "), more)
		}
		return " (" + strings.Join(out, ", ") + ")"
	}
	skipped := fmt.Sprintf("%d skipped%s", r.Summary.Skip, ids(Skip))
	if r.FailOnSkip && r.Summary.Skip > 0 {
		skipped += ", counted as failures (--fail-on-skip)"
	}
	errors := "errors"
	if r.Summary.Error == 1 {
		errors = "error"
	}
	return fmt.Sprintf("%d passed%s%d failed%s%s%s%s%d %s%sexit %d",
		r.Summary.Pass, dot, r.Summary.Fail, ids(Fail), dot, skipped, dot, r.Summary.Error, errors, dot, r.Summary.ExitCode)
}
