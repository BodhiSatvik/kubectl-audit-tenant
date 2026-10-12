package testutil

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	authzv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServerLogsEveryRequestAndServesCannedRoutes(t *testing.T) {
	srv := NewServer(t, map[string]http.HandlerFunc{
		"GET /version": JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
	})
	cs := srv.Client(t, "tenant-a", nil)

	v, err := cs.Discovery().ServerVersion()
	if err != nil || v.GitVersion != "v1.37.0" {
		t.Fatalf("ServerVersion() = %v, %v; want v1.37.0", v, err)
	}
	if _, err := cs.CoreV1().Namespaces().Get(t.Context(), "tenant-a", metav1.GetOptions{}); err == nil {
		t.Error("an unrouted path should answer 404 and fail the call")
	}
	want := []string{"GET /version", "GET /api/v1/namespaces/tenant-a"}
	if got := srv.Hits(); !slices.Equal(got, want) {
		t.Errorf("Hits() = %v, want %v", got, want)
	}
}

func TestStatusAnswersAForbiddenTheClientRecognizes(t *testing.T) {
	srv := NewServer(t, map[string]http.HandlerFunc{
		"GET /api/v1/namespaces/tenant-a": Status(http.StatusForbidden, metav1.StatusReasonForbidden, `namespaces "tenant-a" is forbidden`),
	})
	_, err := srv.Client(t, "tenant-a", nil).CoreV1().Namespaces().Get(t.Context(), "tenant-a", metav1.GetOptions{})
	if !apierrors.IsForbidden(err) {
		t.Errorf("error = %v, want a Forbidden status error", err)
	}
}

func TestSSAREchoesTheQuestionLikeTheAPIServer(t *testing.T) {
	srv := NewServer(t, map[string]http.HandlerFunc{
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": SSAR(`{"allowed":true}`),
	})
	post := func(body string) *http.Response {
		t.Helper()
		resp, err := http.Post(srv.URL+"/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	var got authzv1.SelfSubjectAccessReview // the cluster client sends JSON (cluster.connect sets it)
	if err := json.NewDecoder(post(`{"spec":{"resourceAttributes":{"verb":"list","resource":"nodes"}}}`).Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if a := got.Spec.ResourceAttributes; a == nil || *a != (authzv1.ResourceAttributes{Verb: "list", Resource: "nodes"}) || !got.Status.Allowed {
		t.Errorf("answer = %+v; want the question echoed with allowed=true", got)
	}
	if code := post("not json").StatusCode; code != http.StatusBadRequest {
		t.Errorf("a body that is not an SSAR got %d, want 400", code)
	}
}
