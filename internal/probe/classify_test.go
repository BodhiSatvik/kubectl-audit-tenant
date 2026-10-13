package probe

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// messages reads testdata/messages.txt into API errors keyed by rule id.
func messages(t *testing.T) map[string]error {
	t.Helper()
	data, err := os.ReadFile("testdata/messages.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]error{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.SplitN(line, "\t", 4)
		code, err := strconv.ParseInt(cols[1], 10, 32)
		if err != nil {
			t.Fatalf("%s: %v", cols[0], err)
		}
		var msg string
		if err := json.Unmarshal([]byte(cols[3]), &msg); err != nil {
			t.Fatalf("%s: %v", cols[0], err)
		}
		out[cols[0]] = statusError(int32(code), cols[2], msg)
	}
	return out
}

func statusError(code int32, reason, msg string) error {
	return &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: code, Reason: metav1.StatusReason(reason), Message: msg}}
}

func TestClassifyEveryRuleOnARealServerString(t *testing.T) {
	msgs := messages(t)
	tests := []struct {
		id      string
		verdict report.Verdict
		layer   string
		reason  string // the reason starts with this
		hint    string // the hint contains this; empty for a PASS
	}{
		{"psa", report.Pass, "admission (PSA)", `violates PodSecurity "restricted:latest": privileged (container "probe"`, ""},
		{"webhook", report.Pass, "admission (webhook: validate.kyverno.svc-fail)", `admission webhook "validate.kyverno.svc-fail" denied the request`, ""},
		{"policy", report.Pass, "admission (policy: deny-nodeport)", "ValidatingAdmissionPolicy 'deny-nodeport' with binding 'deny-nodeport-binding' denied request", ""},
		{"dry-run", report.Skip, "-", `admission webhook "side-effects.example.com" does not support dry run`, "sideEffects: NoneOnDryRun"},
		{"quota-exceeded", report.Skip, "-", `pods "audit-tenant-probe-nk6zp" is forbidden: exceeded quota`, "ResourceQuota"},
		{"quota-must-specify", report.Skip, "-", `pods "audit-tenant-probe-b6zp4" is forbidden: failed quota`, "ResourceQuota"},
		{"limitrange-min", report.Skip, "-", `pods "audit-tenant-probe-vxbn5" is forbidden: minimum cpu`, "LimitRange"},
		{"limitrange-max", report.Skip, "-", `pods "audit-tenant-probe-688lv" is forbidden: maximum memory`, "LimitRange"},
		{"invalid", report.Error, "-", `Pod "audit-tenant-probe-8f4bk" is invalid`, "a bug in audit-tenant"},
		{"bad-request", report.Error, "-", `Pod in version "v1" cannot be handled as a Pod`, "a bug in audit-tenant"},
		{"other", report.Error, "-", `pods is forbidden: User "system:serviceaccount:tenant-a:default" cannot create resource "pods"`, "does not recognize"},
	}
	if len(tests) != len(msgs) {
		t.Fatalf("%d cases for %d messages: every message in testdata needs a case", len(tests), len(msgs))
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			err, ok := msgs[tt.id]
			if !ok {
				t.Fatalf("no message %q in testdata", tt.id)
			}
			got := Classify(err)
			if got.Verdict != tt.verdict || got.Layer != tt.layer || !strings.HasPrefix(got.Reason, tt.reason) {
				t.Errorf("Classify() = %+v,\nwant %s, layer %q, reason starting %q", got, tt.verdict, tt.layer, tt.reason)
			}
			if (tt.hint == "") != (got.Hint == "") || !strings.Contains(got.Hint, tt.hint) {
				t.Errorf("hint = %q, want one containing %q (empty only on PASS)", got.Hint, tt.hint)
			}
		})
	}
}

func TestClassifyRuleOrder(t *testing.T) {
	msgs := messages(t)
	// Kyverno answers HTTP 400 and a ValidatingAdmissionPolicy HTTP 422: the admission text
	// must win over "400 or 422 means a malformed probe", or a real block reads as a tool bug.
	for _, id := range []string{"webhook", "policy"} {
		if got := Classify(msgs[id]); got.Verdict != report.Pass {
			t.Errorf("%s = %+v, want PASS before the 400/422 rule", id, got)
		}
	}
	// A webhook that cannot dry-run is SKIP, not a denial, though its text names a webhook.
	if got := Classify(msgs["dry-run"]); got.Verdict != report.Skip {
		t.Errorf("dry-run = %+v, want SKIP", got)
	}
}

func TestClassifyAcceptedEmptyAndUnsent(t *testing.T) {
	if got := Classify(nil); got.Verdict != report.Fail || got.Layer != "-" {
		t.Errorf("Classify(nil) = %+v, want FAIL on layer -: the probe was accepted", got)
	}
	if got := Classify(statusError(500, "InternalError", "")); got.Verdict != report.Error || got.Reason != "HTTP 500 InternalError" || got.Hint == "" {
		t.Errorf("empty message = %+v, want ERROR \"HTTP 500 InternalError\" with a hint", got)
	}
	if got := Classify(errors.New("dial tcp: connection refused")); got.Verdict != report.Error || !strings.Contains(got.Reason, "connection refused") || got.Hint == "" {
		t.Errorf("transport error = %+v, want ERROR naming the error, with a hint", got)
	}
}
