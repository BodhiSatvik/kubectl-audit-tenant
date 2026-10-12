package safety_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

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
		"POST /api/v1/namespaces/tenant-a/pods":                                 testutil.JSON(http.StatusCreated, `{"metadata":{"name":"probe"}}`),
		"POST /api/v1/namespaces/tenant-a/services":                             testutil.JSON(http.StatusCreated, `{"metadata":{"name":"probe"}}`),
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
	cfg := &rest.Config{Host: base.String(), WrapTransport: safety.Wrap(ns, base, nil)}
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
	cfg := &rest.Config{Host: base.String(), WrapTransport: safety.Wrap(ns, base, nil)}
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
	}{
		{"opaque target", ns, func(u *url.URL) { u.Opaque = "/api/v1/namespaces/tenant-a/pods?x=1" }},
		{"namespace with a slash", "tenant-a/secrets", func(*url.URL) {}},
		{"namespace ..", "..", func(*url.URL) {}},
		{"other scheme", ns, func(u *url.URL) { u.Scheme = "ftp" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr := &safety.Transport{Namespace: tt.namespace, Base: base, Next: http.DefaultTransport}
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
