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
	if got.APIVersion != "wgpolicyk8s.io/v1alpha2" || got.Kind != "PolicyReport" || got.Metadata.Name != reportName(r) || got.Metadata.Namespace != "team-a" {
		t.Errorf("object = %s %s %s/%s", got.APIVersion, got.Kind, got.Metadata.Namespace, got.Metadata.Name)
	}
	if got.Metadata.Labels["app.kubernetes.io/managed-by"] != "audit-tenant" || got.Metadata.Annotations["audit-tenant.dev/groups"] != `["team-a","system:authenticated"]` ||
		got.Metadata.Annotations["audit-tenant.dev/other-namespace"] != "kube-system" || got.Metadata.Annotations["audit-tenant.dev/warnings"] != `["--checks or --skip left out results"]` ||
		got.Metadata.Annotations["audit-tenant.dev/verified"] != "true" || got.Metadata.Annotations["audit-tenant.dev/identity"] != "team-a-dev" || got.Metadata.Annotations["audit-tenant.dev/tool-version"] != "0.1.0" ||
		got.Metadata.Annotations["audit-tenant.dev/mode"] != "impersonation" || got.Metadata.Annotations["audit-tenant.dev/context"] != "kind-dev" || got.Metadata.Annotations["audit-tenant.dev/server"] != "https://127.0.0.1:6443" { // D173
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
		pass.Properties["identity"] != "team-a-dev" || pass.Properties["mode"] != "impersonation" || pass.Properties["name"] != "No cluster-scoped access" || pass.Properties["note"] == "" {
		t.Errorf("PASS result = %+v", pass)
	}
	if _, ok := pass.Properties["hint"]; ok {
		t.Errorf("a PASS has no hint property: %v", pass.Properties)
	}
	if fail.Result != "fail" || fail.Properties["hint"] == "" || skip.Result != "skip" || skip.Rule != "" {
		t.Errorf("FAIL %+v, SKIP %+v: want lowercase results, a hint, and no rule when the MTB ID is -", fail, skip)
	}
}

// as and self build a run's report with only the inputs that name its PolicyReport (D154).
func as(user string, groups ...string) Report {
	return Report{Identity: Identity{Mode: ModeImpersonation, Username: user}, Request: Request{User: user, Groups: groups}}
}

func self(user string, groups ...string) Report {
	return Report{Identity: Identity{Mode: ModeSelf, Username: user, Groups: groups, Verified: true}}
}

func TestPolicyReportNameIsOnePerRunInput(t *testing.T) {
	// D144, D154: audit-tenant-<slug cut to 54>-<32 hex of the run's inputs as given>. Runs whose inputs
	// differ keep their own reports (D27), so no run replaces the FAILs of another identity or boundary.
	hashed := regexp.MustCompile(`^audit-tenant-(?:([a-z0-9-]*[a-z0-9])-)?[0-9a-f]{32}$`) // no slug when nothing readable is left
	sa := "system:serviceaccount:tenant-a:tenant-a-dev"
	uid, extra, extraBA, extraTwice, other, server := as("alice"), as("alice"), as("alice"), as("alice"), as("alice"), as("alice")
	uid.Request.UID = "u-1"
	extra.Request.Extra = map[string][]string{"scopes": {"admin", "read"}}
	extraBA.Request.Extra = map[string][]string{"scopes": {"read", "admin"}} // a policy may read the first value (Codex, D154)
	extraTwice.Request.Extra = map[string][]string{"scopes": {"admin", "admin", "read"}}
	other.OtherNamespace = "tenant-b"
	server.Cluster.Server = "https://prod.example.com:6443"
	names := map[string]Report{}
	for _, r := range []Report{
		as(sa), as(sa, "system:authenticated"), // a given group stops the API server adding the service account's own groups
		as(sa, "devs"), as(sa, "devs", "system:serviceaccounts", "system:serviceaccounts:tenant-a"),
		self(sa, "system:serviceaccounts", "system:serviceaccounts:tenant-a", "system:authenticated"), // the token: apart from --as of it
		as("audit-probe", "tenant-a"), as("audit-probe", "tenant-b"), // --as audit-probe --as-group <group>
		as("bob", "system:unauthenticated"), as("bob", "system:unauthenticated", "system:authenticated"),
		as("alice"), self("alice", "devs"), self("alice", "ops"), uid, extra, extraBA, extraTwice, other, server,
		self("alice@example.com"), self("Alice@Example.com"), // case
		self("alice-example-com"), self(""), self("@@@"), // replaced characters, nothing left
		self(strings.Repeat("a", 54)), self(strings.Repeat("a", 55)), // the cut
	} {
		name := reportName(r)
		if hashed.FindStringSubmatch(name) == nil || len(validation.IsDNS1123Subdomain(name)) > 0 || len(name) > len("audit-tenant-")+54+1+32 {
			t.Errorf("reportName(%+v) = %q: want audit-tenant-<slug>-<32 hex>, a valid name of at most 100 characters", r.Request, name)
			continue
		}
		if prev, seen := names[name]; seen {
			t.Errorf("%+v %+v and %+v %+v share the report name %q", prev.Identity, prev.Request, r.Identity, r.Request, name)
		}
		names[name] = r
	}
	if got := hashed.FindStringSubmatch(reportName(as(sa)))[1]; got != "system-serviceaccount-tenant-a-tenant-a-dev" {
		t.Errorf("slug = %q, want the readable identity", got)
	}
	if got := hashed.FindStringSubmatch(reportName(self(strings.Repeat("a", 55))))[1]; got != strings.Repeat("a", 54) {
		t.Errorf("a 55-character slug = %q, want it cut to 54", got)
	}
}

func TestPolicyReportNameKeepsOneRunsInputsTogether(t *testing.T) {
	// D154: the same inputs name the same report. Groups are a set, and the SelfSubjectReview adds
	// groups to the identity, never to the request, so a verified and an unverified run share it.
	sa := "system:serviceaccount:tenant-a:tenant-a-dev"
	verified := as(sa)
	verified.Identity.Groups, verified.Identity.Verified = []string{"system:serviceaccounts", "system:serviceaccounts:tenant-a", "system:authenticated"}, true
	renamed := as("alice") // a SelfSubjectReview that names another user still leaves the request (Codex, D154)
	renamed.Identity.Username, renamed.Identity.Verified = "kind-dev", true
	noExtra := as("alice")
	noExtra.Request.Extra = map[string][]string{}
	for _, pair := range [][2]Report{
		{verified, as(sa)},
		{renamed, as("alice")},
		{noExtra, as("alice")},
		{as("u", "b", "a"), as("u", "a", "b")},
		{as("u", "a", "a"), as("u", "a")},
		{self("alice", "ops", "devs"), self("alice", "devs", "ops")},
	} {
		if a, b := reportName(pair[0]), reportName(pair[1]); a != b {
			t.Errorf("%+v and %+v are one run's inputs; names %q and %q", pair[0].Request, pair[1].Request, a, b)
		}
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
	// D170: whole, with its control characters marked
	if err := yaml.Unmarshal(buf.Bytes(), &got); err != nil || got.Results[1].Message != "line one\nline <U+001B>[31mtwo<U+001B>[0m<U+0007><U+000D>" {
		t.Errorf("the message must round-trip whole: err %v, got %q", err, got.Results[1].Message)
	}
}
