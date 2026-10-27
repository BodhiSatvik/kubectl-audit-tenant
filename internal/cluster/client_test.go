package cluster_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

const ns = "tenant-a"

var opts = cluster.Options{Namespace: ns, Context: "kind-dev", User: "kind-dev", As: "team-a-dev", AsGroups: []string{"team-a"}, Timeout: 30 * time.Second}

// happy returns the routes of a cluster that answers every startup request well.
func happy() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"team-a-dev","groups":["team-a","system:authenticated"]}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a","labels":{"pod-security.kubernetes.io/enforce":"restricted"}}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.JSON(http.StatusCreated, `{"status":{"allowed":false}}`),
		"GET /api/v1/namespaces/tenant-a/resourcequotas":              testutil.JSON(http.StatusOK, `{"items":[{"metadata":{"name":"q"}}]}`),
		"GET /api/v1/namespaces/tenant-a/limitranges":                 testutil.JSON(http.StatusOK, `{"items":[{"metadata":{"name":"l"}}]}`),
	}
}

func connect(t *testing.T, routes map[string]http.HandlerFunc, o cluster.Options) (*testutil.Server, *cluster.Client, error) {
	t.Helper()
	srv := testutil.NewServer(t, routes)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	c, err := cluster.Connect(ctx, &rest.Config{Host: srv.URL}, o)
	return srv, c, err
}

func TestServerLeavesOutQueryAndFragment(t *testing.T) {
	srv := testutil.NewServer(t, happy())
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.RawQuery, u.Fragment = "token=s3cret", "s3cret" // client-go never sends these; -o json must not print them either
	c, err := cluster.Connect(t.Context(), &rest.Config{Host: u.String()}, opts)
	if err != nil {
		t.Fatalf("Connect() error %v", err)
	}
	if c.Server != srv.URL {
		t.Errorf("Server = %q, want %s", c.Server, srv.URL)
	}
}

func TestConnectRefusesUserinfoInTheServerURL(t *testing.T) {
	// D169: Go hides only a password in a failed request's URL, so a token before the @ would
	// reach the reasons of -o json and -o policyreport. The run stops before any request.
	for name, user := range map[string]*url.Userinfo{"a token": url.User("t0ken"), "a user and password": url.UserPassword("admin", "s3cret")} {
		t.Run(name, func(t *testing.T) {
			srv := testutil.NewServer(t, happy())
			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			u.User = user
			_, err = cluster.Connect(t.Context(), &rest.Config{Host: u.String()}, opts)
			if err == nil || !strings.Contains(err.Error(), "user info") || strings.Contains(err.Error(), u.Host) || strings.Contains(err.Error(), "t0ken") || strings.Contains(err.Error(), "admin") {
				t.Errorf("Connect() error %v; want a refusal that names user info and not the URL", err)
			}
			if hits := srv.Hits(); len(hits) != 0 {
				t.Errorf("requests %v; want none", hits)
			}
		})
	}
}

func TestConnectKeepsUserinfoOutOfAServerURLThatDoesNotParse(t *testing.T) {
	// D179: client-go's parse error quotes the whole URL, so it would print the token D169 refuses.
	_, err := cluster.Connect(t.Context(), &rest.Config{Host: "https://t0ken@127.0.0.1:badport"}, opts)
	if err == nil || !strings.Contains(err.Error(), "user info") || strings.Contains(err.Error(), "t0ken") {
		t.Errorf("Connect() error %v; want the user-info refusal without the token", err)
	}
}

func TestConnectResolvesIdentityVersionNamespaceAndCaches(t *testing.T) {
	srv, c, err := connect(t, happy(), opts)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	id := c.Identity
	if id.Mode != "impersonation" || id.Username != "team-a-dev" || !id.Verified || !slices.Equal(id.Groups, []string{"team-a", "system:authenticated"}) {
		t.Errorf("Identity = %+v, want verified impersonation of team-a-dev", id)
	}
	if c.Version != "v1.37.0" || c.Server != srv.URL {
		t.Errorf("Version, Server = %q, %q; want v1.37.0, %s", c.Version, c.Server, srv.URL)
	}
	if c.NamespaceObject == nil || c.NamespaceObject.Labels["pod-security.kubernetes.io/enforce"] != "restricted" || c.NamespaceErr != nil {
		t.Errorf("namespace = %v, %v; want the cached object", c.NamespaceObject, c.NamespaceErr)
	}
	for range 2 {
		if q, err := c.Quotas(); err != nil || len(q) != 1 || q[0].Name != "q" {
			t.Errorf("Quotas() = %v, %v", q, err)
		}
		if l, err := c.LimitRanges(); err != nil || len(l) != 1 || l[0].Name != "l" {
			t.Errorf("LimitRanges() = %v, %v", l, err)
		}
	}
	want := []string{
		"GET /version",
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews",
		"GET /api/v1/namespaces/tenant-a",
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews",
		"GET /api/v1/namespaces/tenant-a/resourcequotas",
		"GET /api/v1/namespaces/tenant-a/limitranges",
	}
	if got := srv.Hits(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v (one list per kind per run)", got, want)
	}
	if n := len(c.Recorder.Requests()); n != len(want) {
		t.Errorf("recorder has %d requests, want %d", n, len(want))
	}
}

