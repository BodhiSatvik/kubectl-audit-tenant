package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"sigs.k8s.io/yaml"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/checks"
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
	return testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"team-a-dev","groups":["team-a"]}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
	}))
}

// unreadable refuses with 403 every object the configuration checks read (the namespace and the
// LISTs), as for a tenant that may not read them: those checks SKIP, which leaves the exit code alone.
func unreadable(routes map[string]http.HandlerFunc) map[string]http.HandlerFunc {
	for _, path := range []string{"/api/v1/namespaces/tenant-a", "/api/v1/namespaces/tenant-a/resourcequotas", "/api/v1/namespaces/tenant-a/limitranges",
		"/apis/networking.k8s.io/v1/namespaces/tenant-a/networkpolicies", "/apis/apps/v1/namespaces/tenant-a/deployments",
		"/apis/apps/v1/namespaces/tenant-a/statefulsets", "/apis/autoscaling/v2/namespaces/tenant-a/horizontalpodautoscalers"} {
		routes["GET "+path] = testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, path+" is forbidden")
	}
	return routes
}

// unbuilt removes the Run of the named checks for the duration of the test, as before their tasks
// landed, so the "not in this build" exit keeps a test once every check is built.
func unbuilt(t *testing.T, ids ...string) {
	t.Helper()
	saved := checks.Registry
	checks.Registry = slices.Clone(checks.Registry)
	for i := range checks.Registry {
		if slices.Contains(ids, checks.Registry[i].ID) {
			checks.Registry[i].Run = nil
		}
	}
	t.Cleanup(func() { checks.Registry = saved })
}

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = cli.Main(args, info, genericiooptions.IOStreams{In: strings.NewReader(""), Out: &out, ErrOut: &errOut})
	return code, out.String(), errOut.String()
}

