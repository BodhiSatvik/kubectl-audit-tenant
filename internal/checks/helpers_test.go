package checks

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"sync"
	"testing"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
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
// without an answer gets HTTP 500, except AT-01's positive control and the "list pods" anchor,
// which are allowed.
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
	if !ok && a.Verb+" "+a.Resource == "list pods" {
		body, ok = allowed, true // the no-rights anchor (D80): every real tenant may list pods
	}
	if !ok {
		http.Error(w, "no canned answer for "+a.Verb+" "+a.Resource, http.StatusInternalServerError)
		return
	}
	spec, _ := json.Marshal(ssar.Spec)
	testutil.JSON(http.StatusCreated, `{"spec":`+string(spec)+`,"status":`+body+`}`)(w, r)
}

// connect builds a cluster.Client for tenant-a whose SSARs are answered by ssar.
func connect(t *testing.T, ssar http.Handler) *cluster.Client { return connectWith(t, ssar, nil) }

// connectWith is connect plus extra routes, such as the dry-run endpoints of AT-04 and AT-05.
func connectWith(t *testing.T, ssar http.Handler, extra map[string]http.HandlerFunc) *cluster.Client {
	t.Helper()
	routes := map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"team-a-dev"}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": ssar.ServeHTTP,
		"GET /api/v1/namespaces/tenant-a/limitranges":                 testutil.JSON(http.StatusOK, `{"items":[]}`),
	}
	maps.Copy(routes, extra)
	srv := testutil.NewServer(t, routes)
	c, err := cluster.Connect(t.Context(), &rest.Config{Host: srv.URL}, cluster.Options{Namespace: "tenant-a", OtherNamespace: "kube-system", As: "team-a-dev", Timeout: time.Minute})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}

// probeServer answers dry-run creates of pods and services. It names each object by the probe
// it is (control, AT-04.1 to AT-04.5, control-service, AT-05), answers from answers (accepted
// when a name has no answer), and remembers the names in the order they were sent.
type probeServer struct {
	mu      sync.Mutex
	answers map[string]http.HandlerFunc
	sent    []string
}

func (s *probeServer) routes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"POST /api/v1/namespaces/tenant-a/pods":     s.ServeHTTP,
		"POST /api/v1/namespaces/tenant-a/services": s.ServeHTTP,
	}
}

func (s *probeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.URL.Query().Get("dryRun") != "All" {
		http.Error(w, "not a dry run", http.StatusBadRequest)
		return
	}
	var obj struct {
		Kind string
		Spec struct {
			Type        string
			HostPID     bool
			HostNetwork bool
			Volumes     []corev1.Volume
			Containers  []corev1.Container
		}
	}
	_ = json.Unmarshal(body, &obj)
	name := "control"
	switch sp := obj.Spec; {
	case obj.Kind == "Service" && sp.Type == "NodePort":
		name = "AT-05"
	case obj.Kind == "Service":
		name = "control-service"
	case len(sp.Volumes) > 0:
		name = "AT-04.2"
	case sp.HostPID:
		name = "AT-04.3"
	case sp.HostNetwork:
		name = "AT-04.4"
	case sp.Containers[0].SecurityContext.Privileged != nil: // both privileged forms (D78)
		name = "AT-04.1"
	case *sp.Containers[0].SecurityContext.AllowPrivilegeEscalation:
		name = "AT-04.5"
	}
	s.mu.Lock()
	s.sent = append(s.sent, name)
	s.mu.Unlock()
	if h, ok := s.answers[name]; ok {
		h(w, r)
		return
	}
	// Accepted: answer as the API server does a dry run, with a generated name and no resourceVersion.
	var answer map[string]any
	_ = json.Unmarshal(body, &answer)
	if meta, ok := answer["metadata"].(map[string]any); ok {
		meta["name"] = "audit-tenant-probe-x7k2p"
	}
	out, _ := json.Marshal(answer)
	testutil.JSON(http.StatusCreated, string(out))(w, r)
}