func TestConnectReusesTheStartupSSARForAT01(t *testing.T) {
	srv, c, err := connect(t, happy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	before := len(srv.Hits())
	listNodes := cluster.Attributes{Verb: "list", Resource: "nodes"}
	if r, err := c.SelfSubjectAccessReview(t.Context(), listNodes); err != nil || r.Status.Allowed {
		t.Errorf("SSAR(list nodes) = %v, %v; want the cached denial", r, err)
	}
	if len(srv.Hits()) != before {
		t.Error("the first AT-01 SSAR should reuse the startup answer, not send again")
	}
	if _, err := c.SelfSubjectAccessReview(t.Context(), listNodes); err != nil {
		t.Fatal(err)
	}
	if len(srv.Hits()) != before+1 {
		t.Error("a second identical SSAR is a real request")
	}
}

func TestConnectInSelfModeWithUnverifiedIdentity(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"a 403":                      testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "selfsubjectreviews is forbidden"),
		"an answer with no username": testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			routes := happy()
			routes["POST /apis/authentication.k8s.io/v1/selfsubjectreviews"] = h
			self := opts
			self.As, self.AsGroups = "", nil
			_, c, err := connect(t, routes, self)
			if err != nil {
				t.Fatalf("Connect() error = %v; SelfSubjectReview must not stop the run", err)
			}
			if c.Identity.Mode != "self" || c.Identity.Username != "kind-dev" || c.Identity.Verified {
				t.Errorf("Identity = %+v, want unverified self identity from the kubeconfig user", c.Identity)
			}
		})
	}
}

func TestConnectKeepsAForbiddenNamespaceForAT09(t *testing.T) {
	routes := happy()
	routes["GET /api/v1/namespaces/tenant-a"] = testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, `namespaces "tenant-a" is forbidden`)
	_, c, err := connect(t, routes, opts)
	if err != nil {
		t.Fatalf("Connect() error = %v; a 403 on the namespace must not stop the run", err)
	}
	if c.NamespaceObject != nil || c.NamespaceErr == nil {
		t.Errorf("namespace = %v, %v; want nil object and the 403 kept", c.NamespaceObject, c.NamespaceErr)
	}
}