func TestFlagValidationExitsTwo(t *testing.T) {
	unbuilt(t, "AT-06")
	srv := cluster(t)
	withNS := kubeconfig(t, srv.URL, "tenant-a")
	noNS := kubeconfig(t, srv.URL, "")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"--as-group without --as", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--as-group", "team-a"}, "--as-group and --as-uid need a user to impersonate"},
		{"unknown check in --checks", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--checks", "AT-99"}, `unknown check ID "AT-99"`},
		{"unknown check in --skip", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--skip", "at-01"}, `unknown check ID "at-01"`},
		{"empty selection", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--checks", "AT-01", "--skip", "AT-01"}, "no checks selected"},
		{"unknown output", []string{"-n", "tenant-a", "--kubeconfig", withNS, "-o", "yaml"}, `unknown output format "yaml"`},
		// D147: a partial report would replace the identity's full report.
		{"policyreport with --checks", []string{"-n", "tenant-a", "--kubeconfig", withNS, "-o", "policyreport", "--checks", "AT-01"}, "-o policyreport needs every check"},
		{"policyreport with --skip", []string{"-n", "tenant-a", "--kubeconfig", withNS, "-o", "policyreport", "--skip", "AT-04.2"}, "-o policyreport needs every check"},
		{"other namespace equals target", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--other-namespace", "tenant-a"}, "--other-namespace must differ from -n"},
		{"timeout zero", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--timeout", "0"}, "--timeout must be longer than 0"},
		{"namespace required", []string{"--kubeconfig", noNS}, "namespace required"},
		{"namespace is not one DNS label", []string{"-n", "kube-system/secrets", "--kubeconfig", withNS}, "invalid namespace"},
		{"other namespace is not one DNS label", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--other-namespace", ""}, "invalid --other-namespace"},
		{"--as-uid without --as", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--as-uid", "1234"}, "--as-group and --as-uid need a user to impersonate"},
		{"check not in this build", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--checks", "AT-06"}, "not in this build yet"},
		{"kubeconfig cannot load", []string{"-n", "tenant-a", "--kubeconfig", filepath.Join(t.TempDir(), "missing")}, "kubeconfig"},
		{"unknown flag", []string{"-n", "tenant-a", "--bogus"}, "unknown flag"},
		{"stray argument", []string{"-n", "tenant-a", "--kubeconfig", withNS, "extra"}, "unknown command"},
		// Added by the /ship test coverage audit, pass 1.
		// Value: protects=a mistyped --context exits 2 naming it, before any request; fails_when=a missing context falls back to current-context and audits another cluster;
		// why_new=no row reached the ToRESTConfig error branch; seam=none; no_claim=the wording after the context name (cli-runtime owns it)
		{"--context names no context", []string{"-n", "tenant-a", "--kubeconfig", withNS, "--context", "typo"}, `cannot load the kubeconfig: context "typo"`},
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
	for _, want := range []string{"namespace tenant-a", "as team-a-dev (groups: team-a)", "cluster kind-dev", "PASS  AT-01    No cluster-scoped access", "exit 0"} {
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
	srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "forbidden"),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
	}))
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
	// The kubeconfig user's as: is the user that --as-group adds groups to.
	code, stdout, stderr := run(t, "--kubeconfig", path, "--user", "proxy", "--as-group", "team-b")
	if code != 0 || !strings.Contains(stdout, "as team-a-dev (unverified) (groups: team-b)") {
		t.Errorf("exit %d, stderr %q; a kubeconfig as: user must take --as-group:\n%s", code, stderr, stdout)
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
	golden(t, "list-checks.golden", stdout)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if code != 0 || len(lines) != 10 || !strings.HasPrefix(lines[0], "AT-01") || !strings.Contains(lines[0], "MTB-PL1-CC-CPI-1") {
		t.Errorf("list-checks: exit %d, %d lines:\n%s", code, len(lines), stdout)
	}
	code, stdout, _ = run(t, "list-checks", "-o", "json")
	golden(t, "list-checks-json.golden", stdout)
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

// golden compares got with testdata/name; go test -update rewrites the file first.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s:\n--- got\n%s\n--- want\n%s", path, got, want)
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

// Added by the /ship test coverage audit, pass 1.
// Value: protects=server text on stderr (the startup error line, --debug answer bodies) carries no ESC, BEL or CR;
// fails_when=report.Sanitize is dropped from Main's error line or from the --debug body print; why_new=sanitize tests cover only the stdout checklist;
// seam=none; no_claim=sanitizing of request paths, which the client builds
func TestServerTextOnStderrIsSanitized(t *testing.T) {
	const evil = "\x1b]0;owned\x07\x1b[2K\rPASS"
	proxyText := func(w http.ResponseWriter, _ *http.Request) { // a text answer: --debug records it byte for byte
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "etcd down"+evil)
	}
	for name, ssar := range map[string]http.HandlerFunc{
		"the startup error line": testutil.Status(http.StatusInternalServerError, metav1.StatusReasonInternalError, "etcd down"+evil),
		"a --debug answer body":  proxyText,
	} {
		t.Run(name, func(t *testing.T) {
			srv := testutil.NewServer(t, map[string]http.HandlerFunc{
				"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
				"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"u"}}}`),
				"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
				"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": ssar,
			})
			code, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--debug")
			if code != 2 || !strings.Contains(stderr, "error: the first SelfSubjectAccessReview failed") || !strings.Contains(stderr, "etcd down") || !strings.Contains(stderr, `"username":"u"`) {
				t.Fatalf("exit %d; want exit 2 with the startup error and the --debug answer bodies on stderr:\n%q", code, stderr)
			}
			if strings.ContainsAny(stderr, "\x1b\x07\r") {
				t.Errorf("stderr carries raw control characters from the server:\n%q", stderr)
			}
		})
	}
}

// brokenWriter is a stdout that cannot be written, like a full disk or a closed pipe.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// Added by the /ship test coverage audit, pass 1.
// Value: protects=a report that cannot be written exits 2, never 0 (CI reads 0 as a proven namespace); fails_when=the printer's write error is ignored;
// why_new=no test gave the run an unwritable stdout; seam=none; no_claim=partial writes or a later stdout close
func TestAReportThatCannotBeWrittenExitsTwo(t *testing.T) {
	srv := cluster(t) // every answer is a denial, so a written report would be exit 0
	var errOut bytes.Buffer
	streams := genericiooptions.IOStreams{In: strings.NewReader(""), Out: brokenWriter{}, ErrOut: &errOut}
	code := cli.Main([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, "")}, info, streams)
	if code != 2 || !strings.Contains(errOut.String(), "cannot write the report: no space left on device") {
		t.Errorf("exit %d, stderr %q; want exit 2 naming the write error", code, errOut.String())
	}
}

// Added by the /ship test coverage audit, pass 1.
// Value: protects=--plain reaches the printer: ASCII separators, no middle dot; fails_when=the flag is not wired into the checklist printer (e.g. when -o wide lands);
// why_new=goldens call Checklist{Plain: true} directly, never through the flag; seam=none; no_claim=color, which a non-terminal never gets
func TestPlainFlagReachesThePrinter(t *testing.T) {
	srv := cluster(t)
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--plain")
	if code != 0 || !strings.Contains(stdout, "namespace tenant-a | as ") || strings.Contains(stdout, "·") {
		t.Errorf("exit %d, stderr %q; want ASCII separators only:\n%s", code, stderr, stdout)
	}
}

func TestWideFlagReachesThePrinter(t *testing.T) {
	srv := cluster(t) // every "may I?" denied: AT-01 is a PASS by RBAC
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "wide", "--checks", "AT-01")
	if code != 0 || !regexp.MustCompile(`(?m)^PASS  AT-01  MTB-PL1-CC-CPI-1  RBAC  No cluster-scoped access  RBAC denied all 4 actions$`).MatchString(stdout) {
		t.Errorf("exit %d, stderr %q; want the MTB ID and layer columns:\n%s", code, stderr, stdout)
	}
}

func TestPolicyReportFlagPrintsAPolicyReportAndKeepsTheExitCode(t *testing.T) {
	srv := clusterWithSSAR(t, testutil.SSAR(`{"allowed":true}`)) // AT-01 FAIL: exit 1
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "policyreport")
	var pr struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Results []struct {
			Result string `json:"result"`
		} `json:"results"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &pr); err != nil || code != 1 || pr.Kind != "PolicyReport" || !strings.HasPrefix(pr.Metadata.Name, "audit-tenant-kind-dev-") ||
		pr.Metadata.Namespace != "tenant-a" || len(pr.Results) != 14 || pr.Results[0].Result != "fail" {
		t.Errorf("exit %d, err %v, stderr %q; want exit 1 and a PolicyReport of 14 results, AT-01 failed:\n%s", code, err, stderr, stdout)
	}
}

func TestPolicyReportNeedsAVerifiedIdentityInSelfMode(t *testing.T) {
	// D151 (Red Team): without a SelfSubjectReview answer the kubeconfig names only its user entry,
	// which other people and in-cluster runs share, so a self-mode report has no name; --as has one.
	srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "forbidden"),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
	}))
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "policyreport")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "-o policyreport needs to know who runs the tool") {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 2 and no report", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "policyreport", "--as", "team-a-dev", "--as-group", "devs")
	if code == 2 || !strings.Contains(stdout, "kind: PolicyReport") {
		t.Errorf("exit %d, stderr %q; --as names the report even when unverified:\n%s", code, stderr, stdout)
	}
}

func TestPolicyReportNameFollowsTheImpersonationFlags(t *testing.T) {
	// D154: the name hashes the flags as given. The fake SelfSubjectReview names kind-dev for every
	// run, so only the request can tell these runs apart.
	srv := clusterWithSSAR(t, testutil.SSAR(`{"allowed":false}`))
	name := func(args ...string) string {
		t.Helper()
		_, stdout, stderr := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "policyreport"}, args...)...)
		var pr struct{ Metadata struct{ Name string } }
		if err := yaml.Unmarshal([]byte(stdout), &pr); err != nil || pr.Metadata.Name == "" {
			t.Fatalf("%v: err %v, stderr %q, no report name:\n%s", args, err, stderr, stdout)
		}
		return pr.Metadata.Name
	}
	sa := "system:serviceaccount:tenant-a:tenant-a-dev"
	base := name("--as", sa)
	if again := name("--as", sa); again != base {
		t.Errorf("the same flags gave %q and %q", base, again)
	}
	for _, args := range [][]string{
		{"--as", sa, "--as-group", "system:authenticated"},
		{"--as", sa, "--as-uid", "u-1"},
		{"--as", sa, "--as-user-extra", "scopes=read"},
		{"--as", sa, "--other-namespace", "tenant-b"},
		nil, // self mode
	} {
		if got := name(args...); got == base {
			t.Errorf("%v must not share the report of --as %s: %q", args, sa, got)
		}
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

func TestOtherNamespaceReachesAT02(t *testing.T) {
	// D72: without it, AT-02 would ask a cluster-wide question that ignores RoleBindings in that namespace.
	srv := cluster(t) // every "may I?" denied
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-02", "--other-namespace", "prod")
	if code != 0 || !strings.Contains(stdout, "secrets in prod") {
		t.Errorf("exit %d, stderr %q; want AT-02 to ask about --other-namespace prod:\n%s", code, stderr, stdout)
	}
}
