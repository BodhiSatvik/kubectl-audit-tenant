package safety_test

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/checks"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/safety"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

const ns = "tenant-a"

// TestSafety is the safety invariant from CONSTRAINTS.md: a real clientset drives the transport
// against an httptest server. Every allowlisted request must pass; every deny-listed request
// must be refused before it reaches the server.
func TestSafety(t *testing.T) {
	ok := testutil.JSON(http.StatusOK, `{"items":[]}`)
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /version":                                                          testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"GET /api/v1/namespaces/tenant-a":                                       testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"GET /api/v1/namespaces/tenant-a/resourcequotas":                        ok,
		"GET /api/v1/namespaces/tenant-a/limitranges":                           ok,
		"GET /apis/networking.k8s.io/v1/namespaces/tenant-a/networkpolicies":    ok,
		"GET /apis/apps/v1/namespaces/tenant-a/deployments":                     ok,
		"GET /apis/apps/v1/namespaces/tenant-a/statefulsets":                    ok,
		"GET /apis/autoscaling/v2/namespaces/tenant-a/horizontalpodautoscalers": ok,
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews":           testutil.JSON(http.StatusCreated, `{"status":{"allowed":false}}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":                testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"tenant-a-dev"}}}`),
		"POST /api/v1/namespaces/tenant-a/pods":                                 testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"}}`), // a dry-run answer: generated name, no resourceVersion
		"POST /api/v1/namespaces/tenant-a/services":                             testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"}}`), // a dry-run answer: generated name, no resourceVersion
	})
	rec := &safety.Recorder{}
	cs := srv.Client(t, ns, rec)
	ctx := t.Context()
	dryRun := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
	list := metav1.ListOptions{}

	t.Run("allowlist passes every request a check makes", func(t *testing.T) {
		calls := []struct {
			want string
			call func() error
		}{
			{"GET /version", func() error { _, err := cs.Discovery().ServerVersion(); return err }},
			{"GET /api/v1/namespaces/tenant-a", func() error {
				_, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
				return err
			}},
			{"GET /api/v1/namespaces/tenant-a/resourcequotas", func() error { _, err := cs.CoreV1().ResourceQuotas(ns).List(ctx, list); return err }},
			{"GET /api/v1/namespaces/tenant-a/limitranges", func() error { _, err := cs.CoreV1().LimitRanges(ns).List(ctx, list); return err }},
			{"GET /apis/networking.k8s.io/v1/namespaces/tenant-a/networkpolicies", func() error {
				_, err := cs.NetworkingV1().NetworkPolicies(ns).List(ctx, list)
				return err
			}},
			{"GET /apis/apps/v1/namespaces/tenant-a/deployments", func() error { _, err := cs.AppsV1().Deployments(ns).List(ctx, list); return err }},
			{"GET /apis/apps/v1/namespaces/tenant-a/statefulsets", func() error { _, err := cs.AppsV1().StatefulSets(ns).List(ctx, list); return err }},
			{"GET /apis/autoscaling/v2/namespaces/tenant-a/horizontalpodautoscalers", func() error {
				_, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, list)
				return err
			}},
			{"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews", func() error {
				_, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authzv1.SelfSubjectAccessReview{}, metav1.CreateOptions{})
				return err
			}},
			{"POST /apis/authentication.k8s.io/v1/selfsubjectreviews", func() error {
				_, err := cs.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				return err
			}},
			{"POST /api/v1/namespaces/tenant-a/pods", func() error { _, err := cs.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{}, dryRun); return err }},
			{"POST /api/v1/namespaces/tenant-a/services", func() error {
				_, err := cs.CoreV1().Services(ns).Create(ctx, &corev1.Service{}, dryRun)
				return err
			}},
		}
		var want []string
		for _, c := range calls {
			if err := c.call(); err != nil {
				t.Errorf("%s: %v", c.want, err)
			}
			want = append(want, c.want)
		}
		if got := srv.Hits(); !slices.Equal(got, want) {
			t.Errorf("server saw %v, want %v", got, want)
		}
		reqs := rec.Requests()
		if len(reqs) != len(calls) {
			t.Fatalf("recorder has %d requests, want %d", len(reqs), len(calls))
		}
		for _, r := range reqs {
			if r.Status < 200 || r.Status > 299 || r.Duration <= 0 {
				t.Errorf("%s %s: status %d, duration %v", r.Method, r.Path, r.Status, r.Duration)
			}
			if (r.Body != "") != (r.Method == http.MethodPost) {
				t.Errorf("%s %s: body recorded only for POST; got %q", r.Method, r.Path, r.Body)
			}
		}
		if p := reqs[len(reqs)-1].Path; !strings.Contains(p, "dryRun=All") {
			t.Errorf("recorded path %q should keep the query", p)
		}
	})

	t.Run("deny-list is refused before any HTTP call", func(t *testing.T) {
		before := srv.Hits()
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
		calls := []struct {
			name string
			call func() error
		}{
			{"pod exec GET", func() error {
				return cs.CoreV1().RESTClient().Get().Namespace(ns).Resource("pods").Name("x").SubResource("exec").Do(ctx).Error()
			}},
			{"secrets GET", func() error { _, err := cs.CoreV1().Secrets(ns).Get(ctx, "db", metav1.GetOptions{}); return err }},
			{"secrets LIST", func() error { _, err := cs.CoreV1().Secrets(ns).List(ctx, list); return err }},
			{"secrets LIST in the other namespace", func() error { _, err := cs.CoreV1().Secrets("kube-system").List(ctx, list); return err }},
			{"PATCH", func() error {
				_, err := cs.CoreV1().Pods(ns).Patch(ctx, "x", types.MergePatchType, []byte(`{}`), metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}})
				return err
			}},
			{"PUT", func() error { _, err := cs.CoreV1().Pods(ns).Update(ctx, pod, metav1.UpdateOptions{}); return err }},
			{"DELETE", func() error { return cs.CoreV1().Pods(ns).Delete(ctx, "x", metav1.DeleteOptions{}) }},
			{"POST without dryRun=All", func() error { _, err := cs.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); return err }},
			{"list in another namespace", func() error { _, err := cs.CoreV1().ResourceQuotas("kube-system").List(ctx, list); return err }},
			{"namespace subresource", func() error {
				return cs.CoreV1().RESTClient().Get().Resource("namespaces").Name(ns).SubResource("finalize").Do(ctx).Error()
			}},
			{"cluster-scoped LIST", func() error { _, err := cs.CoreV1().Nodes().List(ctx, list); return err }},
			{"pod GET by name", func() error { _, err := cs.CoreV1().Pods(ns).Get(ctx, "x", metav1.GetOptions{}); return err }},
			{"pod exec POST with dryRun=All", func() error {
				return cs.CoreV1().RESTClient().Post().Namespace(ns).Resource("pods").Name("x").SubResource("exec").Param("dryRun", "All").Do(ctx).Error()
			}},
			{"eviction POST with dryRun=All", func() error {
				return cs.CoreV1().RESTClient().Post().Namespace(ns).Resource("pods").Name("x").SubResource("eviction").Param("dryRun", "All").Do(ctx).Error()
			}},
			{"create namespace with dryRun=All", func() error { _, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{}, dryRun); return err }},
			{"services POST with dryRun=None", func() error {
				_, err := cs.CoreV1().Services(ns).Create(ctx, &corev1.Service{}, metav1.CreateOptions{DryRun: []string{"None"}})
				return err
			}},
			{"watch on an allowed list path", func() error {
				w, err := cs.CoreV1().ResourceQuotas(ns).Watch(ctx, list)
				if err == nil {
					w.Stop()
				}
				return err
			}},
		}
		for _, c := range calls {
			err := c.call()
			if _, ok := errors.AsType[*safety.RefusedError](err); !ok {
				t.Errorf("%s: error = %v, want a RefusedError", c.name, err)
			}
		}
		if got := srv.Hits(); !slices.Equal(got, before) {
			t.Errorf("a denied request reached the server: %v", got[len(before):])
		}
	})
}

