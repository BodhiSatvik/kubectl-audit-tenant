package checks

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/rest"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

// ignore is the prove callback of a test that reads only the returned results.
func ignore(report.Result) {}

const (
	denied  = `{"allowed":false}`
	allowed = `{"allowed":true}`
)

// ssarServer answers SelfSubjectAccessReviews from canned status bodies keyed by "verb resource",
// echoing the question as the API server does, and remembers every question. A question
// without an answer gets HTTP 500, except AT-01's positive control, which is allowed.
type ssarServer struct {
	mu      sync.Mutex
	answers map[string]string
	asked   []cluster.Attributes
}

func (s *ssarServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var ssar authzv1.SelfSubjectAccessReview
	if err := json.NewDecoder(r.Body).Decode(&ssar); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a := *ssar.Spec.ResourceAttributes
	s.mu.Lock()
	s.asked = append(s.asked, a)
	s.mu.Unlock()
	body, ok := s.answers[a.Verb+" "+a.Resource]
	if !ok && a.Verb+" "+a.Resource == "create selfsubjectaccessreviews" {
		body, ok = allowed, true // AT-01's positive control: a real API server always says yes
	}
	if !ok {
		http.Error(w, "no canned answer for "+a.Verb+" "+a.Resource, http.StatusInternalServerError)
		return
	}
	spec, _ := json.Marshal(ssar.Spec)
	testutil.JSON(http.StatusCreated, `{"spec":`+string(spec)+`,"status":`+body+`}`)(w, r)
}

// connect builds a cluster.Client for tenant-a whose SSARs are answered by ssar.
func connect(t *testing.T, ssar http.Handler) *cluster.Client {
	t.Helper()
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"team-a-dev"}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": ssar.ServeHTTP,
	})
	c, err := cluster.Connect(t.Context(), &rest.Config{Host: srv.URL}, cluster.Options{Namespace: "tenant-a", As: "team-a-dev", Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}
