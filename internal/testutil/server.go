// Package testutil builds a real clientset against a canned HTTP server for tests that need the
// HTTP layer: the safety allowlist, deadlines, signals and startup errors. The fake clientset
// bypasses HTTP and takes no context, so it cannot test those.
package testutil

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/safety"
)

// Server is an httptest server with canned routes and a log of every request it received.
type Server struct {
	*httptest.Server
	mu   sync.Mutex
	hits []string
}

// NewServer serves routes keyed by method-aware ServeMux patterns ("GET /version") and answers
// 404 to anything else. Every request is logged, matched or not. The server closes with t.
func NewServer(t testing.TB, routes map[string]http.HandlerFunc) *Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// Hits returns the requests received so far as "METHOD /path", in order.
func (s *Server) Hits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.hits)
}

// Client returns a real clientset that reaches the server through the safety transport.
func (s *Server) Client(t testing.TB, namespace string, rec *safety.Recorder) *kubernetes.Clientset {
	t.Helper()
	base, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &rest.Config{Host: s.URL, QPS: 50, Burst: 300, WrapTransport: safety.Wrap(namespace, base, rec)}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("NewForConfig: %v", err)
	}
	return cs
}

// JSON answers with the status code and body.
func JSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// SSAR answers a SelfSubjectAccessReview like the API server: the question it received echoed
// back as spec, with status (for example `{"allowed":false}`).
func SSAR(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var review struct {
			Spec json.RawMessage `json:"spec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		JSON(http.StatusCreated, `{"spec":`+string(review.Spec)+`,"status":`+status+`}`)(w, r)
	}
}

// Status answers like the API server does when it refuses a request: a Status object with the
// reason client-go's apierrors helpers read.
func Status(code int32, reason metav1.StatusReason, message string) http.HandlerFunc {
	body, _ := json.Marshal(metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure, Message: message, Reason: reason, Code: code,
	})
	return JSON(int(code), string(body))
}