// TestSafetyReplaysEveryRegisteredCheck runs the whole registry through the transport, as a tenant
// that may create pods and Services (the probe paths) and as one that may not (the RBAC-only
// paths): no check may meet a refusal (it would be an ERROR), every request that reached the server
// is in the trace, none is a write or touches secrets, and together the two runs use every
// allowlisted path.
func TestSafetyReplaysEveryRegisteredCheck(t *testing.T) {
	quota := testutil.JSON(http.StatusOK, `{"items":[{"metadata":{"name":"q"},"spec":{"hard":{"pods":"10"}}}]}`)                                     // AT-10 then lists the workloads
	deployment := testutil.JSON(http.StatusOK, `{"items":[{"metadata":{"name":"api"},"spec":{"template":{"spec":{"containers":[{"name":"c"}]}}}}]}`) // and then the HPAs
	dryRunAnswer := testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"}}`)                                            // a generated name, no resourceVersion
	used := map[string]bool{}
	var allowlist []string
	ran := 0 // coverage needs both runs; -run may select one
	for _, tc := range []struct {
		name, ssar, namespace string
	}{
		{"a tenant that may create pods and Services", `{"allowed":true}`, `{"metadata":{"name":"tenant-a"}}`},
		{"a tenant that may not", `{"allowed":false}`, `{"metadata":{"name":"tenant-a","labels":{"pod-security.kubernetes.io/enforce":"restricted"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran++
			ok := testutil.JSON(http.StatusOK, `{"items":[]}`)
			routes := map[string]http.HandlerFunc{
				"GET /version":                                                          testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
				"GET /api/v1/namespaces/tenant-a":                                       testutil.JSON(http.StatusOK, tc.namespace),
				"GET /api/v1/namespaces/tenant-a/resourcequotas":                        quota,
				"GET /api/v1/namespaces/tenant-a/limitranges":                           ok,
				"GET /apis/networking.k8s.io/v1/namespaces/tenant-a/networkpolicies":    ok,
				"GET /apis/apps/v1/namespaces/tenant-a/deployments":                     deployment,
				"GET /apis/apps/v1/namespaces/tenant-a/statefulsets":                    ok,
				"GET /apis/autoscaling/v2/namespaces/tenant-a/horizontalpodautoscalers": ok,
				"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews":           testutil.SSAR(tc.ssar),
				"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":                testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"tenant-a-dev"}}}`),
				"POST /api/v1/namespaces/tenant-a/pods":                                 dryRunAnswer,
				"POST /api/v1/namespaces/tenant-a/services":                             dryRunAnswer,
			}
			allowlist = slices.Sorted(maps.Keys(routes))
			srv := testutil.NewServer(t, routes)
			c, err := cluster.Connect(t.Context(), &rest.Config{Host: srv.URL}, cluster.Options{Namespace: ns, OtherNamespace: "kube-system", As: "tenant-a-dev", Timeout: time.Minute})
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			for _, r := range checks.Run(t.Context(), c, checks.Registry, time.Minute) {
				// AT-09 keeps a failed namespace question as a note on a PASS, so a refusal can hide there too.
				if r.Verdict == report.Error || strings.Contains(r.Reason+r.Note, "read-only allowlist") {
					t.Errorf("%s: %s %s; note: %s", r.ID, r.Verdict, r.Reason, r.Note)
				}
			}
			var traced []string
			for _, r := range c.Recorder.Requests() {
				u, err := url.Parse(r.Path)
				if err != nil {
					t.Fatalf("trace path %q: %v", r.Path, err)
				}
				traced = append(traced, r.Method+" "+u.Path)
				review := r.Method == http.MethodPost && (u.Path == "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" || u.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews")
				dryRun := r.Method == http.MethodPost && u.Query().Get("dryRun") == "All"
				if (r.Method != http.MethodGet && !review && !dryRun) || strings.Contains(u.Path, "secrets") {
					t.Errorf("%s %s: a write or a secrets path left the process", r.Method, r.Path)
				}
			}
			hits := srv.Hits()
			if !slices.Equal(hits, traced) {
				t.Errorf("server saw %v, trace has %v", hits, traced)
			}
			for _, h := range hits {
				if !slices.Contains(allowlist, h) {
					t.Errorf("%s reached the server but is not an allowlisted path", h)
				}
				used[h] = true
			}
		})
	}
	if ran < 2 {
		return
	}
	for _, path := range allowlist {
		if !used[path] {
			t.Errorf("no check sent %s: the replay does not cover every allowlisted path", path)
		}
	}
}

