package checks

import (
	"context"
	"errors"
	"fmt"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
)

// answer is one SelfSubjectAccessReview read as the API defines it (D10): allowed is the
// server's word; denied is set only by an authorizer that answered with an explicit deny.
type answer struct {
	allowed bool
	denied  bool
}

// Hints shared by every check that asks the cluster "may I?".
const (
	hintRequestFailed = "re-run; if it repeats, run with --debug and report the server message"
	hintEvaluation    = "the cluster's authorizer reported an error; check the authorization webhook or ask a cluster admin"
)

// evaluationError means the authorizer could not evaluate the question.
type evaluationError struct{ msg string }

func (e *evaluationError) Error() string { return e.msg }

// can asks whether the identity may do verb on resource in group, in namespace (empty for
// cluster scope). A failed request, an answer that does not repeat the question, or an
// evaluationError on an answer that is not allowed is an error.
func can(ctx context.Context, c *cluster.Client, verb, group, resource, namespace string) (answer, error) {
	attrs := cluster.Attributes{Verb: verb, Group: group, Resource: resource, Namespace: namespace}
	ssar, err := c.SelfSubjectAccessReview(ctx, attrs)
	if err != nil {
		return answer{}, fmt.Errorf("could not ask \"may I %s %s?\": %w", verb, resource, err)
	}
	// The API server echoes the question. Any other 2xx body (a proxy, an empty object) decodes
	// to allowed=false and would read as a denial, so it is an error, never a PASS (D52).
	if a := ssar.Spec.ResourceAttributes; a == nil || *a != attrs {
		return answer{}, fmt.Errorf("the answer to \"may I %s %s?\" does not repeat the question; something other than the API server answered", verb, resource)
	}
	// An authorizer error does not undo an allow: the union authorizer reports both (D52).
	if ssar.Status.EvaluationError != "" && !ssar.Status.Allowed {
		return answer{}, &evaluationError{fmt.Sprintf("the authorizer could not evaluate \"may I %s %s?\": %s", verb, resource, ssar.Status.EvaluationError)}
	}
	return answer{allowed: ssar.Status.Allowed, denied: ssar.Status.Denied}, nil
}

// hintFor picks the hint for an error can returned.
func hintFor(err error) string {
	if _, ok := errors.AsType[*evaluationError](err); ok {
		return hintEvaluation
	}
	return hintRequestFailed
}
