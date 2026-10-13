package checks

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// dryRun is the only way a check creates anything: the API server runs admission and stores
// nothing. The safety transport refuses a create without it.
var dryRun = metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}

// controlFailed turns a control probe that did not get through into the results of every
// selected result: SKIP when the cluster rejected it (the probes would prove nothing), ERROR
// when it could not be sent, nil when ctx ended (the runner says so).
func controlFailed(ctx context.Context, spec Spec, err error, what, hint string) []report.Result {
	if ctx.Err() != nil {
		return nil
	}
	o := probe.Classify(err)
	if _, rejected := errors.AsType[*apierrors.StatusError](err); !rejected {
		return spec.all(report.Error, "-", o.Reason, o.Hint)
	}
	return spec.all(report.Skip, "-", "the compliant control "+what+" was rejected, so the probes prove nothing: "+o.Reason, hint)
}

// probed is one probe's result. An accepted probe is a FAIL that says what was accepted; it is
// handed to prove at once (D61).
func probed(spec Spec, r ResultSpec, err error, accepted, failHint string, prove func(report.Result)) report.Result {
	o := probe.Classify(err)
	if o.Verdict == report.Fail {
		o.Reason, o.Hint = accepted+" was accepted in a dry run", failHint
	}
	res := spec.result(r, o.Verdict, o.Layer, o.Reason, o.Hint)
	if res.Verdict == report.Fail {
		prove(res)
	}
	return res
}
