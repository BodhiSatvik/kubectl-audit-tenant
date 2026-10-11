package testutil

import (
	"net/http"
	"slices"
	"testing"

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
