package probe

import (
	"encoding/json"
	"errors"
	"net/http"
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
		{"quota-nodeports", report.Pass, "admission (quota: qa-nodeports)", "exceeded quota: qa-nodeports, requested: services.nodeports=1", ""},
		{"quota-nodeports-full", report.Skip, "-", `services "audit-tenant-probe-k2v9q" is forbidden: exceeded quota: qa-nodeports`, "NodePort Services are allowed"},
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

func TestClassifyWebhookDenialNeedsTheAPIServerPhrase(t *testing.T) {
	// "denied" inside a webhook's name is not a denial: the API server says "denied the request".
	err := statusError(http.StatusBadRequest, "BadRequest", `admission webhook "denied-hostpath.policy.example.com" does not support dry run`)
	if got := Classify(err); got.Verdict != report.Skip || !strings.Contains(got.Hint, "NoneOnDryRun") {
		t.Errorf("Classify() = %+v, want SKIP with the sideEffects hint, never a PASS", got)
	}
}

func TestClassifyAWebhookThatQuotesPodSecurityIsTheWebhook(t *testing.T) {
	// Kyverno's podSecurity rule denies through its webhook and quotes the PSA text; the webhook decided.
	err := statusError(http.StatusBadRequest, "", "admission webhook \"validate.kyverno.svc-fail\" denied the request: \n\nresource Pod/tenant-a/audit-tenant-probe-x was blocked due to the following policies \n\npodsecurity-subrule-restricted:\n  restricted: 'Validation rule 'restricted' failed. It violates PodSecurity \"restricted:latest\": privileged'\n")
	if got := Classify(err); got.Verdict != report.Pass || got.Layer != "admission (webhook: validate.kyverno.svc-fail)" {
		t.Errorf("Classify() = %+v, want PASS on layer admission (webhook: validate.kyverno.svc-fail)", got)
	}
}

func TestClassifyALimitRangeWithSeveralViolationsIsALimitRangeSkip(t *testing.T) {
	// LimitRanger joins several violations in brackets: "is forbidden: [minimum ..., minimum ...]".
	err := statusError(http.StatusForbidden, "Forbidden", `pods "audit-tenant-probe-x" is forbidden: [minimum cpu usage per Container is 100m, but request is 1m, minimum memory usage per Container is 64Mi, but request is 1Mi]`)
	if got := Classify(err); got.Verdict != report.Skip || !strings.Contains(got.Hint, "LimitRange") {
		t.Errorf("Classify() = %+v, want SKIP with the LimitRange hint", got)
	}
}

func TestClassifyAValidationMessageThatSaysMustSpecifyIsMalformed(t *testing.T) {
	// D82: apimachinery's union validation says "must specify one of: ..."; only quota says "failed quota".
	err := statusError(http.StatusUnprocessableEntity, "Invalid", `Pod "audit-tenant-probe-x" is invalid: spec.volumes[0]: Invalid value: "": must specify one of: hostPath, emptyDir`)
	if got := Classify(err); got.Verdict != report.Error || !strings.Contains(got.Hint, "a bug in audit-tenant") {
		t.Errorf("Classify() = %+v, want ERROR with the malformed-probe hint, not a quota SKIP", got)
	}
}

func TestClassifyAnAdmissionDenialWithAServerErrorIsAnError(t *testing.T) {
	// D82: a webhook may deny with HTTP 500 when its backend fails; that is a failure, not a policy decision.
	err := statusError(http.StatusInternalServerError, "InternalError", `admission webhook "guard.example.com" denied the request: policy backend unavailable`)
	if got := Classify(err); got.Verdict != report.Error || got.Hint == "" {
		t.Errorf("Classify() = %+v, want ERROR: only a 4xx answer is a policy decision", got)
	}
}

func TestClassifyEveryTextRuleNeedsA4xx(t *testing.T) {
	// D82, D84(5): a 5xx answer that quotes admission or quota text is a failure, not a fence.
	for _, msg := range []string{
		`pods "audit-tenant-probe-x" is forbidden: violates PodSecurity "restricted:latest": privileged`,
		`services "audit-tenant-probe-x" is forbidden: ValidatingAdmissionPolicy 'deny-nodeport' with binding 'b' denied request: no`,
		`services "audit-tenant-probe-x" is forbidden: exceeded quota: q, requested: services.nodeports=1, used: services.nodeports=0, limited: services.nodeports=0`,
		// Added by the /ship test coverage audit, pass 1 (Phase 2).
		// Value: protects=a 5xx quoting quota, LimitRange or dry-run text is ERROR (exit 2), never a SKIP (exit 0 without --fail-on-skip);
		// fails_when=the 4xx guard is dropped from a SKIP rule; why_new=the rows above are PASS-rule texts only; seam=none; no_claim=the hint each ERROR carries
		`pods "audit-tenant-probe-x" is forbidden: exceeded quota: q, requested: limits.cpu=5, used: limits.cpu=0, limited: limits.cpu=1`,
		`pods "audit-tenant-probe-x" is forbidden: minimum cpu usage per Container is 100m, but request is 1m`,
		`admission webhook "side-effects.example.com" does not support dry run`,
	} {
		if got := Classify(statusError(http.StatusInternalServerError, "InternalError", msg)); got.Verdict != report.Error {
			t.Errorf("HTTP 500 %q = %+v, want ERROR", msg, got)
		}
	}
}

func TestClassifyAPolicyThatCannotEvaluateIsAnError(t *testing.T) {
	// D84(4): a ValidatingAdmissionPolicy expression or binding error also says "denied request" (HTTP 422).
	for _, msg := range []string{
		`pods "audit-tenant-probe-x" is forbidden: ValidatingAdmissionPolicy 'no-privileged' with binding 'b' denied request: expression 'object.spec.containers.all(c, !c.securityContext.privileged)' resulted in error: no such key: securityContext`,
		`pods "audit-tenant-probe-x" is forbidden: ValidatingAdmissionPolicy 'no-privileged' with binding 'b' denied request: failed to configure binding: params not found`,
	} {
		if got := Classify(statusError(http.StatusUnprocessableEntity, "Invalid", msg)); got.Verdict != report.Error || !strings.Contains(got.Hint, "ValidatingAdmissionPolicy") {
			t.Errorf("Classify(%q) = %+v, want ERROR with a policy hint, never a PASS", msg, got)
		}
	}
}