// TestSafetyRefusesARedirectToAnotherHost: client-go would re-attach the bearer token to the
// redirected request, so the transport must stop it at the host check.
func TestSafetyRefusesARedirectToAnotherHost(t *testing.T) {
	other := testutil.NewServer(t, map[string]http.HandlerFunc{"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"evil"}`)})
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /version": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/version", http.StatusFound)
		},
	})
	cs := srv.Client(t, ns, nil)
	_, err := cs.Discovery().ServerVersion()
	if _, ok := errors.AsType[*safety.RefusedError](err); !ok {
		t.Errorf("error = %v, want a RefusedError for the redirected host", err)
	}
	if hits := other.Hits(); len(hits) != 0 {
		t.Errorf("the other host received %v", hits)
	}
}

func TestSafetyMatchesPathsUnderTheServerPrefix(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /k8s/clusters/c-demo/version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"GET /version":                     testutil.JSON(http.StatusOK, `{"gitVersion":"wrong"}`),
	})
	base, _ := url.Parse(srv.URL + "/k8s/clusters/c-demo")
	cfg := &rest.Config{Host: base.String(), WrapTransport: safety.Wrap(ns, base, nil, nil)}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := cs.Discovery().ServerVersion()
	if err != nil || v.GitVersion != "v1.37.0" {
		t.Fatalf("ServerVersion() = %v, %v; want v1.37.0 through the prefix", v, err)
	}
	client := &http.Client{Transport: &safety.Transport{Namespace: ns, Base: base, Next: http.DefaultTransport}}
	if _, err := client.Get(srv.URL + "/version"); err == nil {
		t.Error("a path outside the prefix must be refused")
	}
	if got := srv.Hits(); len(got) != 1 || got[0] != "GET /k8s/clusters/c-demo/version" {
		t.Errorf("server saw %v", got)
	}
}

