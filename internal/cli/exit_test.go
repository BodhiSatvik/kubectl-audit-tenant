package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/testutil"
)

// clusterWithSSAR is cluster(t) with a custom answer to every SelfSubjectAccessReview.
func clusterWithSSAR(t *testing.T, ssar http.HandlerFunc) *testutil.Server {
	t.Helper()
	return testutil.NewServer(t, unreadable(map[string]http.HandlerFunc{
		"GET /version": testutil.JSON(http.StatusOK, `{"gitVersion":"v1.37.0"}`),
		"POST /apis/authentication.k8s.io/v1/selfsubjectreviews":      testutil.JSON(http.StatusCreated, `{"status":{"userInfo":{"username":"kind-dev"}}}`),
		"GET /api/v1/namespaces/tenant-a":                             testutil.JSON(http.StatusOK, `{"metadata":{"name":"tenant-a"}}`),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": ssar,
	}))
}

// nthSSAR answers the first n SSARs with first and the rest with then.
func nthSSAR(n int, first, then http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	count := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		k := count
		mu.Unlock()
		if k <= n {
			first(w, r)
			return
		}
		then(w, r)
	}
}

func TestFailExitsOneWithNoErrorLine(t *testing.T) {
	srv := clusterWithSSAR(t, testutil.SSAR(`{"allowed":true}`))
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""))
	if code != 1 || !regexp.MustCompile(`(?m)^FAIL +AT-01 `).MatchString(stdout) || !strings.Contains(stdout, "exit 1") || stderr != "" { // the verdict column pads to the widest verdict
		t.Errorf("exit %d, stderr %q; want exit 1, a FAIL line and nothing on stderr:\n%s", code, stderr, stdout)
	}
}

func TestAdminHintOnlyInSelfMode(t *testing.T) {
	// Every "may I?" allowed: AT-03 proves the cluster-wide wildcard.
	srv := clusterWithSSAR(t, testutil.SSAR(`{"allowed":true}`))
	const hint = "this identity looks like a cluster admin; add --as to test a tenant"
	if _, stdout, _ := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, "")); !strings.Contains(stdout, hint) {
		t.Errorf("self mode: stdout lacks the admin hint:\n%s", stdout)
	}
	if _, stdout, _ := run(t, "-n", "tenant-a", "--as", "team-a-dev", "--kubeconfig", kubeconfig(t, srv.URL, "")); strings.Contains(stdout, hint) {
		t.Errorf("impersonation: the admin hint is for self mode only:\n%s", stdout)
	}
}

func TestErrorExitsTwoWithNoErrorLine(t *testing.T) {
	srv := clusterWithSSAR(t, nthSSAR(1, testutil.SSAR(`{"allowed":false}`), testutil.JSON(http.StatusInternalServerError, `{}`)))
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""))
	if code != 2 || !strings.Contains(stdout, "ERROR  AT-01") || !strings.Contains(stdout, "exit 2") || stderr != "" {
		t.Errorf("exit %d, stderr %q; want exit 2, an ERROR line and nothing on stderr:\n%s", code, stderr, stdout)
	}
}

// Added by the /ship test coverage audit, pass 1 (Phase 2).
// Value: protects=a SKIP exits 0, and exits 1 with --fail-on-skip (pipelines must not accept an unproven namespace); fails_when=the flag stops reaching report.Summarize;
// why_new=only report tests set FailOnSkip; Phase 1 had no check that could SKIP; seam=none; no_claim=the summary wording (checklist goldens own it)
func TestFailOnSkipTurnsASkipIntoExitOne(t *testing.T) {
	for _, tt := range []struct {
		flags []string
		want  int
	}{{nil, 0}, {[]string{"--fail-on-skip"}, 1}} {
		// Startup's question and create services are denied, update services is allowed: AT-05 is SKIP.
		srv := clusterWithSSAR(t, nthSSAR(2, testutil.SSAR(`{"allowed":false}`), testutil.SSAR(`{"allowed":true}`)))
		code, stdout, stderr := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-05"}, tt.flags...)...)
		if code != tt.want || !regexp.MustCompile(`(?m)^SKIP +AT-05 `).MatchString(stdout) {
			t.Errorf("flags %v: exit %d, stderr %q; want exit %d with AT-05 SKIP:\n%s", tt.flags, code, stderr, tt.want, stdout)
		}
	}
}

