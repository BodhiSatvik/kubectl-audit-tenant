package cli_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// injected is a control pod answer as a mutating webhook could leave it: a token in an env value.
const injected = `{"kind":"Pod","apiVersion":"v1","metadata":{"name":"audit-tenant-probe-ctl01","namespace":"tenant-a","uid":"u-1"},` +
	`"spec":{"containers":[{"name":"probe","env":[{"name":"TOKEN","value":"SENTINEL-ENV"}]}]}}`

// TestDebugTraceGolden is the stderr golden of a fake --debug run with --as and a bearer token: the
// identity steps, one line per request with the answers of the POSTs, never a request header, no
// SelfSubjectReview extra, and only the kind, metadata and probe security fields of an accepted create
// (acceptance criterion 2, D51, D140, D145).
func TestDebugTraceGolden(t *testing.T) {
	srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews": testutil.JSON(http.StatusCreated,
			`{"status":{"userInfo":{"username":"team-a-dev","groups":["team-a","system:authenticated"],"extra":{"token-id":["SENTINEL-EXTRA"]}}}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":true}`),
		"POST /api/v1/namespaces/tenant-a/pods": func(w http.ResponseWriter, r *http.Request) {
			var pod struct{ Spec struct{ HostPID bool } }
			_ = json.NewDecoder(r.Body).Decode(&pod)
			if pod.Spec.HostPID {
				testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, `pods "audit-tenant-probe-p4x2z" is forbidden: violates PodSecurity "restricted:latest": host namespaces (hostPID=true)`)(w, r)
				return
			}
			testutil.JSON(http.StatusCreated, injected)(w, r)
		},
	}))
	code, _, stderr := run(t, "-n", "tenant-a", "--as", "team-a-dev", "--as-group", "team-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-04.3", "--debug")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	got := regexp.MustCompile(`(?m) [0-9.]+(µs|ms|s)$`).ReplaceAllString(stderr, " <duration>")
	path := filepath.Join("testdata", "stderr-trace.golden")
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
		t.Errorf("--debug stderr differs from %s:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
	for _, bad := range []string{"Authorization", "Bearer", "test-token", "Impersonate-", "SENTINEL"} {
		if strings.Contains(stderr, bad) {
			t.Errorf("--debug output contains %q:\n%s", bad, stderr)
		}
	}
}

// D141, D146: warnings for an impersonated user without groups (flag or kubeconfig as:), a service
// account with --as-group, a selection that leaves results out and a changed --other-namespace; none
// for a service account alone, a user with groups, self mode or a full default run.
func TestIdentityWarnings(t *testing.T) {
	const (
		user    = "warning: impersonating team-a-dev with no groups"
		sa      = "warning: impersonating service account system:serviceaccount:tenant-a:tenant-a-dev with groups that leave out system:serviceaccounts and system:serviceaccounts:tenant-a"
		partial = "warning: --checks or --skip ran 1 of 14 results"
		other   = "warning: --other-namespace prod"
	)
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{[]string{"--as", "team-a-dev"}, []string{user}},
		{[]string{"--as", "team-a-dev", "--as-group", "devs"}, nil},
		{[]string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev"}, nil},
		{[]string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev", "--as-group", "devs"}, []string{sa}},
		{[]string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev", "--as-group", "system:serviceaccounts", "--as-group", "system:serviceaccounts:tenant-a"}, nil},
		{[]string{"--skip", "AT-02", "--other-namespace", "prod"}, []string{"warning: --checks or --skip ran 13 of 14 results"}}, // AT-02 did not run: no namespace warning
		{nil, nil},
		{[]string{"--checks", "AT-02"}, []string{partial}},
		{[]string{"--other-namespace", "prod"}, []string{other}},
		{[]string{"--as", "team-a-dev", "--other-namespace", "prod"}, []string{user, other}},
	} {
		srv := impersonatingCluster(t)
		code, stdout, stderr := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, "")}, tt.args...)...)
		if code != 0 || strings.Count(stdout, "\nwarning: ") != len(tt.want) {
			t.Errorf("%v: exit %d, stderr %q; want %d warning lines:\n%s", tt.args, code, stderr, len(tt.want), stdout)
		}
		for _, w := range tt.want {
			if !strings.Contains(stdout, "\n"+w) {
				t.Errorf("%v: stdout lacks %q:\n%s", tt.args, w, stdout)
			}
		}
	}
	// A kubeconfig user's as: impersonates too; the warning names no flag the user never passed.
	srv := impersonatingCluster(t)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters: [{name: dev, cluster: {server: %q}}]\ncontexts: [{name: kind-dev, context: {cluster: dev, user: kind-dev}}]\ncurrent-context: kind-dev\nusers: [{name: kind-dev, user: {token: test-token, as: team-a-dev}}]\n", srv.URL)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stdout, _ := run(t, "-n", "tenant-a", "--kubeconfig", path); !strings.Contains(stdout, "\n"+user+" (--as-group, or as-groups in the kubeconfig)") {
		t.Errorf("kubeconfig as: without groups must warn:\n%s", stdout)
	}
	_, stdout, _ := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--as", "team-a-dev", "-o", "json")
	var rep struct{ Warnings []string }
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil || len(rep.Warnings) != 1 || !strings.HasPrefix("warning: "+rep.Warnings[0], user) {
		t.Errorf("err %v, warnings %q; -o json must carry the warning:\n%s", err, rep.Warnings, stdout)
	}
}

