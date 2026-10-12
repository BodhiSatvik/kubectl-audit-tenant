// Package safety makes the client read-only by construction (ADR 0001). An exact-path allowlist
// in front of the HTTP transport refuses every other request before it leaves the process.
package safety

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
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

// Transport allows, on the configured server only: GET of /version, of the target namespace and
// of six list paths inside it; POST of SelfSubjectAccessReview and SelfSubjectReview; POST of
// pods and services in the target namespace with dryRun=All. Paths must match exactly under the
// server's path prefix, so every subresource, every named object, every other namespace, every
// other host (a redirect would carry the credentials along) and every other verb is refused.
type Transport struct {
	Namespace string
	Base      *url.URL // scheme, host and path prefix of the API server (rest.DefaultServerUrlFor)
	Next      http.RoundTripper
	Recorder  *Recorder // nil records nothing
}

// Wrap returns the function rest.Config.WrapTransport expects.
func Wrap(namespace string, base *url.URL, rec *Recorder) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		return &Transport{Namespace: namespace, Base: base, Next: next, Recorder: rec}
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
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
			if readErr == nil && len(body) > maxAnswer {
				readErr = fmt.Errorf("the answer to POST %s is larger than %d bytes; an API server answer is a few KiB", req.URL.Path, maxAnswer)
			}
			if err := errors.Join(readErr, resp.Body.Close()); err != nil {
				t.Recorder.add(r)
				return nil, err
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
			r.Body = string(body)
			if strings.HasSuffix(req.URL.Path, selfSubjectReviews) {
				r.Body = withoutExtra(body)
			}
		}
	}
	t.Recorder.add(r)
	return resp, err
}

const (
	selfSubjectReviews = "/apis/authentication.k8s.io/v1/selfsubjectreviews"
	maxAnswer          = 1 << 20 // bytes; an SSAR or dry-run answer is a few KiB, a stream that never ends is not an answer
)

// withoutExtra drops status.userInfo.extra from a SelfSubjectReview answer before --debug can
// print it: identity providers put claims there (a cloud role ARN and session, a token ID).
// An answer it cannot read is not shown at all.
func withoutExtra(body []byte) string {
	const hidden = "(SelfSubjectReview answer not shown: not a JSON object)"
	var review map[string]any
	if json.Unmarshal(body, &review) != nil {
		return hidden
	}
	if status, ok := review["status"].(map[string]any); ok {
		if user, ok := status["userInfo"].(map[string]any); ok {
			delete(user, "extra")
		}
	}
	out, err := json.Marshal(review)
	if err != nil {
		return hidden
	}
	return string(out)
}

func (t *Transport) allowed(req *http.Request) bool {
	u := req.URL
	if t.Base == nil || u.Opaque != "" || u.Scheme != t.Base.Scheme || u.Host != t.Base.Host || u.Query().Has("watch") {
		return false
	}
	if len(validation.IsDNS1123Label(t.Namespace)) > 0 {
		return false // a namespace that is not one label would widen the paths below
	}
	path, ok := strings.CutPrefix(u.EscapedPath(), strings.TrimSuffix(t.Base.EscapedPath(), "/")) // both encoded
	if !ok {
		return false
	}
	core := "/api/v1/namespaces/" + t.Namespace
	apis := "/namespaces/" + t.Namespace
	switch req.Method {
	case http.MethodGet:
		switch path {
		case "/version", core, core + "/resourcequotas", core + "/limitranges",
			"/apis/networking.k8s.io/v1" + apis + "/networkpolicies",
			"/apis/apps/v1" + apis + "/deployments",
			"/apis/apps/v1" + apis + "/statefulsets",
			"/apis/autoscaling/v2" + apis + "/horizontalpodautoscalers":
			return true
		}
	case http.MethodPost:
		switch path {
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews",
			selfSubjectReviews:
			return true
		case core + "/pods", core + "/services":
			return slices.Contains(u.Query()["dryRun"], "All")
		}
	}
	return false
}