func TestSafetyMatchesAnEncodedServerPrefix(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{"GET /k8s x/version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`)})
	base, _ := url.Parse(srv.URL + "/k8s%20x")
	cfg := &rest.Config{Host: base.String(), WrapTransport: safety.Wrap(ns, base, nil, nil)}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := cs.Discovery().ServerVersion(); err != nil || v.GitVersion != "v1.37.0" {
		t.Errorf("ServerVersion() = %v, %v; a percent-encoded prefix must still match", v, err)
	}
}

func TestSafetyGuardsAgainstCraftedRequests(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{"POST /api/v1/namespaces/tenant-a/pods": testutil.JSON(http.StatusCreated, `{}`)})
	base, _ := url.Parse(srv.URL)
	for _, tt := range []struct {
		name      string
		namespace string
		mutate    func(*url.URL)
		noLatch   bool
	}{
		{"opaque target", ns, func(u *url.URL) { u.Opaque = "/api/v1/namespaces/tenant-a/pods?x=1" }, false},
		{"namespace with a slash", "tenant-a/secrets", func(*url.URL) {}, false},
		{"namespace ..", "..", func(*url.URL) {}, false},
		{"other scheme", ns, func(u *url.URL) { u.Scheme = "ftp" }, false},
		{"a dry-run create through a transport with no latch (D103)", ns, func(*url.URL) {}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			latch := new(atomic.Bool) // a real latch, so each row is refused by its own rule
			if tt.noLatch {
				latch = nil
			}
			tr := &safety.Transport{Namespace: tt.namespace, Base: base, Next: http.DefaultTransport, Unverified: latch}
			u, _ := url.Parse(srv.URL + "/api/v1/namespaces/" + tt.namespace + "/pods?dryRun=All")
			tt.mutate(u)
			req, _ := http.NewRequest(http.MethodPost, u.String(), strings.NewReader("{}"))
			req.URL = u
			if _, err := tr.RoundTrip(req); err == nil {
				t.Error("want a refusal")
			}
		})
	}
	if hits := srv.Hits(); len(hits) != 0 {
		t.Errorf("server saw %v", hits)
	}
}