func TestConnectExitReasons(t *testing.T) {
	impersonateMsg := `users "team-a-dev" is forbidden: User "kind-dev" cannot impersonate resource "users" in API group "" at the cluster scope`
	tests := []struct {
		name  string
		route string
		h     http.HandlerFunc
		want  string
	}{
		{"401 on version", "GET /version", testutil.Status(http.StatusUnauthorized, metav1.StatusReasonUnauthorized, "Unauthorized"),
			"unauthorized: the cluster rejected your credentials (expired token?); log in again and retry"},
		{"cannot impersonate", "GET /version", testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, impersonateMsg),
			"kind-dev may not impersonate team-a-dev: ask a cluster admin for `impersonate` on users, groups and serviceaccounts, or run without --as"},
		{"namespace 404", "GET /api/v1/namespaces/tenant-a", testutil.Status(http.StatusNotFound, metav1.StatusReasonNotFound, `namespaces "tenant-a" not found`),
			"namespace tenant-a not found in context kind-dev; check -n or --context"},
		{"namespace 500: the namespace may not exist, so the run does not go on", "GET /api/v1/namespaces/tenant-a", testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcd unavailable"),
			"could not read namespace tenant-a: etcd unavailable"},
		{"first SSAR 403", "POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews", testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "selfsubjectaccessreviews.authorization.k8s.io is forbidden"),
			"this identity lacks `create selfsubjectaccessreviews`, which every authenticated user normally has"},
		{"/version answers HTML (a login proxy)", "GET /version", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>sign in</html>") },
			"API server unreachable: /version returned no version"},
		{"403 on /version", "GET /version", testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "forbidden"),
			"the API server refused /version for this identity"},
		{"first SSAR 500", "POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews", testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcd unavailable"),
			"the first SelfSubjectAccessReview failed"},
		{"401 late in startup", "GET /api/v1/namespaces/tenant-a", testutil.Status(http.StatusUnauthorized, metav1.StatusReasonUnauthorized, "Unauthorized"),
			"unauthorized: the cluster rejected your credentials"},
		{"401 on SelfSubjectReview", "POST /apis/authentication.k8s.io/v1/selfsubjectreviews", testutil.Status(http.StatusUnauthorized, metav1.StatusReasonUnauthorized, "Unauthorized"),
			"unauthorized: the cluster rejected your credentials"},
		{"401 on the first SSAR", "POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews", testutil.Status(http.StatusUnauthorized, metav1.StatusReasonUnauthorized, "Unauthorized"),
			"unauthorized: the cluster rejected your credentials"},
		{"cannot impersonate without a user name", "GET /version", testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "cannot impersonate"),
			"your identity may not impersonate team-a-dev"},
		{"/version answers 500 in plain text (Teleport refusing an impersonation, D196)", "GET /version", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `impersonation request has been denied, user header "team-a-dev" is not allowed in roles`)
		}, `the API server, or a proxy in front of it, answered /version with HTTP 500: an error on the server ("impersonation request has been denied`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes := happy()
			routes[tt.route] = tt.h
			_, _, err := connect(t, routes, opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Connect() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConnectUnreachableServer(t *testing.T) {
	srv := testutil.NewServer(t, nil)
	srv.Close()
	_, err := cluster.Connect(t.Context(), &rest.Config{Host: srv.URL}, opts)
	if err == nil || !strings.HasPrefix(err.Error(), "API server unreachable: ") {
		t.Errorf("Connect() error = %v, want an 'API server unreachable' reason", err)
	}
}

func TestConnectTimesOutDuringStartup(t *testing.T) {
	wait := func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	sleeper := &clientcmdapi.ExecConfig{Command: "sleep", Args: []string{"1"}, APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode}
	for _, tt := range []struct {
		name string
		cfg  func(host string) *rest.Config
	}{
		{"a server that never answers", func(host string) *rest.Config { return &rest.Config{Host: host} }},
		{"a credential plugin that sleeps", func(host string) *rest.Config { return &rest.Config{Host: host, ExecProvider: sleeper} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := testutil.NewServer(t, map[string]http.HandlerFunc{"GET /version": wait})
			o := opts
			o.Timeout = 200 * time.Millisecond
			ctx, cancel := context.WithTimeout(t.Context(), o.Timeout)
			defer cancel()
			_, err := cluster.Connect(ctx, tt.cfg(srv.URL), o)
			want := "timed out during startup: the credential plugin or the API server did not answer within 200ms"
			if err == nil || err.Error() != want {
				t.Errorf("Connect() error = %v, want %q", err, want)
			}
		})
	}
}

func TestConnectInterruptedDuringStartup(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{"GET /version": func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := cluster.Connect(ctx, &rest.Config{Host: srv.URL}, opts)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("Connect() error = %v, want 'interrupted'", err)
	}
}

func TestSSARWithoutAVerdictIsNotADenial(t *testing.T) {
	// A proxy that reflects the request: the question comes back, status.allowed does not.
	routes := happy()
	routes["POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews"] = testutil.JSON(http.StatusCreated, `{"spec":{"resourceAttributes":{"verb":"list","resource":"nodes"}},"status":{}}`)
	_, c, err := connect(t, routes, opts)
	if err != nil {
		t.Fatalf("Connect() error = %v; an answer without a verdict is AT-01's ERROR, not a startup failure", err)
	}
	for _, source := range []string{"the first question", "the same question again"} {
		if r, err := c.SelfSubjectAccessReview(t.Context(), cluster.FirstQuestion); !errors.Is(err, cluster.ErrNoVerdict) {
			t.Errorf("%s: SSAR = %v, %v; want ErrNoVerdict, never a denial", source, r, err)
		}
	}
}

func TestSSARAnEchoedRequestHasNoVerdict(t *testing.T) {
	// D64: a proxy that sends the request back. The request carries no status, so the echo has
	// no verdict; with a status in the request, the echo would read as "allowed: false".
	routes := happy()
	routes["POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews"] = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		testutil.JSON(http.StatusCreated, string(body))(w, r)
	}
	_, c, err := connect(t, routes, opts)
	if err != nil {
		t.Fatalf("Connect() error = %v; an echoed answer is the checks' ERROR, not a startup failure", err)
	}
	if r, err := c.SelfSubjectAccessReview(t.Context(), cluster.Attributes{Verb: "get", Resource: "secrets", Namespace: "kube-system"}); !errors.Is(err, cluster.ErrNoVerdict) {
		t.Errorf("SSAR behind an echoing proxy = %v, %v; want ErrNoVerdict, never a denial", r, err)
	}
}

