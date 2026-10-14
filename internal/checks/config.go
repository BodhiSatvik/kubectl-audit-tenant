package checks

import (
	"context"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// layerConfig is the deciding layer of a verdict read from stored objects (AT-06 to AT-09).
const layerConfig = "config"

// listFailed is the result of a check whose LIST failed. A 403 is a fact about the identity, so
// it is SKIP naming the permission (DESIGN.md section 6); nil when the run ended, so the runner
// says timeout or interrupted; any other error is ERROR.
func listFailed(ctx context.Context, c *cluster.Client, spec Spec, err error, resource string) []report.Result {
	switch {
	case apierrors.IsForbidden(err):
		return spec.all(report.Skip, "-", "cannot list "+resource+" in "+c.Namespace+" (forbidden)",
			"grant `list` on "+resource+" in "+c.Namespace+" to this identity, or run with --as <admin>")
	case ctx.Err() != nil:
		return nil
	}
	return spec.all(report.Error, "-", "could not list "+resource+": "+err.Error(), hintRequestFailed)
}

// byName returns a sorted copy of objects, so "the first by name" decides when several qualify
// and reasons list objects in a stable order. The cached list itself stays untouched.
func byName[T any, P interface {
	*T
	metav1.Object
}](objects []T) []T {
	return slices.SortedFunc(slices.Values(objects), func(a, b T) int { return strings.Compare(P(&a).GetName(), P(&b).GetName()) })
}
