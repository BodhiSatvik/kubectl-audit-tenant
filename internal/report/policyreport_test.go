package report

import (
	"bytes"
	"maps"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

func TestPolicyReportSample(t *testing.T) {
	r := sample()
	r.Results[0].Note = "not tested: grants limited to named objects (resourceNames)"
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
	if got.APIVersion != "wgpolicyk8s.io/v1alpha2" || got.Kind != "PolicyReport" || got.Metadata.Name != "audit-tenant-team-a-dev" || got.Metadata.Namespace != "team-a" {
		t.Errorf("object = %s %s %s/%s", got.APIVersion, got.Kind, got.Metadata.Namespace, got.Metadata.Name)
	}
	if got.Metadata.Labels["app.kubernetes.io/managed-by"] != "audit-tenant" || got.Metadata.Annotations["audit-tenant.dev/groups"] != "team-a,system:authenticated" ||
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

func TestPolicyReportNameIsOnePerIdentity(t *testing.T) {
	long := "oidc:" + strings.Repeat("platform-engineer.", 6) // 113 characters
	for _, tt := range []struct{ identity, want string }{
		{"system:serviceaccount:tenant-a:tenant-a-dev", "audit-tenant-system-serviceaccount-tenant-a-tenant-a-dev"},
		{"Alice@Example.COM", "audit-tenant-alice-example-com"},
		{"user:", "audit-tenant-user"}, // a name ends with a letter or digit
		{"", "audit-tenant"},
		{long, "audit-tenant-oidc-platform-engineer-platform-engineer-"}, // a prefix: the cut slug and its hash follow
	} {
		got := reportName(tt.identity)
		if !strings.HasPrefix(got, tt.want) || (tt.identity != long && got != tt.want) {
			t.Errorf("reportName(%q) = %q, want %q", tt.identity, got, tt.want)
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
			t.Errorf("reportName(%q) = %q is not a valid object name: %v", tt.identity, got, errs)
		}
	}
	// Cut to 54 characters plus a hash of the full identity, so two long identities with the same
	// first 54 characters still get their own report (D27).
	a, b := reportName(long+"a"), reportName(long+"b")
	if a == b || len(a) != len("audit-tenant-")+54+1+8 {
		t.Errorf("long identities: %q and %q; want different names of %d characters", a, b, len("audit-tenant-")+54+1+8)
	}
}
