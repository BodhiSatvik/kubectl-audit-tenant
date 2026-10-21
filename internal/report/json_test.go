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
	if strings.ContainsAny(s, "\x1b\x07") || !strings.Contains(s, `line one\nline \u001b[31mtwo`) {
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
		"an unknown field":          func(m map[string]any) { m["extra"] = true },
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

// result is the SKIP result of sample() in a decoded report.
func result(m map[string]any) map[string]any { return m["results"].([]any)[2].(map[string]any) }