func TestSafetyRecordsATruncatedPOSTAnswerAsAnError(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "{")
		},
	})
	base, _ := url.Parse(srv.URL)
	rec := &safety.Recorder{}
	client := &http.Client{Transport: &safety.Transport{Namespace: ns, Base: base, Next: http.DefaultTransport, Recorder: rec}}
	resp, err := client.Post(srv.URL+"/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", "application/json", strings.NewReader("{}"))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a truncated answer must surface as an error, not as a half body")
	}
	if reqs := rec.Requests(); len(reqs) != 1 || reqs[0].Status != http.StatusCreated || reqs[0].Body != "" {
		t.Errorf("Requests() = %+v, want the failed POST recorded with status 201 and no body", reqs)
	}
}

func TestSafetyRefusesEverythingWithoutATargetNamespace(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{"GET /version": testutil.JSON(http.StatusOK, `{}`)})
	cs := srv.Client(t, "", nil)
	_, err := cs.Discovery().ServerVersion()
	if _, ok := errors.AsType[*safety.RefusedError](err); !ok {
		t.Errorf("error = %v, want a RefusedError: an empty namespace must not widen the allowlist", err)
	}
	if hits := srv.Hits(); len(hits) != 0 {
		t.Errorf("server saw %v", hits)
	}
}

func TestSafetyRecordsAFailedRequestWithStatusZero(t *testing.T) {
	srv := testutil.NewServer(t, nil)
	rec := &safety.Recorder{}
	cs := srv.Client(t, ns, rec)
	srv.Close()

	_, err := cs.Discovery().ServerVersion()
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if _, refused := errors.AsType[*safety.RefusedError](err); refused {
		t.Errorf("a connection error must not look like a refusal: %v", err)
	}
	reqs := rec.Requests()
	if len(reqs) != 1 || reqs[0].Status != 0 || reqs[0].Method != http.MethodGet {
		t.Errorf("Requests() = %+v, want one GET with status 0", reqs)
	}
}

func TestRefusedErrorNamesTheRequest(t *testing.T) {
	err := &safety.RefusedError{Method: http.MethodDelete, Path: "/api/v1/namespaces/tenant-a/pods/x"}
	for _, want := range []string{"DELETE", "/api/v1/namespaces/tenant-a/pods/x", "bug"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() = %q, want it to mention %q", err.Error(), want)
		}
	}
}

func TestSafetyRecordsTheSelfSubjectReviewWithoutExtra(t *testing.T) {
	answer := `{"status":{"userInfo":{"username":"tenant-a-dev","extra":{"arn":["SENTINEL-EXTRA"]}}}}`
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews": testutil.JSON(http.StatusCreated, answer),
	})
	rec := &safety.Recorder{}
	review, err := srv.Client(t, ns, rec).AuthenticationV1().SelfSubjectReviews().Create(t.Context(), &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := review.Status.UserInfo.Extra["arn"]; len(got) != 1 {
		t.Errorf("the caller must still get extra, got %v", review.Status.UserInfo.Extra)
	}
	reqs := rec.Requests()
	if len(reqs) != 1 || strings.Contains(reqs[0].Body, "SENTINEL-EXTRA") || !strings.Contains(reqs[0].Body, "tenant-a-dev") {
		t.Errorf("recorded = %+v; want the username kept and userInfo.extra dropped (identity-provider claims must not reach --debug)", reqs)
	}
}

