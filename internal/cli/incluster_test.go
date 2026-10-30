package cli_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
)

// noKubeconfig makes the run find no kubeconfig, as in a pod or on a fresh machine.
func noKubeconfig(t *testing.T) {
	t.Helper()
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	t.Setenv("HOME", t.TempDir())
}

// S5-F4: clientcmd falls back to the pod's config only when no flag changes its default, so in a
// pod with no kubeconfig every --as run called http://localhost:8080.
func TestAsInAPodWithNoKubeconfigUsesThePodsConfig(t *testing.T) {
	noKubeconfig(t)
	srv := impersonatingCluster(t)
	cli.SetInClusterConfig(t, func() (*rest.Config, error) { return &rest.Config{Host: srv.URL, BearerToken: "pod-token"}, nil })
	const sa = "system:serviceaccount:tenant-a:tenant-a-dev"
	for _, args := range [][]string{
		{"--as", sa},
		{"--as", "alice", "--as-group", "team-a-devs", "--request-timeout", "5s"},
		nil, // self mode worked before; it must keep working
	} {
		code, stdout, stderr := run(t, append([]string{"-n", "tenant-a"}, args...)...)
		want := "as team-a-dev (self) (groups: team-a)"
		if len(args) > 0 {
			want = "as " + args[1] + " (groups: "
		}
		if code != 0 || !strings.Contains(stdout, want) {
			t.Errorf("%v: exit %d, stderr %q; want exit 0 and the header %q from the pod's cluster:\n%s", args, code, stderr, want, stdout)
		}
		if !strings.Contains(stdout, "cluster "+srv.URL) {
			t.Errorf("%v: with no kubeconfig context, the header names the server:\n%s", args, stdout)
		}
	}
}

// S1-F6: with no kubeconfig and no pod, the error says so instead of asking for -n or naming localhost:8080.
func TestNoKubeconfigOutsideAPodSaysSo(t *testing.T) {
	noKubeconfig(t)
	cli.SetInClusterConfig(t, func() (*rest.Config, error) { return nil, rest.ErrNotInCluster })
	for _, args := range [][]string{{"-n", "tenant-a"}, {"-n", "tenant-a", "--as", "alice", "--as-group", "devs"}, nil} {
		code, _, stderr := run(t, args...)
		if code != 2 || !strings.Contains(stderr, "no kubeconfig") || strings.Contains(stderr, "localhost:8080") {
			t.Errorf("%v: exit %d, stderr %q; want exit 2 saying there is no kubeconfig", args, code, stderr)
		}
	}
	cli.SetInClusterConfig(t, func() (*rest.Config, error) { return nil, errors.New("open token: permission denied") })
	if code, _, stderr := run(t, "-n", "tenant-a", "--as", "alice", "--as-group", "devs"); code != 2 || !strings.Contains(stderr, "permission denied") {
		t.Errorf("exit %d, stderr %q; want exit 2 with the pod config's error", code, stderr)
	}
}
