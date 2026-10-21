package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
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
		Request:   Request{User: "team-a-dev", Groups: []string{"team-a"}},
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
	r.Results[1].Hint = "first\nsecond \x1b[1mbold\x1b[0m\x1b]0;evil title\x07"
	r.Summary = Summarize(r.Results, false)
	out := render(t, Checklist{}, r)
	golden(t, "checklist-long-ansi-reason", out)
	for _, bad := range []string{"\x1b", "\r", "\t", "\x07"} {
		if strings.Contains(out, bad) {
			t.Errorf("output still contains %q", bad)
		}
	}
	if !strings.Contains(out, "line one; line two tab") || !strings.Contains(out, "first; second bold\n") {
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

func TestChecklistPrintsWarningsUnderTheHeader(t *testing.T) {
	r := sample()
	r.Warnings = []string{"--as alice has no --as-group: ...", "--other-namespace \x1b[2Kprod is not checked"} // flag values are user text
	out := render(t, Checklist{}, r)
	golden(t, "checklist-warnings", out)
	if !strings.Contains(out, "cluster kind-dev\nwarning: --as alice has no --as-group: ...\nwarning: --other-namespace prod is not checked\n\n") {
		t.Errorf("warnings follow the header, sanitized:\n%s", out)
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
	if Width(&bytes.Buffer{}) != 0 {
		t.Error("a buffer has no width")
	}
	for _, tt := range []struct {
		name         string
		tty, noColor bool
		env          string
		want         bool
	}{
		{"terminal", true, false, "", true},
		{"NO_COLOR set", true, false, "1", false},
		{"--no-color", true, true, "", false},
		{"pipe", false, false, "", false},
	} {
		t.Setenv("NO_COLOR", tt.env)
		if got := wantColor(tt.tty, tt.noColor); got != tt.want {
			t.Errorf("%s: wantColor = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestChecklistAllPass(t *testing.T) {
	r := sample()
	for i := range r.Results {
		r.Results[i].Verdict, r.Results[i].Hint = Pass, ""
	}
	r.Summary = Summarize(r.Results, false)
	out := render(t, Checklist{}, r)
	golden(t, "checklist-all-pass", out)
	if !strings.Contains(out, "3 passed · 0 failed · 0 skipped · 0 errors · exit 0") || strings.Contains(out, "fix") {
		t.Errorf("an all-PASS report has a bare summary and no fix lines:\n%s", out)
	}
}

func TestChecklistSanitizesTheHeader(t *testing.T) {
	r := sample()
	r.Namespace = "team-a\x1b[2K"
	r.Cluster.Context = "kind\u202eved"
	r.Identity = Identity{Mode: ModeSelf, Username: "u\x1b[2K\rPASS", Groups: []string{"g\x1b]0;x\x07", "h\n"}}
	out := render(t, Checklist{}, r)
	if strings.ContainsAny(out, "\x1b\r\x07\u202e") || !strings.HasPrefix(out, "namespace team-a · as uPASS (self) (unverified) (groups: g, h) · cluster kindved\n") {
		t.Errorf("header carries server text unsanitized: %q", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestChecklistPrintsANoteUnderItsResult(t *testing.T) {
	// D86: what a PASS did not test is never cut with the reason; a fix line still comes first.
	var out bytes.Buffer
	r := Report{Namespace: "team-a", Identity: Identity{Mode: ModeSelf, Username: "dev", Verified: true}, Results: []Result{
		{ID: "AT-03", Name: "No privilege escalation", Verdict: Pass, Reason: strings.Repeat("r", 300), Note: "not tested: grants limited to named roles (resourceNames)"},
		{ID: "AT-05", Name: "NodePort services blocked", Verdict: Fail, Reason: "accepted", Hint: "add a policy", Note: "tested on create only"},
	}}
	if err := (Checklist{Out: &out}).Print(r); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	want := []string{
		"      note   not tested: grants limited to named roles (resourceNames)",
		"      fix    add a policy",
		"      note   tested on create only",
	}
	if !strings.Contains(out.String(), want[0]+"\n") || !slices.Contains(lines, want[1]) || slices.Index(lines, want[1]) > slices.Index(lines, want[2]) {
		t.Errorf("checklist:\n%s\nwant the note lines %q, a fix line before a note", out.String(), want)
	}
}

// wideSample has one result per deciding layer of DESIGN.md section 6, in ID order.
func wideSample() Report {
	r := sample()
	long := "estimate: tenant-a-quota requests.cpu: peak needs 6, hard is 4, short by 2 (deployments: api 3->4, worker 2->3); tenant-a-quota pods: peak needs 24, hard is 20, short by 4"
	r.Results = []Result{
		{ID: "AT-01", Name: "No cluster-scoped access", MTB: "MTB-PL1-CC-CPI-1", Verdict: Pass, Layer: "RBAC", Reason: "RBAC denied all 4 actions"},
		{ID: "AT-04.1", Name: "Privileged container blocked", MTB: "MTB-PL1-BC-CPI-5", Verdict: Pass, Layer: "API server (allow-privileged=false)",
			Reason: "spec.containers[0].securityContext.privileged: Forbidden: disallowed by cluster policy", Note: "tested in a regular container only (not initContainers or ephemeral containers)"},
		{ID: "AT-04.2", Name: "hostPath volume blocked", MTB: "MTB-PL1-BC-HI-1", Verdict: Pass, Layer: "admission (PSA)", Reason: `violates PodSecurity "restricted:latest": hostPath volumes (volume "host")`},
		{ID: "AT-04.3", Name: "hostPID blocked", MTB: "MTB-PL1-BC-HI-4", Verdict: Pass, Layer: "admission (webhook: validate.kyverno.svc-fail)", Reason: `admission webhook "validate.kyverno.svc-fail" denied the request: host namespaces are not allowed`},
		{ID: "AT-04.4", Name: "hostNetwork blocked", MTB: "MTB-PL1-BC-HI-3", Verdict: Pass, Layer: "admission (policy: deny-host-network)", Reason: "ValidatingAdmissionPolicy 'deny-host-network' with binding 'b' denied request: no host network"},
		{ID: "AT-05", Name: "NodePort services blocked", MTB: "MTB-PL1-BC-HI-1", Verdict: Pass, Layer: "admission (quota: no-nodeports)", Reason: "exceeded quota: no-nodeports, requested: services.nodeports=1, used: services.nodeports=0, limited: services.nodeports=0"},
		{ID: "AT-06", Name: "ResourceQuota present and complete", MTB: "MTB-PL1-CC-FNS-1", Verdict: Pass, Layer: "config", Reason: "ResourceQuota tenant-a-quota has no scopes and sets all five keys"},
		{ID: "AT-09", Name: "Pod Security Admission enforced", MTB: "-", Verdict: Skip, Layer: "-", Reason: "cannot read namespace labels (forbidden)", Hint: "grant `get` on namespace team-a to this identity, or run with --as <admin>"},
		{ID: "AT-10", Name: "Quota covers rollout peak", MTB: "-", Verdict: Fail, Layer: "config", Reason: long, Hint: "raise the quota, or lower replicas or maxSurge"},
	}
	r.Summary = Summarize(r.Results, false)
	return r
}

func TestWideShowsMTBAndLayerAndTheFullReason(t *testing.T) {
	r := wideSample()
	out := render(t, Checklist{Wide: true, Width: 80}, r)
	golden(t, "wide-sample", out)
	if !strings.Contains(out, r.Results[8].Reason+"\n") {
		t.Error("-o wide never cuts a reason, even on a narrow terminal")
	}
	if strings.Contains(out, "…") || strings.Contains(out, "full reasons") || strings.Contains(out, "MTB IDs and layers: -o wide") {
		t.Errorf("-o wide has no cut reasons and no pointer to itself:\n%s", out)
	}
	if !strings.Contains(out, "\nmachine output: -o json, -o policyreport\n") {
		t.Errorf("-o wide still names the machine outputs:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") { // MTB and layer are the two columns after the ID
		if strings.HasPrefix(line, "PASS  AT-04.3") && !strings.HasPrefix(line, "PASS  AT-04.3  MTB-PL1-BC-HI-4   admission (webhook: validate.kyverno.svc-fail)  hostPID blocked") {
			t.Errorf("wide line = %q, want verdict, ID, MTB, layer, name", line)
		}
	}
}

func TestWideSanitizesLayerAndReasonAndStaysASCIIWhenPlain(t *testing.T) {
	r := wideSample()
	r.Results[3].Layer = "admission (webhook: evil\x1b[2K.example.com)" // the webhook name is server text
	r.Results[3].Reason = "line one\nline \x1b[31mtwo\x1b[0m\x07"
	out := render(t, Checklist{Wide: true, Plain: true}, r)
	golden(t, "wide-plain-ansi", out)
	if strings.ContainsAny(out, "\x1b\x07·…") {
		t.Errorf("-o wide --plain must be sanitized ASCII:\n%q", out)
	}
	if !strings.Contains(out, "admission (webhook: evil.example.com)") || !strings.Contains(out, "line one; line two\n") {
		t.Errorf("layer and reason must be sanitized:\n%s", out)
	}
}
