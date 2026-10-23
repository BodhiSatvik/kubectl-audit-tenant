package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// validate checks doc against docs/report.schema.json, the schema the e2e also validates with.
func validate(doc []byte) error {
	c := jsonschema.NewCompiler()
	c.AssertFormat() // as the e2e's jv -f: the timestamp must be a date-time
	sch, err := c.Compile("../../docs/report.schema.json")
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return err
	}
	return sch.Validate(inst)
}

func printJSON(t *testing.T, r Report) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestJSONMatchesTheSchema(t *testing.T) {
	r := sample()
	r.Results[0].Note = "not tested: grants limited to named objects (resourceNames)" // D86: the note travels with the result
	out := printJSON(t, r)
	golden(t, "json-sample", string(out))
	r.Warnings = []string{"--other-namespace prod is not checked"}
	if err := validate(printJSON(t, r)); err != nil {
		t.Errorf("a report with warnings does not match the schema: %v", err)
	}
	if err := validate(out); err != nil {
		t.Fatalf("-o json does not match docs/report.schema.json: %v", err)
	}
	var got struct {
		APIVersion string `json:"apiVersion"`
		Results    []Result
		Summary    Summary
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != "audit-tenant.dev/v1alpha1" || got.Results[0].Note == "" || got.Summary != r.Summary {
		t.Errorf("apiVersion %q, note %q, summary %+v; want audit-tenant.dev/v1alpha1, the note, and %+v", got.APIVersion, got.Results[0].Note, got.Summary, r.Summary)
	}
}

func TestJSONKeepsServerTextWithoutRawControlBytes(t *testing.T) {
	r := sample()
	r.Identity = Identity{Mode: ModeSelf, Username: "dev"} // unverified self mode: no groups
	r.Results[1].Reason = "line one\nline \x1b[31mtwo\x1b[0m"
	out := printJSON(t, r)
	if err := validate(out); err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := string(out)
	if strings.ContainsAny(s, "\x1b\x07") || !strings.Contains(s, `line one\nline <U+001B>[31mtwo`) { // D170: a decoded string holds no control either
		t.Errorf("the full server text is kept, JSON-escaped:\n%s", s)
	}
	if !strings.Contains(s, `"groups": []`) || !strings.Contains(s, "--as <admin>") {
		t.Errorf("no groups must print as [] and text must not be HTML-escaped:\n%s", s)
	}
}

func TestTheSchemaRejectsWhatTheContractForbids(t *testing.T) {
	for name, edit := range map[string]func(m map[string]any){
		"an unknown verdict":        func(m map[string]any) { result(m)["verdict"] = "MAYBE" },
		"a SKIP without a hint":     func(m map[string]any) { result(m)["hint"] = "" }, // acceptance criterion 6
		"no summary":                func(m map[string]any) { delete(m, "summary") },
		"exit code 3":               func(m map[string]any) { m["summary"].(map[string]any)["exitCode"] = 3 },
		"another apiVersion":        func(m map[string]any) { m["apiVersion"] = "audit-tenant.dev/v1" },
		"an empty note":             func(m map[string]any) { result(m)["note"] = "" },
		"a result with no severity": func(m map[string]any) { delete(result(m), "severity") },
		"an empty warnings list":    func(m map[string]any) { m["warnings"] = []any{} },
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(printJSON(t, sample()), &m); err != nil {
				t.Fatal(err)
			}
			edit(m)
			doc, _ := json.Marshal(m)
			if validate(doc) == nil {
				t.Errorf("the schema accepts a report with %s", name)
			}
		})
	}
}

func TestTheSchemaAcceptsAddedFields(t *testing.T) {
	// D171: a minor release may add a field at any level; a consumer that pins the schema keeps working.
	var m map[string]any
	if err := json.Unmarshal(printJSON(t, sample()), &m); err != nil {
		t.Fatal(err)
	}
	m["extra"] = true
	for _, obj := range []string{"tool", "cluster", "identity", "summary"} {
		m[obj].(map[string]any)["extra"] = true
	}
	result(m)["extra"] = true
	doc, _ := json.Marshal(m)
	if err := validate(doc); err != nil {
		t.Errorf("the schema rejects added fields: %v", err)
	}
}

func TestMachineOutputMarksControlAndFormatCharacters(t *testing.T) {
	// D170: a right-to-left override in any string a dashboard shows must not reach it raw.
	const rlo = "\u202e"
	r := sample()
	r.Tool.Version += rlo
	r.Cluster = Cluster{Context: "kind" + rlo, Server: "https://x" + rlo, Version: "v1" + rlo}
	r.Identity.Username += rlo
	r.Identity.Groups = []string{"devs" + rlo}
	r.Warnings = []string{"warn" + rlo}
	for i := range r.Results {
		r.Results[i].Layer += rlo
		r.Results[i].Reason += rlo + "\x7f\u0085\r\tend\n"
		r.Results[i].Hint += rlo
		r.Results[i].Note = "note" + rlo
	}
	before := r.Results[0].Reason
	out := printJSON(t, r)
	var pr bytes.Buffer
	if err := PolicyReport(&pr, r); err != nil {
		t.Fatal(err)
	}
	// One marker per string: JSON has 19 (tool, cluster 3, identity 2, warning, 4 per result); the
	// PolicyReport 21 (6 annotations, 5 per result), its JSON-in-annotation strings not HTML-escaped.
	for name, tc := range map[string]struct {
		out     string
		markers int
	}{"-o json": {string(out), 19}, "-o policyreport": {pr.String(), 21}} {
		if strings.ContainsAny(tc.out, rlo+"\x7f\u0085\r") || strings.Count(tc.out, "<U+202E>") != tc.markers || !strings.Contains(tc.out, "<U+007F><U+0085><U+000D>") {
			t.Errorf("%s keeps a raw control or format character, or has not %d markers:\n%s", name, tc.markers, tc.out)
		}
	}
	if !strings.Contains(string(out), `<U+000D>\tend\n`) {
		t.Errorf("tab and newline must stay as they are:\n%s", out)
	}
	if err := validate(out); err != nil {
		t.Errorf("schema: %v", err)
	}
	if r.Results[0].Reason != before {
		t.Errorf("the caller's report changed: %q", r.Results[0].Reason)
	}
}

func TestMachineOutputCarriesTheAdminHint(t *testing.T) {
	// D173: a self run as a cluster admin reads as a wide-open cluster; the reason travels with the report.
	r := sample()
	r.AdminHint = true
	r.Warnings = make([]string, 1, 2) // spare capacity: the hint must not land in the caller's array
	r.Warnings[0] = "--other-namespace prod is not checked"
	out := printJSON(t, r)
	var got struct{ Warnings []string }
	if err := json.Unmarshal(out, &got); err != nil || len(got.Warnings) != 2 || got.Warnings[0] != r.Warnings[0] || got.Warnings[1] != adminHint || r.Warnings[:2][1] != "" {
		t.Errorf("err %v, warnings %q; want the run's caveat, then the admin hint, and the caller's slice untouched", err, got.Warnings)
	}
	var pr bytes.Buffer
	if err := PolicyReport(&pr, r); err != nil || !strings.Contains(pr.String(), "looks like a cluster admin") {
		t.Errorf("err %v; the PolicyReport warnings miss the admin hint:\n%s", err, pr.String())
	}
}

// result is the SKIP result of sample() in a decoded report.
func result(m map[string]any) map[string]any { return m["results"].([]any)[2].(map[string]any) }
