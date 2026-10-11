// Package safety makes the client read-only by construction (ADR 0001). An exact-path allowlist
// in front of the HTTP transport refuses every other request before it leaves the process.
package safety

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// RefusedError is returned for a request outside the allowlist. It means a bug in audit-tenant,
// never a problem with the cluster.
type RefusedError struct {
	Method string
	Path   string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("audit-tenant refused to send %s %s: not in its read-only allowlist (a bug in the tool; please report it)", e.Method, e.Path)
}

// Transport allows: GET of /version, of the target namespace and of six list paths inside it;
// POST of SelfSubjectAccessReview and SelfSubjectReview; POST of pods and services in the target
// namespace with dryRun=All. Paths must match exactly, so every subresource, every named object,
// every other namespace and every other verb is refused.
type Transport struct {
	Namespace string
	Next      http.RoundTripper
	Recorder  *Recorder // nil records nothing
}

// Wrap returns the function rest.Config.WrapTransport expects.
func Wrap(namespace string, rec *Recorder) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		return &Transport{Namespace: namespace, Next: next, Recorder: rec}
	}
}

// RoundTrip refuses requests outside the allowlist and records the rest.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.allowed(req) {
		return nil, &RefusedError{Method: req.Method, Path: req.URL.Path}
	}
	start := time.Now()
	resp, err := t.Next.RoundTrip(req)
	r := Request{Method: req.Method, Path: req.URL.RequestURI(), Duration: time.Since(start)}
	if err == nil {
		r.Status = resp.StatusCode
		// Every allowed POST is an SSAR, a SelfSubjectReview or a dry-run; --debug prints their answers.
		if req.Method == http.MethodPost {
			body, readErr := io.ReadAll(resp.Body)
			if err := errors.Join(readErr, resp.Body.Close()); err != nil {
				t.Recorder.add(r)
				return nil, err
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
			r.Body = string(body)
		}
	}
	t.Recorder.add(r)
	return resp, err
}

func (t *Transport) allowed(req *http.Request) bool {
	if t.Namespace == "" {
		return false // no target namespace, no requests: "/api/v1/namespaces/" would list them all
	}
	core := "/api/v1/namespaces/" + t.Namespace
	apis := "/namespaces/" + t.Namespace
	switch req.Method {
	case http.MethodGet:
		switch req.URL.Path {
		case "/version", core, core + "/resourcequotas", core + "/limitranges",
			"/apis/networking.k8s.io/v1" + apis + "/networkpolicies",
			"/apis/apps/v1" + apis + "/deployments",
			"/apis/apps/v1" + apis + "/statefulsets",
			"/apis/autoscaling/v2" + apis + "/horizontalpodautoscalers":
			return true
		}
	case http.MethodPost:
		switch req.URL.Path {
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews",
			"/apis/authentication.k8s.io/v1/selfsubjectreviews":
			return true
		case core + "/pods", core + "/services":
			return slices.Contains(req.URL.Query()["dryRun"], "All")
		}
	}
	return false
}