func TestSafetyKeepsUserInfoExtraOutOfTheTraceUnderAPrefixAndOnBadBodies(t *testing.T) {
	const prefix = "/k8s/clusters/c-demo" // a Rancher-style server URL
	for name, body := range map[string]string{
		"behind a server path prefix": `{"status":{"userInfo":{"username":"u","extra":{"arn":["SENTINEL-EXTRA"]}}}}`,
		"an answer that is not JSON":  `{"status":{"userInfo":{"extra":{"arn":["SENTINEL-EXTRA"]}}}}garbage`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := testutil.NewServer(t, map[string]http.HandlerFunc{
				"POST " + prefix + "/apis/authentication.k8s.io/v1/selfsubjectreviews": testutil.JSON(http.StatusCreated, body),
			})
			base, _ := url.Parse(srv.URL + prefix)
			rec := &safety.Recorder{}
			cs, err := kubernetes.NewForConfig(&rest.Config{Host: base.String(), WrapTransport: safety.Wrap(ns, base, rec, nil)})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = cs.AuthenticationV1().SelfSubjectReviews().Create(t.Context(), &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
			if reqs := rec.Requests(); len(reqs) != 1 || reqs[0].Body == "" || strings.Contains(reqs[0].Body, "SENTINEL-EXTRA") {
				t.Errorf("recorded = %+v; userInfo.extra must never reach --debug", reqs)
			}
		})
	}
}

func TestSafetyRefusesAnAnswerTooLargeToBeFromAnAPIServer(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.JSON(http.StatusCreated, `{"status":{"reason":"`+strings.Repeat("x", 1<<20)+`"}}`),
	})
	base, _ := url.Parse(srv.URL)
	rec := &safety.Recorder{}
	client := &http.Client{Transport: &safety.Transport{Namespace: ns, Base: base, Next: http.DefaultTransport, Recorder: rec}}
	resp, err := client.Post(srv.URL+"/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", "application/json", strings.NewReader("{}"))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("an answer over 1 MiB must be an error, not buffered whole")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("error = %v, want the size limit named", err)
	}
	if reqs := rec.Requests(); len(reqs) != 1 || reqs[0].Body != "" {
		t.Errorf("Requests() = %d recorded, body %d bytes; want the POST recorded with no body", len(reqs), len(reqs[0].Body))
	}
}

