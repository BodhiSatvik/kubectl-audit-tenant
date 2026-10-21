package report

import (
	"bytes"
	"maps"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

func TestPolicyReportSample(t *testing.T) {
	r := sample()
	r.Results[0].Note = "not tested: grants limited to named objects (resourceNames)"
	r.Warnings = []string{"--checks or --skip left out results"} // D146: the caveats travel with the report
	var buf bytes.Buffer
	if err := PolicyReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	golden(t, "policyreport-sample", buf.String())

	var got struct {
		APIVersion, Kind string
		Metadata         struct {
			Name, Namespace string
			Labels          map[string]string
			Annotations     map[string]string
		}
		Scope   map[string]string
		Summary map[string]int
		Results []struct {
			Policy, Rule, Result, Severity, Category, Source, Message string
			Timestamp                                                 struct{ Seconds, Nanos int64 }
			Properties                                                map[string]string
		}
	}
	if err := yaml.UnmarshalStrict(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != "wgpolicyk8s.io/v1alpha2" || got.Kind != "PolicyReport" || got.Metadata.Name != reportName(r.Identity) || got.Metadata.Namespace != "team-a" {
		t.Errorf("object = %s %s %s/%s", got.APIVersion, got.Kind, got.Metadata.Namespace, got.Metadata.Name)
	}
	if got.Metadata.Labels["app.kubernetes.io/managed-by"] != "audit-tenant" || got.Metadata.Annotations["audit-tenant.dev/groups"] != `["team-a","system:authenticated"]` ||
		got.Metadata.Annotations["audit-tenant.dev/other-namespace"] != "kube-system" || got.Metadata.Annotations["audit-tenant.dev/warnings"] != `["--checks or --skip left out results"]` ||
		got.Metadata.Annotations["audit-tenant.dev/verified"] != "true" || got.Metadata.Annotations["audit-tenant.dev/identity"] != "team-a-dev" || got.Metadata.Annotations["audit-tenant.dev/tool-version"] != "0.1.0" {
		t.Errorf("labels %v, annotations %v", got.Metadata.Labels, got.Metadata.Annotations)
	}
	if got.Scope["kind"] != "Namespace" || got.Scope["name"] != "team-a" || got.Scope["apiVersion"] != "v1" {
		t.Errorf("scope = %v, want the target Namespace", got.Scope)
	}
	if want := map[string]int{"pass": 1, "fail": 1, "warn": 0, "error": 0, "skip": 1}; !maps.Equal(got.Summary, want) {
		t.Errorf("summary = %v, want %v", got.Summary, want)
	}
	pass, fail, skip := got.Results[0], got.Results[1], got.Results[2]
	if pass.Policy != "AT-01" || pass.Rule != "MTB-PL1-CC-CPI-1" || pass.Result != "pass" || pass.Severity != "high" || pass.Category != "multi-tenancy" || pass.Source != "audit-tenant" ||
		pass.Message != "RBAC denied all 4 actions" || pass.Timestamp.Seconds != r.Timestamp.Unix() || pass.Properties["layer"] != "RBAC" ||
		pass.Properties["identity"] != "team-a-dev" || pass.Properties["name"] != "No cluster-scoped access" || pass.Properties["note"] == "" {
		t.Errorf("PASS result = %+v", pass)
	}
	if _, ok := pass.Properties["hint"]; ok {
		t.Errorf("a PASS has no hint property: %v", pass.Properties)
	}
	if fail.Result != "fail" || fail.Properties["hint"] == "" || skip.Result != "skip" || skip.Rule != "" {
		t.Errorf("FAIL %+v, SKIP %+v: want lowercase results, a hint, and no rule when the MTB ID is -", fail, skip)
	}
}

func TestPolicyReportNameIsOnePerIdentityAndGroupSet(t *testing.T) {
	// D144, D147: audit-tenant-<slug cut to 54>-<32 hex of the requested identity>, so identities that
	// differ in case, in replaced characters or in groups keep their own reports (D27).
	hashed := regexp.MustCompile(`^audit-tenant-(?:([a-z0-9-]*[a-z0-9])-)?[0-9a-f]{32}$`) // no slug when nothing readable is left
	sa := Identity{Mode: ModeImpersonation, Username: "system:serviceaccount:tenant-a:tenant-a-dev", Groups: []string{"system:serviceaccounts", "system:authenticated"}}
	names := map[string]Identity{}
	for _, id := range []Identity{
		sa,
		{Mode: ModeImpersonation, Username: sa.Username, Groups: []string{"devs", "system:authenticated"}}, // the same user with other groups
		{Mode: ModeImpersonation, Username: "audit-probe", Groups: []string{"tenant-a"}},                   // --as audit-probe --as-group <group>
		{Mode: ModeImpersonation, Username: "audit-probe", Groups: []string{"tenant-b"}},                   // ... for a second group
		{Username: "alice@example.com"}, {Username: "Alice@Example.com"}, // case
		{Mode: ModeImpersonation, Username: "alice@example.com"},           // --as alice without her identity provider's groups (D151)
		{Username: "alice-example-com"}, {Username: ""}, {Username: "@@@"}, // replaced characters, nothing left
		{Username: strings.Repeat("a", 54)}, {Username: strings.Repeat("a", 55)}, // the cut
	} {
		name := reportName(id)
		m := hashed.FindStringSubmatch(name)
		if m == nil || len(validation.IsDNS1123Subdomain(name)) > 0 || len(name) > len("audit-tenant-")+54+1+32 {
			t.Errorf("reportName(%+v) = %q: want audit-tenant-<slug>-<32 hex>, a valid name of at most 100 characters", id, name)
			continue
		}
		if other, seen := names[name]; seen {
			t.Errorf("%+v and %+v share the report name %q", other, id, name)
		}
		names[name] = id
	}
	if got := hashed.FindStringSubmatch(reportName(sa))[1]; got != "system-serviceaccount-tenant-a-tenant-a-dev" {
		t.Errorf("slug = %q, want the readable identity", got)
	}
	if got := hashed.FindStringSubmatch(reportName(Identity{Username: strings.Repeat("a", 55)}))[1]; got != strings.Repeat("a", 54) {
		t.Errorf("a 55-character slug = %q, want it cut to 54", got)
	}
	if reportName(Identity{Mode: ModeImpersonation, Username: "u", Groups: []string{"b", "a"}}) != reportName(Identity{Mode: ModeImpersonation, Username: "u", Groups: []string{"a", "b"}}) {
		t.Error("the group order the server returns must not rename the report")
	}
}

func TestPolicyReportNameIsTheSameForAVerifiedAndAnUnverifiedRun(t *testing.T) {
	// D147: the hash covers the requested identity. A SelfSubjectReview adds system:authenticated and a
	// service account's own groups; a run whose review failed has only the requested groups.
	saUser := "system:serviceaccount:tenant-a:tenant-a-dev"
	for _, tt := range []struct{ verified, unverified Identity }{
		{Identity{Mode: ModeImpersonation, Username: saUser, Groups: []string{"system:serviceaccounts", "system:serviceaccounts:tenant-a", "system:authenticated"}, Verified: true},
			Identity{Mode: ModeImpersonation, Username: saUser}},
		{Identity{Mode: ModeImpersonation, Username: "audit-probe", Groups: []string{"tenant-a", "system:authenticated"}, Verified: true},
			Identity{Mode: ModeImpersonation, Username: "audit-probe", Groups: []string{"tenant-a"}}},
		// A self-mode run without a verified identity never prints a PolicyReport (D151).
	} {
		if a, b := reportName(tt.verified), reportName(tt.unverified); a != b {
			t.Errorf("verified %q and unverified %q runs of one identity must share a report name", a, b)
		}
	}
	if reportName(Identity{Mode: ModeImpersonation, Username: saUser}) == reportName(Identity{Mode: ModeImpersonation, Username: saUser, Groups: []string{"devs"}}) {
		t.Error("requested groups still name their own report")
	}
	// Ship review (D149): the API server adds a service account's own groups only when no group is
	// given, so --as-group devs without them is another identity than devs with them, and one of the
	// two is not the pair the server adds.
	withOwn := Identity{Mode: ModeImpersonation, Username: saUser, Groups: []string{"devs", "system:serviceaccounts", "system:serviceaccounts:tenant-a"}}
	if reportName(withOwn) == reportName(Identity{Mode: ModeImpersonation, Username: saUser, Groups: []string{"devs"}}) {
		t.Error("a service account with --as-group but without its own groups must not replace the report of one with them")
	}
	if reportName(Identity{Mode: ModeImpersonation, Username: saUser, Groups: []string{"system:serviceaccounts"}}) == reportName(Identity{Mode: ModeImpersonation, Username: saUser}) {
		t.Error("one of a service account's two own groups is not the pair the API server adds")
	}
	both := Identity{Mode: ModeImpersonation, Username: saUser, Groups: []string{"system:serviceaccounts:tenant-a", "system:serviceaccounts"}}
	if reportName(both) != reportName(Identity{Mode: ModeImpersonation, Username: saUser}) {
		t.Error("--as-group with exactly the pair the API server adds is the same identity as no --as-group")
	}
	// Red Team (D151): an impersonated service account with its own groups is the token identity, and
	// an impersonated user is not that user's own login.
	if reportName(Identity{Mode: ModeSelf, Username: saUser, Groups: both.Groups, Verified: true}) != reportName(Identity{Mode: ModeImpersonation, Username: saUser}) {
		t.Error("the service account's token and --as of that service account must share a report")
	}
	if reportName(Identity{Mode: ModeSelf, Username: "alice", Groups: []string{"devs"}, Verified: true}) == reportName(Identity{Mode: ModeImpersonation, Username: "alice", Groups: []string{"system:authenticated"}, Verified: true}) {
		t.Error("--as alice without groups tests fewer rights than alice; it must not replace her own report")
	}
}

// Extended by the /ship test coverage audit (Phase 4), pass 1: the warnings annotation check.
// Value: protects=a run with no warnings writes no audit-tenant.dev/warnings annotation (DESIGN.md section 6: "when the run has any", D146);
// fails_when=the len(r.Warnings) guard is dropped, so every clean report carries warnings: "null"; why_new=the sample sets warnings and the CLI test reads no annotations;
// seam=none; no_claim=how dashboards show annotations
func TestPolicyReportPrintsNoGroupsAsAnEmptyArray(t *testing.T) {
	r := sample()
	r.Identity = Identity{Mode: ModeSelf, Username: "dev"}
	var buf bytes.Buffer
	if err := PolicyReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Metadata struct{ Annotations map[string]string }
	}
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil || got.Metadata.Annotations["audit-tenant.dev/groups"] != "[]" {
		t.Errorf("err %v, groups annotation %q; want []", err, got.Metadata.Annotations["audit-tenant.dev/groups"])
	}
	if w, ok := got.Metadata.Annotations["audit-tenant.dev/warnings"]; ok {
		t.Errorf("a run with no warnings has the warnings annotation %q; want none", w)
	}
}

func TestPolicyReportEscapesServerControlBytes(t *testing.T) {
	r := sample()
	r.Results[1].Reason = "line one\nline \x1b[31mtwo\x1b[0m\x07\r"
	r.Results[1].Layer = "admission (webhook: evil\x1b[2K.example.com)"
	var buf bytes.Buffer
	if err := PolicyReport(&buf, r); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), "\x1b\x07\r") {
		t.Errorf("-o policyreport carries raw control bytes:\n%q", buf.String())
	}
	var got struct{ Results []struct{ Message string } }
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil || got.Results[1].Message != r.Results[1].Reason {
		t.Errorf("the message must round-trip whole: err %v, got %q", err, got.Results[1].Message)
	}
}
