package cli_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	"sigs.k8s.io/yaml"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

type policyReportRows struct {
	Summary map[string]int `json:"summary"`
	Results []struct {
		Policy, Result, Message string
		Properties              map[string]string
	} `json:"results"`
}

// D202: a shop that enforces pod security with a policy engine skips AT-09 on purpose; the report
// keeps one row per result, in run order, and the skip rows leave the exit code alone.
func TestPolicyReportWritesASkipRowForEachSkippedResult(t *testing.T) {
	srv := impersonatingCluster(t)
	args := []string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--as", "system:serviceaccount:tenant-a:dev", "--skip", "AT-09,AT-04.2"}
	wantCode, _, _ := run(t, args...)
	code, stdout, stderr := run(t, append(args, "-o", "policyreport")...)
	var pr policyReportRows
	if err := yaml.Unmarshal([]byte(stdout), &pr); err != nil || code != wantCode || len(pr.Results) != 14 {
		t.Fatalf("exit %d (checklist %d), err %v, stderr %q; want the checklist's exit code and 14 rows:\n%s", code, wantCode, err, stderr, stdout)
	}
	var ids []string
	skips := 0
	for _, r := range pr.Results {
		ids = append(ids, r.Policy)
		if r.Result == "skip" {
			skips++
		}
		if (r.Policy == "AT-09" || r.Policy == "AT-04.2") && (r.Result != "skip" || r.Message != "skipped with --skip: this run did not test it" || r.Properties["hint"] != "drop --skip "+r.Policy+" to test it") {
			t.Errorf("%s: want a skip row naming --skip, got %+v", r.Policy, r)
		}
	}
	if !slices.IsSorted(ids) || pr.Summary["skip"] != skips {
		t.Errorf("rows %v must be in run order and summary.skip %d must count every skip row (%d)", ids, pr.Summary["skip"], skips)
	}
	if !strings.Contains(stderr, "warning: --skip ran 12 of 14 results; the others are skip rows in this report") {
		t.Errorf("stderr %q lacks the --skip warning", stderr)
	}
}

// D203: a report of only SKIPs would replace the identity's FAIL rows on the dashboard.
func TestPolicyReportRefusesARunThatProvedNothing(t *testing.T) {
	srv := impersonatingClusterWithSSAR(t, noRights)
	code, stdout, stderr := run(t, "-n", "tenant-a", "--as", "alcie", "--as-group", "devs", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "policyreport")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "-o policyreport needs a run that proved something: every check was SKIP") {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 2, no report and the refusal", code, stdout, stderr)
	}
}

// D196 review and D203: a proxy that adds a group, drops an asked group or replaces the user makes the
// results describe another identity. The checklist warns; -o policyreport refuses before any check.
func TestAnIdentityTheClusterChangedIsWarnedAndRefusedAsAReport(t *testing.T) {
	const sa = "system:serviceaccount:tenant-a:dev"
	for _, tt := range []struct {
		user, groups string
		args         []string
		want         string
	}{
		{sa, `["system:masters","system:authenticated"]`, []string{"--as", sa}, "the cluster gives " + sa + " groups that the run did not ask for: system:masters"},
		{"kind-dev", `["system:masters","system:authenticated"]`, []string{"--as", sa, "--as-group", "system:masters"}, "the cluster names the identity kind-dev, not the --as user " + sa},
		{"alice", `["system:authenticated"]`, []string{"--as", "alice", "--as-group", "devs"}, "the cluster does not give alice these groups the run asked for: devs"},
	} {
		srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
			"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
			"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"`+tt.user+`","groups":`+tt.groups+`}}}`),
			"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
		}))
		args := append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, "")}, tt.args...)
		if code, stdout, stderr := run(t, args...); code != 0 || !strings.Contains(stdout, "\nwarning: "+tt.want) {
			t.Errorf("%v: exit %d, stderr %q; want exit 0 and the warning %q:\n%s", tt.args, code, stderr, tt.want, stdout)
		}
		before := len(srv.Hits())
		code, stdout, stderr := run(t, append(args, "-o", "policyreport")...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "-o policyreport needs results that describe the identity the flags name, and "+tt.want) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want exit 2 and the refusal", tt.args, code, stdout, stderr)
		}
		if hits := srv.Hits()[before:]; len(hits) != 4 { // the startup requests only: /version, SelfSubjectReview, namespace, first SSAR
			t.Errorf("%v: the refusal must come before any check; saw %v", tt.args, hits)
		}
	}
}

