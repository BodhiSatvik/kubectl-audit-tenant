package cluster_test

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

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
