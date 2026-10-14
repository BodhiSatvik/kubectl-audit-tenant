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
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/probe"
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

// ErrUnverified refuses a create after an earlier create answer that was not a verified dry run:
// the server may have stored that object, so any further create could be real too (D101, D103).
var ErrUnverified = errors.New("audit-tenant sent no further create: an earlier create answer could not be verified as a dry run")

// Transport allows, on the configured server only: GET of /version, of the target namespace and
// of six list paths inside it; POST of SelfSubjectAccessReview and SelfSubjectReview; POST of
// pods and services in the target namespace with dryRun=All. Paths must match exactly under the
// server's path prefix, so every subresource, every named object, every other namespace, every
// other host (a redirect would carry the credentials along) and every other verb is refused.
// A create is sent once: after the first create answer that is not a verified dry run, no create
// leaves the process (D103).
type Transport struct {
	Namespace  string
	Base       *url.URL // scheme, host and path prefix of the API server (rest.DefaultServerUrlFor)
	Next       http.RoundTripper
	Recorder   *Recorder    // nil records nothing
	Unverified *atomic.Bool // set by the first create answer that is not a verified dry run; one per run; nil refuses every create
}

// Wrap returns the function rest.Config.WrapTransport expects.
func Wrap(namespace string, base *url.URL, rec *Recorder, unverified *atomic.Bool) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		return &Transport{Namespace: namespace, Base: base, Next: next, Recorder: rec, Unverified: unverified}
	}
}

// RoundTrip refuses requests outside the allowlist and records the rest.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ok, create := t.allowed(req)
	if !ok {
		return nil, &RefusedError{Method: req.Method, Path: req.URL.Path}
	}
	if create && t.Unverified.Load() {
		return nil, ErrUnverified
	}
	start := time.Now()
	resp, err := t.Next.RoundTrip(req)
	r := Request{Method: req.Method, Path: req.URL.RequestURI(), Duration: time.Since(start)}
	verified := false
	if err == nil {
		r.Status = resp.StatusCode
		if create {
			resp.Header.Del("Retry-After") // client-go resends a POST answered 429 or 5xx with Retry-After, up to 10 times
		}
		// Every allowed POST is an SSAR, a SelfSubjectReview or a dry-run; --debug prints their answers.
		if req.Method == http.MethodPost {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
			if readErr == nil && len(body) > maxAnswer {
				readErr = fmt.Errorf("the answer to POST %s is larger than %d bytes; an API server answer is a few KiB", req.URL.Path, maxAnswer)
			}
			if err = errors.Join(readErr, resp.Body.Close()); err == nil {
				resp.Body = io.NopCloser(bytes.NewReader(body))
				r.Body = string(body)
				if strings.HasSuffix(req.URL.Path, selfSubjectReviews) {
					r.Body = withoutExtra(body)
				}
				verified = create && dryRunAnswer(resp.StatusCode, body)
			}
		}
	}
	if create && !verified {
		t.Unverified.Store(true) // the server may have stored this one; a failed send may have reached it
	}
	t.Recorder.add(r)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// dryRunAnswer reports whether a create answer shows that nothing was stored: a rejection (a 4xx
// other than 429, which also means "overloaded"), or an accepted dry run (a 2xx whose metadata
// carries a generated probe name and no resourceVersion). Anything else leaves the run unsure.
func dryRunAnswer(status int, body []byte) bool {
	switch {
	case status >= 400 && status < 500 && status != http.StatusTooManyRequests:
		return true
	case status < 200 || status > 299:
		return false
	}
	var answer map[string]any // a map, not a struct: keys must match exactly, as client-go decodes them
	if json.Unmarshal(body, &answer) != nil {
		return false
	}
	meta, _ := answer["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	rv, stored := meta["resourceVersion"]
	return strings.HasPrefix(name, probe.NamePrefix) && (!stored || rv == "")
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

// allowed reports whether req is on the allowlist, and whether it is a create (a pods or services POST).
func (t *Transport) allowed(req *http.Request) (ok, create bool) {
	u := req.URL
	if t.Base == nil || u.Opaque != "" || u.Scheme != t.Base.Scheme || u.Host != t.Base.Host || u.Query().Has("watch") {
		return false, false
	}
	if len(validation.IsDNS1123Label(t.Namespace)) > 0 {
		return false, false // a namespace that is not one label would widen the paths below
	}
	path, ok := strings.CutPrefix(u.EscapedPath(), strings.TrimSuffix(t.Base.EscapedPath(), "/")) // both encoded
	if !ok {
		return false, false
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
			return true, false
		}
	case http.MethodPost:
		switch path {
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews",
			selfSubjectReviews:
			return true, false
		case core + "/pods", core + "/services":
			return t.Unverified != nil && slices.Contains(u.Query()["dryRun"], "All"), true
		}
	}
	return false, false
}