func TestMoreIdentityWarnings(t *testing.T) {
	// The API server treats a service-account name it cannot split as a plain user, and system:authenticated
	// alone adds no group, so both get the no-groups warning.
	for _, as := range [][]string{{"--as", "system:serviceaccount:tenant-a:dev:x"}, {"--as", "team-a-dev", "--as-group", "system:authenticated"}} {
		srv := impersonatingCluster(t)
		if _, stdout, _ := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, "")}, as...)...); strings.Count(stdout, "\nwarning: ") != 1 ||
			!strings.Contains(stdout, "\nwarning: impersonating "+as[1]+" with no groups") {
			t.Errorf("%v: want only the no-groups warning:\n%s", as, stdout)
		}
	}
	// An impersonation the cluster did not confirm cannot show what a proxy changed.
	srv := testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{ // no SelfSubjectReview route: 404
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": testutil.SSAR(`{"allowed":false}`),
	}))
	if _, stdout, _ := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--as", "alice", "--as-group", "devs"); !strings.Contains(stdout, "\nwarning: the cluster did not confirm the impersonated identity") {
		t.Errorf("an unverified --as run must say the run cannot see a proxy's changes:\n%s", stdout)
	}
}

// S5-F3: in -o json and -o policyreport the warnings also go to stderr, which a pipeline log shows.
func TestMachineFormatsPutWarningsOnStderr(t *testing.T) {
	for _, format := range []string{"json", "policyreport", "checklist"} {
		srv := impersonatingCluster(t)
		_, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--as", "team-a-dev", "-o", format)
		if want := format != "checklist"; strings.Contains(stderr, "warning: impersonating team-a-dev with no groups") != want {
			t.Errorf("-o %s: stderr %q; want the warning on stderr: %v", format, stderr, want)
		}
	}
	// The cluster-admin line is a warning in -o json, so it goes to stderr too.
	srv := clusterWithSSAR(t, testutil.SSAR(`{"allowed":true}`))
	if _, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "-o", "json"); !strings.Contains(stderr, "warning: this identity looks like a cluster admin; add --as to test a tenant\n") {
		t.Errorf("stderr %q lacks the cluster-admin line", stderr)
	}
}

// D181: clientcmd's error for a proxy-url it cannot parse quotes the whole URL, password included.
func TestAProxyURLThatDoesNotParseIsNotPrinted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	cfg := "apiVersion: v1\nkind: Config\nclusters: [{name: dev, cluster: {server: \"https://127.0.0.1:6443\", proxy-url: \"http://bob:pa%ss@proxy:3128\"}}]\n" +
		"contexts: [{name: kind-dev, context: {cluster: dev, user: kind-dev}}]\ncurrent-context: kind-dev\nusers: [{name: kind-dev, user: {token: test-token}}]\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", path)
	if code != 2 || strings.Contains(stderr, "pa%ss") || !strings.Contains(stderr, `the proxy-url of cluster "dev" holds user info`) {
		t.Errorf("exit %d, stderr %q; want exit 2 naming the cluster, never the URL", code, stderr)
	}
	// A proxy-url that parses keeps the real error: here, a missing CA file.
	cfg = strings.Replace(strings.Replace(cfg, "pa%ss", "pass", 1), `server: "https://127.0.0.1:6443",`, `server: "https://127.0.0.1:6443", certificate-authority: /no/such/ca.crt,`, 1)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "-n", "tenant-a", "--kubeconfig", path); code != 2 || strings.Contains(stderr, "does not parse") || !strings.Contains(stderr, "ca.crt") {
		t.Errorf("exit %d, stderr %q; want the CA file error, not the proxy-url message", code, stderr)
	}
}

func TestCompletionCommandIsGoneAndGoInstallVersionSaysSo(t *testing.T) {
	// S1-F4: cobra's script completes "audit-tenant", which kubectl never runs.
	if code, _, stderr := run(t, "completion", "zsh"); code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Errorf("exit %d, stderr %q; want the completion command removed", code, stderr)
	}
	// S1-F5: go install from the module proxy records no commit or date.
	var out strings.Builder
	cli.Main([]string{"version"}, cli.BuildInfo{Version: "0.1.2", Commit: "none", Date: "unknown"}, genericiooptions.IOStreams{In: strings.NewReader(""), Out: &out, ErrOut: &out})
	if out.String() != "audit-tenant 0.1.2 (go install build: no commit or date recorded)\n" {
		t.Errorf("version printed %q", out.String())
	}
	out.Reset()
	cli.Main([]string{"version"}, cli.BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}, genericiooptions.IOStreams{In: strings.NewReader(""), Out: &out, ErrOut: &out})
	if out.String() != "audit-tenant dev (commit none, built unknown)\n" { // go run: not a go install build
		t.Errorf("version printed %q", out.String())
	}
}
