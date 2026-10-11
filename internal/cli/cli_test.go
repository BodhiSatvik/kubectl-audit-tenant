package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

var info = cli.BuildInfo{Version: "v0.1.0-test", Commit: "abc1234", Date: "2026-10-11"}

// kubeconfig writes a kubeconfig for server with an optional namespace in its context.
func kubeconfig(t *testing.T, server, namespace string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters: [{name: dev, cluster: {server: %q}}]
contexts: [{name: kind-dev, context: {cluster: dev, user: kind-dev, namespace: %q}}]
current-context: kind-dev
users: [{name: kind-dev, user: {token: test-token}}]
`, server, namespace)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cluster serves a startup-complete cluster whose every "may I?" is denied.
func cluster(t *testing.T) *testutil.Server {
	t.Helper()
	return testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"team-a-dev","groups":["team-a"]}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.JSON(http.StatusCreated, `{"status":{"allowed":false}}`),
	})
}

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = cli.Main(args, info, genericiooptions.IOStreams{In: strings.NewReader(""), Out: &out, ErrOut: &errOut})
	return code, out.String(), errOut.String()
}

func TestFlagValidationExitsTwo(t *testing.T) {
	srv := cluster(t)
	withNS := kubeconfig(t, srv.URL, "tenant-a")
	noNS := kubeconfig(t, srv.URL, "")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"--as-group without --as", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--as-group", "team-a"}, "--as-group and --as-uid need --as"},
		{"unknown check in --checks", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--checks", "AT-99"}, `unknown check ID "AT-99"`},
		{"unknown check in --skip", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--skip", "at-01"}, `unknown check ID "at-01"`},
		{"empty selection", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--checks", "AT-01", "--skip", "AT-01"}, "no checks selected"},
		{"unknown output", []string{"-n", "tenant-a", "--kubeconfig", withNS, "-o", "yaml"}, `unknown output format "yaml"`},
		{"other namespace equals target", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--other-namespace", "tenant-a"}, "--other-namespace must differ from -n"},
		{"timeout zero", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--timeout", "0"}, "--timeout must be longer than 0"},
		{"namespace required", []string{"--kubeconfig", noNS}, "namespace required"},
		{"namespace is not one DNS label", []string{"-n", "kube-system/secrets", "--kubeconfig", withNS}, "invalid namespace"},
		{"--as-uid without --as", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--as-uid", "1234"}, "--as-group and --as-uid need --as"},
		{"check not in this build", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--checks", "AT-02"}, "not in this build yet"},
		{"kubeconfig cannot load", []string{"-n", "tenant-a", "--kubeconfig", filepath.Join(t.TempDir(), "missing")}, "kubeconfig"},
		{"unknown flag", []string{"-n", "tenant-a", "--bogus"}, "unknown flag"},
		{"stray argument", []string{"-n", "tenant-a", "--kubeconfig", withNS, "extra"}, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := run(t, tt.args...)
			if code != 2 || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit %d, stderr %q; want exit 2 mentioning %q", code, stderr, tt.want)
			}
		})
	}
	if hits := srv.Hits(); len(hits) != 0 {
		t.Errorf("flag validation must not touch the cluster; saw %v", hits)
	}
}

func TestStartupFailureExitsTwoWithTheReason(t *testing.T) {
	srv := cluster(t)
	srv.Close()
	code, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""))
	if code != 2 || !strings.Contains(stderr, "API server unreachable") {
		t.Errorf("exit %d, stderr %q; want exit 2 with 'API server unreachable'", code, stderr)
	}
}

func TestRunPrintsTheChecklistAndExitsWithTheSummaryCode(t *testing.T) {
	srv := cluster(t)
	code, stdout, stderr := run(t, "-n", "tenant-a", "--as", "team-a-dev", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--debug")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"namespace tenant-a", "as team-a-dev (groups: team-a)", "cluster kind-dev", "PASS  AT-01  No cluster-scoped access", "exit 0"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "GET /version") || !strings.Contains(stderr, "POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews") {
		t.Errorf("--debug should list every request on stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "Authorization") || strings.Contains(stderr, "test-token") || strings.Contains(stderr, "Impersonate") {
		t.Errorf("--debug must not print request headers:\n%s", stderr)
	}
}

func TestIdentityComesFromTheEffectiveConfig(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "forbidden"),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.JSON(http.StatusCreated, `{"status":{"allowed":false}}`),
	})
	path := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters: [{name: dev, cluster: {server: %q}}]
contexts: [{name: kind-dev, context: {cluster: dev, user: kind-dev, namespace: tenant-a}}]
current-context: kind-dev
users:
  - {name: kind-dev, user: {token: t1}}
  - {name: alt, user: {token: t2}}
  - {name: proxy, user: {token: t3, as: team-a-dev, as-groups: [team-a]}}
`, srv.URL)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// SelfSubjectReview is forbidden here, so the header shows the fallback identity.
	_, stdout, _ := run(t, "--kubeconfig", path, "--user", "alt")
	if !strings.Contains(stdout, "as alt (self) (unverified)") {
		t.Errorf("--user must name the overriding user in the header:\n%s", stdout)
	}
	_, stdout, _ = run(t, "--kubeconfig", path, "--user", "proxy")
	if !strings.Contains(stdout, "as team-a-dev (unverified) (groups: team-a)") {
		t.Errorf("kubeconfig impersonation must show as impersonation:\n%s", stdout)
	}
}

