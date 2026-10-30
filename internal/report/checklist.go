package report

import (
	"cmp"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

// Checklist is the default printer: a header, one line per result, a fix line under every
// non-PASS, a note line under a result that names what it did not test, and a summary
// (DESIGN.md section 6). With Wide it is -o wide.
type Checklist struct {
	Out   io.Writer
	Color bool // the caller decides with WantColor
	Plain bool // ASCII only: no color, no "·", no "…"
	Width int  // terminal columns; 0 when Out is not a terminal
	Wide  bool // -o wide: MTB ID and deciding layer after the ID; reasons never cut
}

// Print writes the report. Server text is sanitized and, except in -o wide, capped; a cut
// reason adds the "full reasons" footer.
func (p Checklist) Print(r Report) error {
	dot, ellipsis := " · ", "…"
	if p.Plain {
		dot, ellipsis = " | ", "..."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "namespace %s%s%s%scluster %s\n", Sanitize(r.Namespace), dot, identity(r.Identity), dot, Sanitize(cmp.Or(r.Cluster.Context, r.Cluster.Server))) // in a pod, no context names the cluster (S5-F7)
	if r.AdminHint {
		b.WriteString(adminHint + "\n")
	}
	for _, w := range r.Warnings {
		b.WriteString("warning: " + Sanitize(w) + "\n") // flag values are user text
	}
	b.WriteString("\n")

	vw, iw, nw, mw, lw := 0, 0, 0, 0, 0 // column widths: verdict, ID, name, MTB, layer
	for _, res := range r.Results {
		vw, iw, nw = max(vw, len(res.Verdict)), max(iw, len(res.ID)), max(nw, len(res.Name))
		mw, lw = max(mw, len(res.MTB)), max(lw, len(Sanitize(res.Layer)))
	}
	limit := p.reasonWidth(vw + 2 + iw + 2 + nw + 2)
	cut := false
	for _, res := range r.Results {
		reason, wide, c := Sanitize(res.Reason), "", false
		if p.Wide {
			wide = fmt.Sprintf("%-*s  %-*s  ", mw, res.MTB, lw, Sanitize(res.Layer))
		} else {
			reason, c = clip(reason, limit, ellipsis)
		}
		cut = cut || c
		fmt.Fprintf(&b, "%s  %-*s  %s%-*s  %s\n", p.verdict(res.Verdict, vw), iw, res.ID, wide, nw, res.Name, reason)
		if res.Verdict != Pass {
			fmt.Fprintf(&b, "%*s  %-*s  %s\n", vw, "", iw, "fix", Sanitize(res.Hint))
		}
		if res.Note != "" { // after the fix line, which always follows a non-PASS directly
			fmt.Fprintf(&b, "%*s  %-*s  %s\n", vw, "", iw, "note", Sanitize(res.Note))
		}
	}
	b.WriteString("\n" + summaryLine(r, dot) + "\n")
	// S3-F2: a saved page is audit evidence only with its date, tool and cluster, as -o json has them.
	fmt.Fprintf(&b, "audit-tenant %s%s%s%sserver %s%sKubernetes %s\n", Sanitize(r.Tool.Version), dot, r.Timestamp.UTC().Format(time.RFC3339), dot,
		Sanitize(r.Cluster.Server), dot, Sanitize(cmp.Or(r.Cluster.Version, "unknown")))
	if !p.Wide {
		b.WriteString("MTB IDs and layers: -o wide" + dot)
	}
	b.WriteString("machine output: -o json, -o policyreport\n")
	if cut {
		b.WriteString("full reasons: -o wide or -o json\n")
	}
	out := b.String()
	if p.Plain { // server text may hold any character; ASCII only means all of it
		out = strings.Map(func(r rune) rune {
			if r > unicode.MaxASCII {
				return '?'
			}
			return r
		}, out)
	}
	_, err := io.WriteString(p.Out, out)
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