// Added for the PR #3 must-fix items (D103).
// Value: protects=after a pods or services POST answer that is not a verified dry run, no later create leaves the process; fails_when=the transport
// latches only on some answers, or not at all; why_new=the D101 latch lived in the checks and saw only a resourceVersion; seam=the transport under a
// real clientset; no_claim=what the checks report (internal/checks)
func TestSafetySendsNoCreateAfterAnAnswerItCannotVerify(t *testing.T) {
	dropped := func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) } // the connection drops after the request was sent
	for _, tt := range []struct {
		name   string
		answer http.HandlerFunc
		latch  bool
	}{
		{"an accepted dry run", testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"}}`), false},
		{"a rejection", testutil.JSON(http.StatusForbidden, `{"kind":"Status","code":403}`), false},
		{"a malformed probe", testutil.JSON(http.StatusUnprocessableEntity, `{"kind":"Status","code":422}`), false},
		{"a stored object", testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p","resourceVersion":"4712"}}`), true},
		{"a resourceVersion that is not a string", testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p","resourceVersion":4712}}`), true},
		{"no generated name", testutil.JSON(http.StatusCreated, `{"metadata":{"name":"tenant-proxy-object"}}`), true},
		{"no metadata", testutil.JSON(http.StatusCreated, `{"kind":"Pod"}`), true},
		{"an empty 2xx body", testutil.JSON(http.StatusCreated, ``), true},
		{"a 2xx body that is not JSON", testutil.JSON(http.StatusOK, `<html>proxy</html>`), true},
		{"a 2xx body over 1 MiB", testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-x7k2p"},"x":"`+strings.Repeat("x", 1<<20)+`"}`), true},
		{"a failed send", dropped, true},
		{"a 429", testutil.JSON(http.StatusTooManyRequests, `{"kind":"Status","code":429}`), true},
		{"a 503", testutil.JSON(http.StatusServiceUnavailable, `{"kind":"Status","code":503}`), true},
		{"a redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := testutil.NewServer(t, map[string]http.HandlerFunc{
				"POST /api/v1/namespaces/tenant-a/pods":     tt.answer,
				"POST /api/v1/namespaces/tenant-a/services": testutil.JSON(http.StatusCreated, `{"metadata":{"name":"audit-tenant-probe-s"}}`),
			})
			cs := srv.Client(t, ns, nil)
			dryRun := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
			_, _ = cs.CoreV1().Pods(ns).Create(t.Context(), &corev1.Pod{}, dryRun)
			_, err := cs.CoreV1().Services(ns).Create(t.Context(), &corev1.Service{}, dryRun)
			sent := slices.Contains(srv.Hits(), "POST /api/v1/namespaces/tenant-a/services")
			if sent == tt.latch {
				t.Errorf("after %s: later create sent = %v, want %v", tt.name, sent, !tt.latch)
			}
			if tt.latch && !errors.Is(err, safety.ErrUnverified) {
				t.Errorf("after %s: error = %v, want ErrUnverified", tt.name, err)
			}
		})
	}
}

// Added for the PR #3 must-fix items (D103).
// Value: protects=client-go cannot resend a create: one probe is at most one write if a hop drops dryRun=All; fails_when=Retry-After reaches client-go,
// which resends a POST up to 10 times on a 429 or 5xx; why_new=no test sent Retry-After; seam=the transport under a real clientset; no_claim=GET retries
func TestSafetySendsEachCreateOnce(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		srv := testutil.NewServer(t, map[string]http.HandlerFunc{
			"POST /api/v1/namespaces/tenant-a/pods": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "0")
				testutil.JSON(code, `{"kind":"Status","code":`+strconv.Itoa(code)+`}`)(w, nil)
			},
		})
		_, err := srv.Client(t, ns, nil).CoreV1().Pods(ns).Create(t.Context(), &corev1.Pod{}, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		if err == nil {
			t.Errorf("HTTP %d: want the error returned", code)
		}
		if hits := srv.Hits(); len(hits) != 1 {
			t.Errorf("HTTP %d with Retry-After: server saw %d creates, want 1", code, len(hits))
		}
	}
}