func TestJSONReportsTheExitCodeTheProcessReturns(t *testing.T) {
	for _, tt := range []struct {
		flags []string
		want  int
	}{{nil, 0}, {[]string{"--fail-on-skip"}, 1}} {
		// As above: AT-05 is SKIP.
		srv := clusterWithSSAR(t, nthSSAR(2, testutil.SSAR(`{"allowed":false}`), testutil.SSAR(`{"allowed":true}`)))
		code, stdout, stderr := run(t, append([]string{"-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--checks", "AT-05", "-o", "json"}, tt.flags...)...)
		var rep struct {
			APIVersion string `json:"apiVersion"`
			Summary    struct {
				Skip     int `json:"skip"`
				ExitCode int `json:"exitCode"`
			} `json:"summary"`
		}
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil || code != tt.want || rep.Summary.ExitCode != tt.want || rep.Summary.Skip != 1 || rep.APIVersion != "audit-tenant.dev/v1alpha1" {
			t.Errorf("flags %v: exit %d, err %v, stderr %q; want exit %d and the same exitCode in the JSON summary:\n%s", tt.flags, code, err, stderr, tt.want, stdout)
		}
	}
}

// Added by the /ship test coverage audit, pass 1.
// Value: protects=--timeout bounds a run whose API server stops answering: partial report, ERROR "not run: timeout after 1s", exit 2;
// fails_when=the CLI stops applying --timeout to the run context or passes another value to the runner; why_new=deadline tests build their own context, not the flag's;
// seam=none; no_claim=--request-timeout, or a hang inside a credential plugin (cluster tests own that)
func TestTimeoutEndsARunWhoseServerStopsAnswering(t *testing.T) {
	srv := clusterWithSSAR(t, nthSSAR(1, testutil.SSAR(`{"allowed":false}`), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // read the body, or the server never notices the client leaving
		select {
		case <-r.Context().Done(): // the run's deadline cut the request
		case <-time.After(5 * time.Second): // a run that ignores --timeout gets an error here instead of hanging the test
			http.Error(w, "the test server gave up", http.StatusInternalServerError)
		}
	}))
	code, stdout, stderr := run(t, "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--timeout", "1s")
	if code != 2 || !strings.Contains(stdout, "ERROR  AT-01") || !strings.Contains(stdout, "not run: timeout after 1s") || !strings.Contains(stdout, "raise --timeout") {
		t.Errorf("exit %d, stderr %q; want exit 2 with AT-01 not run at the deadline:\n%s", code, stderr, stdout)
	}
}

// TestHelperProcess is the child of TestInterruptPrintsAPartialReportAndExitsTwo: it runs the
// real CLI with the arguments after "--".
func TestHelperProcess(_ *testing.T) {
	if os.Getenv("AUDIT_TENANT_HELPER") != "1" {
		return // only the child process (see TestInterruptPrintsAPartialReportAndExitsTwo) goes further
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	os.Exit(cli.Main(args, info, genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}))
}

// interruptedRun runs the CLI in a child process, answers the startup question (list nodes) with
// first, holds the next "may I?" and sends SIGINT while it hangs.
func interruptedRun(t *testing.T, first string) (code int, stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	reached := make(chan struct{})
	var once sync.Once
	srv := clusterWithSSAR(t, nthSSAR(1, testutil.SSAR(first), func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // read the body, or the server never notices the client leaving
		once.Do(func() { close(reached) })
		<-r.Context().Done() // hold the second question until the client gives up
	}))
	child := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "--", "-n", "tenant-a", "--kubeconfig", kubeconfig(t, srv.URL, ""), "--no-color") //nolint:gosec // the test re-executes its own binary as the helper process
	child.Env = append(os.Environ(), "AUDIT_TENANT_HELPER=1")
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	child.Stdout, child.Stderr = stdout, stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		_ = child.Process.Kill()
		t.Fatal("the run never reached the second SelfSubjectAccessReview")
	}
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	return code, stdout, stderr, err
}

func TestInterruptAfterAFailExitsOne(t *testing.T) {
	// D63: a FAIL proven before the signal stands, as it does after --timeout (ADR 0004).
	code, stdout, stderr, err := interruptedRun(t, `{"allowed":true}`)
	if out := stdout.String(); code != 1 || !strings.Contains(out, "RBAC allows list nodes") || !strings.Contains(out, "interrupted") || !strings.Contains(out, "exit 1") {
		t.Errorf("exit %d (%v); want 1: the FAIL proven before the interrupt stands\nstdout:\n%s\nstderr:\n%s", code, err, out, stderr.String())
	}
}

func TestInterruptPrintsAPartialReportAndExitsTwo(t *testing.T) {
	code, stdout, stderr, err := interruptedRun(t, `{"allowed":false}`)
	out := stdout.String()
	if code != 2 || !strings.Contains(out, "ERROR  AT-01") || !strings.Contains(out, "interrupted") || !strings.Contains(out, "re-run; the cluster was not changed") || !strings.Contains(out, "exit 2") {
		t.Errorf("exit %d (%v); want 2 with the partial report:\nstdout:\n%s\nstderr:\n%s", code, err, out, stderr.String())
	}
}
