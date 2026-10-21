//go:build envtest

// Package envtest runs AT-01 to AT-03 as the tenant against a real API server with real RBAC
// (DESIGN.md section 8, acceptance criterion 3). `make envtest` fetches the API server and etcd
// binaries with setup-envtest and sets the build tag, so `make check` needs neither.
package envtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

func TestRBACAgainstARealAPIServer(t *testing.T) {
	ctrllog.SetLogger(logr.Discard()) // envtest logs only its own start-up steps
	env := &envtest.Environment{}
	if _, err := env.Start(); err != nil {
		t.Fatalf("start the API server (run `make envtest`, which sets KUBEBUILDER_ASSETS): %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stop the API server: %v", err)
		}
	})
	admin, err := env.AddUser(envtest.User{Name: "envtest-admin", Groups: []string{"system:masters"}}, nil)
	if err != nil {
		t.Fatalf("add the admin user: %v", err)
	}
	apply(t, admin.Config())
	raw, err := admin.KubeConfig()
	if err != nil {
		t.Fatalf("admin kubeconfig: %v", err)
	}
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	const pass, fail = report.Pass, report.Fail
	for _, tc := range []struct {
		user string
		want map[string]report.Verdict
		exit int
	}{
		{"tenant-dev", map[string]report.Verdict{"AT-01": pass, "AT-02": pass, "AT-03": pass}, 0},
		{"at01-fail", map[string]report.Verdict{"AT-01": fail, "AT-02": pass, "AT-03": pass}, 1},
		{"at02-fail", map[string]report.Verdict{"AT-01": pass, "AT-02": fail, "AT-03": pass}, 1},
		{"at03-fail", map[string]report.Verdict{"AT-01": pass, "AT-02": pass, "AT-03": fail}, 1},
		{"cluster-admin-user", map[string]report.Verdict{"AT-01": fail, "AT-02": fail, "AT-03": fail}, 1},
	} {
		t.Run(tc.user, func(t *testing.T) {
			code, out, stderr := run(kubeconfig, "-n", "tenant-a", "--as", tc.user, "--checks", "AT-01,AT-02,AT-03", "-o", "json")
			if code != tc.exit {
				t.Errorf("exit code = %d, want %d; stderr: %s", code, tc.exit, stderr)
			}
			var rep report.Report
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("-o json: %v\n%s", err, out)
			}
			// The SelfSubjectReview under impersonation names the impersonated user.
			if !rep.Identity.Verified || rep.Identity.Username != tc.user {
				t.Errorf("identity = %+v, want %s verified", rep.Identity, tc.user)
			}
			got := map[string]report.Verdict{}
			for _, r := range rep.Results {
				got[r.ID] = r.Verdict
				if r.Layer != "RBAC" || (r.Verdict != pass && r.Hint == "") {
					t.Errorf("%s %s: layer %q, hint %q; want layer RBAC and a hint on every non-PASS", r.ID, r.Verdict, r.Layer, r.Hint)
				}
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("verdicts = %v, want %v\n%s", got, tc.want, out)
			}
		})
	}

	t.Run("--as-group without --as exits 2", func(t *testing.T) {
		code, out, stderr := run(kubeconfig, "-n", "tenant-a", "--as-group", "tenants")
		if code != 2 || out != "" || !strings.Contains(stderr, "--as-group and --as-uid need a user to impersonate") {
			t.Errorf("exit %d, stdout %q, stderr %q; want exit 2, no report and the message", code, out, stderr)
		}
	})
}

// run runs the plugin's command line against the kubeconfig and returns the exit code, stdout and stderr.
func run(kubeconfig string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = cli.Main(append([]string{"--kubeconfig", kubeconfig}, args...), cli.BuildInfo{Version: "envtest"},
		genericiooptions.IOStreams{In: strings.NewReader(""), Out: &out, ErrOut: &errOut})
	return code, out.String(), errOut.String()
}

// apply creates every object in fixtures/rbac.yaml.
func apply(t *testing.T, cfg *rest.Config) {
	t.Helper()
	k8s, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := os.ReadFile("fixtures/rbac.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(fixtures), 4096)
	for {
		var u unstructured.Unstructured
		if err := dec.Decode(&u.Object); errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatalf("fixtures/rbac.yaml: %v", err)
		}
		if err := k8s.Create(t.Context(), &u); err != nil {
			t.Fatalf("create %s %s: %v", u.GetKind(), u.GetName(), err)
		}
	}
}
