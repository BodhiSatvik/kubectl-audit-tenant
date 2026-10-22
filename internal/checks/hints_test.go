package checks

import (
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

// TestHintsOnEveryFailSkipAndError is the hint floor of CONSTRAINTS.md over the whole registry: three
// scripted clusters make every result FAIL, then SKIP, then ERROR, and each result must carry a hint.
// A new check joins the test by joining the Registry, and must reach all three verdicts here.
func TestHintsOnEveryFailSkipAndError(t *testing.T) {
	lists := func(h http.HandlerFunc) map[string]http.HandlerFunc {
		return map[string]http.HandlerFunc{
			"GET /api/v1/namespaces/tenant-a/resourcequotas":                        h,
			"GET /api/v1/namespaces/tenant-a/limitranges":                           h,
			"GET /apis/networking.k8s.io/v1/namespaces/tenant-a/networkpolicies":    h,
			"GET /apis/apps/v1/namespaces/tenant-a/deployments":                     h,
			"GET /apis/apps/v1/namespaces/tenant-a/statefulsets":                    h,
			"GET /apis/autoscaling/v2/namespaces/tenant-a/horizontalpodautoscalers": h,
		}
	}
	forbidden := testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "forbidden")
	open := lists(testutil.JSON(http.StatusOK, `{"items":[]}`))
	maps.Copy(open, (&probeServer{}).routes()) // every probe accepted
	closed := lists(forbidden)
	closed["GET /api/v1/namespaces/tenant-a"] = forbidden

	for _, tc := range []struct {
		name   string
		want   report.Verdict
		ssar   *ssarServer
		routes map[string]http.HandlerFunc
		after  func(*cluster.Client)
	}{
		{"every grant, every probe accepted, nothing configured", report.Fail, &ssarServer{otherwise: allowed}, open, nil},
		{"no rights, nothing readable", report.Skip, &ssarServer{answers: map[string]string{"list pods": denied}, otherwise: denied}, closed, nil},
		{"answers without a verdict, lists failing", report.Error, &ssarServer{otherwise: `{}`},
			lists(testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcd timeout")),
			func(c *cluster.Client) {
				// Startup ends the run on any namespace error but a 403; AT-09 still answers one with ERROR.
				c.NamespaceObject, c.NamespaceErr = nil, apierrors.NewInternalError(errors.New("etcd timeout"))
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectWith(t, tc.ssar, tc.routes)
			if tc.after != nil {
				tc.after(c)
			}
			got := Run(t.Context(), c, Registry, time.Minute)
			if len(got) != len(ResultIDs(Registry)) {
				t.Fatalf("%d results, want one per result ID of the registry", len(got))
			}
			for _, r := range got {
				if r.Verdict != tc.want || r.Hint == "" {
					t.Errorf("%s: %s with hint %q, want %s with a hint; reason: %s", r.ID, r.Verdict, r.Hint, tc.want, r.Reason)
				}
				// The runner's own ERRORs (a crash, a missing result, the deadline) carry hints too; they must not stand in for the check's.
				if r.Hint == hintBug || strings.HasPrefix(r.Reason, "not run: timeout") || r.Reason == "interrupted" { // a check's own "not run: an earlier create ..." ERROR is fine
					t.Errorf("%s: the runner filled this result (%s), not the check", r.ID, r.Reason)
				}
			}
		})
	}
}
