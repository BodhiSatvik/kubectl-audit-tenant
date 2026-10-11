package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

func sample() Report {
	r := Report{
		Tool:      Tool{Name: "audit-tenant", Version: "0.1.0"},
		Cluster:   Cluster{Context: "kind-dev", Server: "https://127.0.0.1:6443", Version: "v1.37.0"},
		Namespace: "team-a", OtherNamespace: "kube-system",
		Identity:  Identity{Mode: "impersonation", Username: "team-a-dev", Groups: []string{"team-a", "system:authenticated"}, Verified: true},
		Timestamp: time.Date(2026, 10, 20, 15, 4, 5, 0, time.UTC),
		Results: []Result{
			{ID: "AT-01", Name: "No cluster-scoped access", Kind: Behavioral, MTB: "MTB-PL1-CC-CPI-1", Severity: High, Verdict: Pass, Layer: "RBAC", Reason: "RBAC denied all 4 actions"},
			{ID: "AT-05", Name: "NodePort services blocked", Kind: Behavioral, MTB: "MTB-PL1-BC-HI-1", Severity: Medium, Verdict: Fail, Layer: "-",
				Reason: "a NodePort Service was accepted in a dry run", Hint: "add a policy that rejects type: NodePort, or remove `create services` from the tenant role"},
			{ID: "AT-09", Name: "Pod Security Admission enforced", Kind: Config, MTB: "-", Severity: High, Verdict: Skip, Layer: "-",
				Reason: "cannot read namespace labels (forbidden)", Hint: "grant `get` on namespace team-a to this identity, or run with --as <admin>"},
		},
	}
	r.Summary = Summarize(r.Results, false)
	return r
}

func golden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func render(t *testing.T, p Checklist, r Report) string {
	t.Helper()
	var buf bytes.Buffer
	p.Out = &buf
	if err := p.Print(r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestChecklistSample(t *testing.T) {
	golden(t, "checklist-sample", render(t, Checklist{}, sample()))
}

func TestChecklistPlain(t *testing.T) {
	golden(t, "checklist-plain", render(t, Checklist{Plain: true}, sample()))
}

func TestChecklistColor(t *testing.T) {
	out := render(t, Checklist{Color: true}, sample())
	golden(t, "checklist-color", out)
	for _, want := range []string{"\x1b[32mPASS\x1b[0m", "\x1b[31mFAIL\x1b[0m", "\x1b[33mSKIP\x1b[0m"} {
		if !strings.Contains(out, want) {
			t.Errorf("colored output should contain %q", want)
		}
	}
}

func TestChecklistSanitizesAndCapsServerText(t *testing.T) {
	r := sample()
	long := strings.Repeat("admission webhook \"validate.kyverno.svc\" denied the request: ", 9) // about 600 characters
	r.Results[1].Reason = "line one\r\nline \x1b[31mtwo\x1b[0m\ttab\x07bell\n" + long
	r.Results[1].Hint = "first\nsecond \x1b[1mbold\x1b[0m"
	r.Summary = Summarize(r.Results, false)
	out := render(t, Checklist{}, r)
	golden(t, "checklist-long-ansi-reason", out)
	for _, bad := range []string{"\x1b", "\r", "\t", "\x07"} {
		if strings.Contains(out, bad) {
			t.Errorf("output still contains %q", bad)
		}
	}
	if !strings.Contains(out, "line one; line two tab") || !strings.Contains(out, "first; second bold") {
		t.Error("newlines should fold to '; ' and escapes should be stripped")
	}
	if !strings.Contains(out, "…") || !strings.Contains(out, "full reasons: -o wide or -o json") {
		t.Error("a cut reason needs the ellipsis and the full-reasons footer")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "FAIL") && len([]rune(line)) > 120+2+7+2+len("Pod Security Admission enforced")+2+5 {
			t.Errorf("a non-terminal reason is capped at 120 characters: %q", line)
		}
	}
}

func TestChecklistWidthCapHasAFloorOfSixty(t *testing.T) {
	r := sample()
	r.Results[1].Reason = strings.Repeat("x", 200)
	narrow := render(t, Checklist{Width: 70}, r)
	line := strings.Split(narrow, "\n")[3]
	reason := strings.TrimSpace(line[strings.Index(line, "NodePort services blocked")+len("NodePort services blocked"):])
	if n := len([]rune(reason)); n != 60 {
		t.Errorf("reason on a 70-column terminal has %d characters, want 60 (floor): %q", n, reason)
	}
	wide := render(t, Checklist{Width: 200}, r)
	if !strings.Contains(wide, strings.Repeat("x", 100)) {
		t.Error("a wide terminal keeps more of the reason")
	}
}

func TestChecklistAdminHintAndUnverifiedSelf(t *testing.T) {
	r := sample()
	r.Identity = Identity{Mode: "self", Username: "kind-dev", Verified: false}
	r.AdminHint = true
	out := render(t, Checklist{}, r)
	golden(t, "checklist-admin-hint", out)
	for _, want := range []string{"as kind-dev (self) (unverified)", "this identity looks like a cluster admin; add --as to test a tenant"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q", want)
		}
	}
}

func TestChecklistSummaryListsThreeIDsThenMore(t *testing.T) {
	r := sample()
	r.Results = nil
	for _, id := range []string{"AT-01", "AT-02", "AT-03", "AT-04.1"} {
		r.Results = append(r.Results, Result{ID: id, Name: id, Verdict: Fail, Reason: "no", Hint: "fix"})
	}
	for _, id := range []string{"AT-06", "AT-07", "AT-08", "AT-09"} {
		r.Results = append(r.Results, Result{ID: id, Name: id, Verdict: Skip, Reason: "no", Hint: "fix"})
	}
	r.Results = append(r.Results, Result{ID: "AT-10", Name: "AT-10", Verdict: Error, Reason: "boom", Hint: "retry"})
	r.FailOnSkip = true
	r.Summary = Summarize(r.Results, true)
	out := render(t, Checklist{}, r)
	golden(t, "checklist-fail-on-skip", out)
	want := "0 passed · 4 failed (AT-01, AT-02, AT-03 and 1 more) · 4 skipped (AT-06, AT-07, AT-08 and 1 more), counted as failures (--fail-on-skip) · 1 error · exit 1"
	if !strings.Contains(out, want) {
		t.Errorf("summary line missing; want %q in:\n%s", want, out)
	}
}

func TestWantColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if WantColor(&bytes.Buffer{}, false) {
		t.Error("a buffer is not a terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if WantColor(os.Stdout, false) {
		t.Error("NO_COLOR set means no color")
	}
	t.Setenv("NO_COLOR", "")
	if WantColor(os.Stdout, true) {
		t.Error("--no-color means no color")
	}
	if Width(&bytes.Buffer{}) != 0 {
		t.Error("a buffer has no width")
	}
}