// Added by the /ship pre-landing review (Phase 2).
// Value: protects=an answer whose verdict key differs in letter case, or is null, is ErrNoVerdict, never a denial (D93); fails_when=the presence check matches keys
// case-insensitively again; why_new=the tests above cover a missing status only; seam=none; no_claim=how a real proxy rewrites keys
func TestSSARAVerdictTheAPIDecoderCannotReadIsNotADenial(t *testing.T) {
	for _, status := range []string{`"status":{"Allowed":true}`, `"Status":{"allowed":true}`, `"status":{"allowed":null}`} {
		routes := happy()
		routes["POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews"] = testutil.JSON(http.StatusCreated, `{"spec":{"resourceAttributes":{"verb":"list","resource":"nodes"}},`+status+`}`)
		_, c, err := connect(t, routes, opts)
		if err != nil {
			t.Fatalf("%s: Connect() error = %v", status, err)
		}
		if r, err := c.SelfSubjectAccessReview(t.Context(), cluster.FirstQuestion); !errors.Is(err, cluster.ErrNoVerdict) {
			t.Errorf("%s: SSAR = %v, %v; want ErrNoVerdict, never a denial", status, r, err)
		}
	}
}

// Added by the /ship pre-landing review (Phase 2).
// Value: protects=API Warning headers (a PSA warn label) never reach stderr, which carries only --debug output (D90); fails_when=client-go's default
// warning handler is back; why_new=CLI tests capture streams.ErrOut, not klog; seam=none; no_claim=what --debug prints
func TestAPIWarningsNeverReachStderr(t *testing.T) {
	var logged bytes.Buffer
	klog.LogToStderr(false)
	klog.SetOutput(&logged)
	t.Cleanup(func() { klog.LogToStderr(true) })
	routes := happy()
	ssar := routes["POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews"]
	routes["POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Warning", `299 - "would violate PodSecurity \"restricted:latest\""`)
		ssar(w, r)
	}
	if _, _, err := connect(t, routes, opts); err != nil {
		t.Fatal(err)
	}
	klog.Flush()
	if logged.Len() != 0 {
		t.Errorf("an API warning was logged to stderr: %q", logged.String())
	}
}

func TestSSARIsSentAsJSON(t *testing.T) {
	// The body is built by hand (D64); without the header a proxy that enforces it answers 415.
	routes := happy()
	routes["POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews"] = func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			http.Error(w, "Content-Type "+ct, http.StatusUnsupportedMediaType)
			return
		}
		testutil.JSON(http.StatusCreated, `{"spec":{"resourceAttributes":{"verb":"list","resource":"nodes"}},"status":{"allowed":false}}`)(w, r)
	}
	if _, _, err := connect(t, routes, opts); err != nil {
		t.Errorf("Connect() error = %v; the SSAR must carry Content-Type: application/json", err)
	}
}

func TestSSARTheNoRightsAnchorIsSentOncePerRun(t *testing.T) {
	// D80: up to five checks ask the anchor; one request answers them all.
	srv, c, err := connect(t, happy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	listPods := cluster.Attributes{Verb: "list", Resource: "pods", Namespace: ns}
	before := len(srv.Hits())
	for range 3 {
		if _, err := c.SelfSubjectAccessReview(t.Context(), listPods); err != nil {
			t.Fatal(err)
		}
	}
	if sent := len(srv.Hits()) - before; sent != 1 {
		t.Errorf("sent %d SSARs for one question, want 1", sent)
	}
}