func TestNamespaceComesFromTheContextWhenFlagIsAbsent(t *testing.T) {
	srv := cluster(t)
	code, stdout, stderr := run(t, "--kubeconfig", kubeconfig(t, srv.URL, "tenant-a"))
	if code != 0 || !strings.Contains(stdout, "namespace tenant-a") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestListChecks(t *testing.T) {
	code, stdout, _ := run(t, "list-checks")
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if code != 0 || len(lines) != 10 || !strings.HasPrefix(lines[0], "AT-01") || !strings.Contains(lines[0], "MTB-PL1-CC-CPI-1") {
		t.Errorf("list-checks: exit %d, %d lines:\n%s", code, len(lines), stdout)
	}
	code, stdout, _ = run(t, "list-checks", "-o", "json")
	var checks []struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Results  []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &checks); err != nil || code != 0 || len(checks) != 10 || len(checks[3].Results) != 5 || checks[0].Severity != "high" {
		t.Errorf("list-checks -o json: exit %d, err %v, %d checks:\n%s", code, err, len(checks), stdout)
	}
	if code, _, stderr := run(t, "list-checks", "-o", "yaml"); code != 2 || !strings.Contains(stderr, "unknown output format") {
		t.Errorf("list-checks -o yaml: exit %d, stderr %q", code, stderr)
	}
}

func TestVersionPrintsTheBuildInfo(t *testing.T) {
	code, stdout, _ := run(t, "version")
	if code != 0 || stdout != "audit-tenant v0.1.0-test (commit abc1234, built 2026-10-11)\n" {
		t.Errorf("version: exit %d, stdout %q", code, stdout)
	}
}

func TestHelpStartsWithThePluginName(t *testing.T) {
	code, stdout, _ := run(t, "--help")
	if code != 0 || !strings.HasPrefix(stdout, "kubectl audit-tenant") || !strings.Contains(stdout, "--request-timeout") {
		t.Errorf("--help: exit %d, stdout:\n%s", code, stdout)
	}
}

func TestDebugTraceIsPrintedWhenStartupFails(t *testing.T) {
	srv := testutil.NewServer(t, map[string]http.HandlerFunc{
		"GET /version": testutil.Status(http.StatusUnauthorized, metav1.StatusReasonUnauthorized, "Unauthorized"),
	})
	code, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--debug")
	if code != 2 || !strings.Contains(stderr, "GET /version") || !strings.Contains(stderr, " 401 ") {
		t.Errorf("exit %d; --debug must still list the startup requests on a startup failure; stderr:\n%s", code, stderr)
	}
}

func TestDebugPrintsPostAnswerBodiesWithoutHeaders(t *testing.T) {
	srv := cluster(t)
	_, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--debug")
	if !strings.Contains(stderr, `"allowed":false`) {
		t.Errorf("--debug should print the SSAR answer bodies:\n%s", stderr)
	}
	if strings.Contains(stderr, "test-token") || strings.Contains(stderr, "Authorization") {
		t.Errorf("--debug must not print request headers:\n%s", stderr)
	}
}
