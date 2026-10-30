package cli

import (
	"testing"

	"k8s.io/client-go/rest"
)

// SetInClusterConfig stands in for the pod's own config (the service account token and the
// KUBERNETES_SERVICE_* address) for one test.
func SetInClusterConfig(t *testing.T, f func() (*rest.Config, error)) {
	t.Helper()
	saved := inClusterConfig
	inClusterConfig = f
	t.Cleanup(func() { inClusterConfig = saved })
}