// D196: an access proxy such as Teleport adds its role's groups to --as (live: system:masters). The
// run names groups nobody asked for, and none for the groups the API server adds itself or in self mode.
func TestWarningNamesGroupsTheClusterAddedToAnImpersonatedUser(t *testing.T) {
	const added = "warning: the cluster gives system:serviceaccount:tenant-a:tenant-a-dev groups that the run did not ask for: system:masters;"
	for _, tt := range []struct {
		groups string
		args   []string
		want   bool
	}{
		{`["system:masters","system:authenticated"]`, []string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev"}, true},
		{`["system:serviceaccounts","system:serviceaccounts:tenant-a","system:authenticated"]`, []string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev"}, false},
		{`["system:masters","system:authenticated"]`, []string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev", "--as-group", "system:masters"}, false},
		{`["system:masters","system:authenticated"]`, nil, false},
	} {
		srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
			"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
			"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"system:serviceaccount:tenant-a:tenant-a-dev","groups":`+tt.groups+`}}}`),
			"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
		}))
		code, stdout, stderr := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, "")}, tt.args...)...)
		if got := strings.Contains(stdout, "\n"+added); code != 0 || got != tt.want {
			t.Errorf("groups %s, %v: exit %d, stderr %q, added-groups warning %v; want exit 0 and %v:\n%s", tt.groups, tt.args, code, stderr, got, tt.want, stdout)
		}
	}
}

func TestDebugTraceNamesAnUnverifiedSelfIdentity(t *testing.T) {
	// The last identity line matters most when the SelfSubjectReview fails (testing specialist).
	srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.Status(http.StatusForbidden, metav1.StatusReasonForbidden, "forbidden"),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
	}))
	_, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-02", "--debug")
	for _, want := range []string{"identity: context kind-dev, kubeconfig user kind-dev, no impersonation\n",
		"identity: no SelfSubjectReview answer named the identity, so the header shows kind-dev from the kubeconfig and --as, marked unverified\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("--debug stderr lacks %q:\n%s", want, stderr)
		}
	}
}

// Added by the /ship test coverage audit (Phase 4), pass 1.
// Value: protects=the --debug identity line built from the SelfSubjectReview answer carries no raw ESC, BEL or CR from the server;
// fails_when=report.Sanitize is dropped from trace's identity lines; why_new=the stderr sanitize test names a plain user, and JSON escapes the body line;
// seam=none; no_claim=sanitizing of the kubeconfig and --as text on the first identity line
func TestDebugIdentityLineSanitizesTheServerNamedIdentity(t *testing.T) {
	srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews": testutil.JSON(http.StatusCreated,
			`{"status":{"userInfo":{"username":"team-a-dev\u001b]0;owned\u0007\u001b[2K\r","groups":["team-a\u001b[31m"]}}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
	}))
	_, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-02", "--debug")
	if !strings.Contains(stderr, "identity: the SelfSubjectReview names team-a-dev") {
		t.Fatalf("--debug stderr lacks the verified identity line:\n%q", stderr)
	}
	if strings.ContainsAny(stderr, "\x1b\x07\r") {
		t.Errorf("the --debug identity line carries raw control characters from the server:\n%q", stderr)
	}
}
