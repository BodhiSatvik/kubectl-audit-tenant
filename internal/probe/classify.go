package probe

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// Outcome is what the cluster's answer to one probe proves.
type Outcome struct {
	Verdict report.Verdict
	Layer   string
	Reason  string
	Hint    string
}

// Hints for answers that decide nothing.
const (
	hintQuota      = "a ResourceQuota stopped the probe before the security gates (quota full, or a size it requires is missing); free quota or add a LimitRange with defaults, then re-run"
	hintLimitRange = "a LimitRange min or max stopped the probe before the security gates; check the namespace's LimitRange, then re-run"
	hintMalformed  = "a bug in audit-tenant: the cluster says the probe is malformed; please report it with --debug output"
	hintUnknown    = "the cluster rejected the probe for a reason audit-tenant does not recognize, so it decides nothing; read the reason, or run with --debug and report it"
	hintUnsent     = "re-run; if it repeats, run with --debug and report the server message"
)

// Classify reads the cluster's answer to a dry-run probe with the rules of DESIGN.md section 5,
// in order, first match wins. nil means the probe was accepted: FAIL on layer "-", and the caller
// says what was accepted. The admission rules come before the HTTP 400/422 rule because Kyverno
// denies with 400 and a ValidatingAdmissionPolicy with 422.
func Classify(err error) Outcome {
	if err == nil {
		return Outcome{Verdict: report.Fail, Layer: "-"}
	}
	status, ok := errors.AsType[*apierrors.StatusError](err)
	if !ok {
		return Outcome{report.Error, "-", "could not send the probe: " + err.Error(), hintUnsent}
	}
	s := status.ErrStatus
	msg := s.Message
	full := cmp.Or(msg, strings.TrimSpace(fmt.Sprintf("HTTP %d %s", s.Code, s.Reason)))
	switch {
	case strings.Contains(msg, "violates PodSecurity"):
		return Outcome{report.Pass, "admission (PSA)", from(msg, "violates PodSecurity"), ""}
	case strings.Contains(msg, "admission webhook") && strings.Contains(msg, "denied"):
		return Outcome{report.Pass, "admission (webhook: " + between(msg, `admission webhook "`, `"`) + ")", from(msg, "admission webhook"), ""}
	case strings.Contains(msg, "ValidatingAdmissionPolicy") && strings.Contains(msg, "denied request"):
		return Outcome{report.Pass, "admission (policy: " + between(msg, "ValidatingAdmissionPolicy '", "'") + ")", from(msg, "ValidatingAdmissionPolicy"), ""}
	case strings.Contains(msg, "does not support dry run") || strings.Contains(msg, "sideEffects"):
		return Outcome{report.Skip, "-", full, fmt.Sprintf("webhook %q cannot take a dry run, so the probe was not tested; ask its owner to set sideEffects: NoneOnDryRun in its webhook configuration",
			cmp.Or(between(msg, `admission webhook "`, `"`), "unknown"))}
	case strings.Contains(msg, "exceeded quota") || strings.Contains(msg, "must specify"):
		return Outcome{report.Skip, "-", full, hintQuota}
	case strings.Contains(msg, "forbidden: maximum") || strings.Contains(msg, "forbidden: minimum"):
		return Outcome{report.Skip, "-", full, hintLimitRange}
	case s.Code == 400 || s.Code == 422:
		return Outcome{report.Error, "-", full, hintMalformed}
	}
	return Outcome{report.Error, "-", full, hintUnknown}
}

// from returns msg from the first key on, dropping the "pods "audit-tenant-probe-x7k2p" is
// forbidden: " prefix that names only the generated probe.
func from(msg, key string) string {
	_, rest, _ := strings.Cut(msg, key)
	return key + rest
}

// between returns the text after start up to the next end, or "" when start is missing.
func between(msg, start, end string) string {
	_, rest, ok := strings.Cut(msg, start)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, end)
	return name
}
