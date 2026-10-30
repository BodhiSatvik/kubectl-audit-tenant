package checks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// answer is one SelfSubjectAccessReview read as the API defines it (D10): allowed is the
// server's word; denied is set only by an authorizer that answered with an explicit deny.
type answer struct {
	allowed bool
	denied  bool
	reason  string // status.reason, server text; on an RBAC allow it names the binding (S3-F1)
}

// rbacAllowedBy starts the RBAC authorizer's status.reason for an allow; the binding follows.
const rbacAllowedBy = "RBAC: allowed by "

// Hints shared by every check that asks the cluster "may I?".
const (
	hintRequestFailed = probe.HintRetry
	hintEvaluation    = "the cluster's authorizer reported an error; check the authorization webhook or ask a cluster admin"
	hintNotAPIServer  = "check the server URL and any proxy in your kubeconfig: the answer did not come from a Kubernetes API server"
)

// evaluationError means the authorizer could not evaluate the question.
type evaluationError struct{ msg string }

func (e *evaluationError) Error() string { return e.msg }

// notAPIServerError means the answer cannot have come from a Kubernetes API server.
type notAPIServerError struct{ msg string }

func (e *notAPIServerError) Error() string { return e.msg }

// can asks the cluster question q. A failed request, an answer with no verdict (status.allowed),
// an answer that does not repeat the question, or an evaluationError on an answer that is not
// allowed is an error.
func can(ctx context.Context, c *cluster.Client, q question) (answer, error) {
	attrs := cluster.Attributes{Verb: q.verb, Group: q.group, Resource: q.resource, Subresource: q.subresource, Name: q.name, Namespace: q.namespace}
	ssar, err := c.SelfSubjectAccessReview(ctx, attrs)
	if errors.Is(err, cluster.ErrNoVerdict) {
		return answer{}, &notAPIServerError{fmt.Sprintf("the answer to \"may I %s?\" has no verdict (status.allowed); something other than the API server answered", q.action())}
	}
	if err != nil {
		return answer{}, fmt.Errorf("could not ask \"may I %s?\": %w", q.action(), err)
	}
	// The API server echoes the question. A 2xx body without that echo (a proxy, an empty object)
	// decodes to allowed=false and would read as a denial, so it is an error (D52). A proxy that
	// echoes the whole request passes this check and fails the verdict check above, because the
	// request carries no status (D64).
	if a := ssar.Spec.ResourceAttributes; a == nil || *a != attrs {
		return answer{}, &notAPIServerError{fmt.Sprintf("the answer to \"may I %s?\" does not repeat the question; something other than the API server answered", q.action())}
	}
	// An authorizer error does not undo an allow: the union authorizer reports both (D52).
	if ssar.Status.EvaluationError != "" && !ssar.Status.Allowed {
		return answer{}, &evaluationError{fmt.Sprintf("the authorizer could not evaluate \"may I %s?\": %s", q.action(), ssar.Status.EvaluationError)}
	}
	return answer{allowed: ssar.Status.Allowed, denied: ssar.Status.Denied, reason: ssar.Status.Reason}, nil
}

// hintFor picks the hint for an error can returned.
func hintFor(err error) string {
	if _, ok := errors.AsType[*evaluationError](err); ok {
		return hintEvaluation
	}
	if _, ok := errors.AsType[*notAPIServerError](err); ok {
		return hintNotAPIServer
	}
	return hintRequestFailed
}

// question is one "may I?" a check asks; an empty namespace asks at cluster scope, an empty name
// about every object of the resource. label names the action in reasons when verb and resource
// alone would not read well.
type question struct{ verb, group, resource, subresource, name, namespace, label string }

func (q question) action() string {
	if q.label != "" {
		return q.label
	}
	a := q.verb + " " + q.resource
	if q.subresource != "" {
		a += "/" + q.subresource
	}
	if q.namespace != "" {
		a += " in " + q.namespace
	}
	return a
}

// denials asks questions that must all be denied, in order. Each allowed answer makes a FAIL that
// names every grant so far, handed to prove at once, so the runner keeps it even if a later
// question never returns (D61). stop reports that the check is decided: res is then the FAIL
// (a proven grant beats a later failed question or the deadline), an ERROR for an answer it
// could not read, or nil when ctx ended first (the runner says so). Otherwise every answer was
// a denial and explicit lists the explicit ones. The FAIL's fix is remove, then why; when every
// grant names its binding, the reason and the fix name those instead (S3-F1).
func denials(ctx context.Context, c *cluster.Client, spec Spec, prove func(report.Result), questions []question, remove, why string) (explicit []string, res []report.Result, stop bool) {
	r := spec.Results[0]
	var allowed, bindings []string
	named := true // every allowed answer named its binding; a webhook or system:masters may name none
	var fail report.Result
	var failed error // the first question whose answer could not be read; an allowed answer still wins (D52)
	ended := false
	for _, q := range questions {
		ans, err := can(ctx, c, q)
		if err != nil {
			if ctx.Err() != nil {
				ended = true // a grant already proven is still reported below
				break
			}
			if failed == nil {
				failed = err
			}
			if _, ok := errors.AsType[*evaluationError](err); !ok {
				break // a failed request would repeat for every question, and client-go's retries could spend the whole --timeout
			}
			continue
		}
		if ans.allowed {
			allowed = append(allowed, q.action())
			binding, ok := strings.CutPrefix(ans.reason, rbacAllowedBy)
			named = named && ok && binding != ""
			if named && !slices.Contains(bindings, binding) {
				bindings = append(bindings, binding)
			}
			reason, fix := "RBAC allows "+strings.Join(allowed, ", "), remove
			if named {
				reason += "; granted by " + firstThree(bindings)
				fix = "remove or change " + firstThree(bindings)
			}
			if c.Identity.Mode == report.ModeSelf {
				fix = "ask a cluster admin to " + fix // S4-F1: the reader is often the tenant, who cannot change bindings
			}
			fail = spec.result(r, report.Fail, "RBAC", reason, fix+"; "+why)
			prove(fail)
		} else if ans.denied {
			explicit = append(explicit, q.action())
		}
	}
	switch {
	case len(allowed) > 0:
		return nil, []report.Result{fail}, true
	case ended:
		return nil, nil, true
	case failed != nil:
		return nil, []report.Result{spec.result(r, report.Error, "-", failed.Error(), hintFor(failed))}, true
	}
	return explicit, nil, false
}

// explicitDenies is the PASS-reason suffix naming the actions an authorizer denied explicitly.
func explicitDenies(explicit []string) string {
	if len(explicit) == 0 {
		return ""
	}
	return " (explicit deny: " + strings.Join(explicit, ", ") + ")"
}