// D140, D145: a mutating webhook can inject values (env, tokens) into an accepted object, and users
// paste --debug output into public issues, so the trace keeps only the metadata and the probe's
// security fields of an accepted create: enough to see that a mutation removed a dangerous field.
func TestSafetyShowsOnlyTheMetadataAndSecurityFieldsOfAnAcceptedCreate(t *testing.T) {
	accepted := `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"audit-tenant-probe-x7k2p","namespace":"tenant-a","uid":"u-1",` +
		`"annotations":{"vault.example/token":"SENTINEL-ANNOTATION"}},"spec":{"hostPID":true,"containers":[{"name":"probe","image":"SENTINEL-IMAGE",` +
		`"securityContext":{"privileged":false,"windowsOptions":{"gmsaCredentialSpec":"SENTINEL-GMSA"}},"env":[{"name":"TOKEN","value":"SENTINEL-ENV"}]}],` +
		`"volumes":[{"name":"host","hostPath":{"path":"/"}},{"name":"creds","secret":{"secretName":"SENTINEL-SECRET"}}]},"status":{"phase":"Pending"}}`
	for _, tt := range []struct {
		name         string
		status       int
		body         string
		want, hidden []string
	}{
		{"an accepted dry run", http.StatusCreated, accepted,
			[]string{`"name":"audit-tenant-probe-x7k2p"`, `"uid":"u-1"`, `"kind":"Pod"`, `"hostPID":true`, `"securityContext":{"privileged":false}`,
				`{"hostPath":{"path":"/"},"name":"host"}`, "other fields not shown"},
			[]string{"SENTINEL", "Pending", "creds"}},
		{"a NodePort Service", http.StatusCreated, `{"kind":"Service","metadata":{"name":"audit-tenant-probe-s1"},"spec":{"type":"NodePort","clusterIP":"SENTINEL"}}`,
			[]string{`"type":"NodePort"`}, []string{"SENTINEL"}},
		{"a stored object whose resourceVersion is a number", http.StatusCreated, `{"kind":"Pod","metadata":{"name":"x","resourceVersion":4711},"spec":{"x":"SENTINEL"}}`,
			[]string{`"resourceVersion":4711`}, []string{"SENTINEL"}},
		// Added by the /ship test coverage audit (Phase 4), pass 1.
		// Value: protects=--debug shows an injected init container's name and gate fields, never its image, env or windowsOptions (D140, D145);
		// fails_when=createSummary stops reading spec.initContainers or copies them whole; why_new=the rows above carry regular containers only;
		// seam=none; no_claim=ephemeral containers, which a create cannot carry
		{"an init container a webhook injected", http.StatusCreated, `{"kind":"Pod","metadata":{"name":"audit-tenant-probe-x7k2p"},"spec":{"initContainers":[{"name":"istio-init",` +
			`"image":"SENTINEL-IMAGE","env":[{"name":"T","value":"SENTINEL-ENV"}],"securityContext":{"capabilities":{"add":["NET_ADMIN"]},"runAsUser":0,` +
			`"windowsOptions":{"gmsaCredentialSpec":"SENTINEL-GMSA"}}}],"containers":[{"name":"probe"}]}}`,
			[]string{`"initContainers":[{"name":"istio-init","securityContext":{"capabilities":{"add":["NET_ADMIN"]},"runAsUser":0}}]`}, []string{"SENTINEL"}},
		{"an accepted answer that is not JSON", http.StatusOK, "SENTINEL plain text", []string{"not shown"}, []string{"SENTINEL"}},
		{"a rejection", http.StatusForbidden, `{"kind":"Status","message":"pods \"audit-tenant-probe-x\" is forbidden: violates PodSecurity"}`,
			[]string{"violates PodSecurity"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/namespaces/tenant-a/pods"
			if strings.Contains(tt.body, `"Service"`) {
				path = "/api/v1/namespaces/tenant-a/services"
			}
			srv := testutil.NewServer(t, map[string]http.HandlerFunc{"POST " + path: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}})
			base, _ := url.Parse(srv.URL)
			rec := &safety.Recorder{}
			client := &http.Client{Transport: &safety.Transport{Namespace: ns, Base: base, Next: http.DefaultTransport, Recorder: rec, Unverified: &atomic.Bool{}}}
			resp, err := client.Post(srv.URL+path+"?dryRun=All", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := io.ReadAll(resp.Body); string(got) != tt.body {
				t.Errorf("the client must still get the whole answer; got %q", got)
			}
			_ = resp.Body.Close()
			reqs := rec.Requests()
			for _, w := range tt.want {
				if !strings.Contains(reqs[0].Body, w) {
					t.Errorf("trace body %q lacks %q", reqs[0].Body, w)
				}
			}
			for _, h := range tt.hidden {
				if strings.Contains(reqs[0].Body, h) {
					t.Errorf("trace body %q shows %q", reqs[0].Body, h)
				}
			}
		})
	}
}
