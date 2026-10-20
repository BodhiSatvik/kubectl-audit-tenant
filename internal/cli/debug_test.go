package cli_test

import (
	"encoding/json"
	"flag"
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
// SelfSubjectReview extra, and only the metadata of an accepted create (acceptance criterion 2, D51, D140).
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
	path := filepath.Join("testdata", "debug-trace.golden")
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

// D141: warnings for an impersonated user without groups and for a changed --other-namespace; none
// for a service account, a user with --as-group, self mode or the default --other-namespace.
func TestIdentityWarnings(t *testing.T) {
	const user = "warning: --as team-a-dev has no --as-group"
	const other = "warning: --other-namespace prod"
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{[]string{"--as", "team-a-dev"}, []string{user}},
		{[]string{"--as", "team-a-dev", "--as-group", "devs"}, nil},
		{[]string{"--as", "system:serviceaccount:tenant-a:tenant-a-dev"}, nil},
		{nil, nil},
		{[]string{"--other-namespace", "prod"}, []string{other}},
		{[]string{"--as", "team-a-dev", "--other-namespace", "prod"}, []string{user, other}},
	} {
		srv := cluster(t)
		code, stdout, stderr := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-02"}, tt.args...)...)
		if code != 0 || strings.Count(stdout, "\nwarning: ") != len(tt.want) {
			t.Errorf("%v: exit %d, stderr %q; want %d warning lines:\n%s", tt.args, code, stderr, len(tt.want), stdout)
		}
		for _, w := range tt.want {
			if !strings.Contains(stdout, "\n"+w) {
				t.Errorf("%v: stdout lacks %q:\n%s", tt.args, w, stdout)
			}
		}
	}
	srv := cluster(t)
	_, stdout, _ := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-02", "--as", "team-a-dev", "-o", "json")
	var rep struct{ Warnings []string }
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil || len(rep.Warnings) != 1 || !strings.HasPrefix("warning: "+rep.Warnings[0], user) {
		t.Errorf("err %v, warnings %q; -o json must carry the warning:\n%s", err, rep.Warnings, stdout)
	}
}
