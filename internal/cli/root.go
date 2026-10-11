// Package cli holds the cobra commands: the run (root), list-checks and version.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

// BuildInfo is set by goreleaser through -ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// exitError carries the process exit code. An empty msg means the output already explained it.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func exit2(format string, args ...any) error {
	return &exitError{code: 2, msg: fmt.Sprintf(format, args...)}
}

// Main runs the command line and returns the process exit code (0, 1 or 2; ADR 0004).
func Main(args []string, info BuildInfo, streams genericiooptions.IOStreams) int {
	cmd := New(info, streams)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exitError](err); ok {
		if ee.msg != "" {
			cmd.PrintErrln("error:", report.Sanitize(ee.msg)) // startup errors carry server text
		}
		return ee.code
	}
	cmd.PrintErrf("error: %s\nrun 'kubectl audit-tenant --help' for the flags\n", report.Sanitize(err.Error()))
	return 2
}

// New builds the command tree. Usage lines are prefixed with "kubectl" so help reads as the
// user types it.
func New(info BuildInfo, streams genericiooptions.IOStreams) *cobra.Command {
	root := &cobra.Command{
		Use:   "audit-tenant",
		Short: "Prove a namespace meets a tenant governance baseline",
		Long: `kubectl audit-tenant runs ten tenant-governance checks as the tenant identity against one
namespace and prints one verdict per check: PASS, FAIL, SKIP or ERROR, with the reason and a
hint. It never changes the cluster: it asks "may I?" (SelfSubjectAccessReview) and rehearses
creates with server-side dry-run.

Exit code: 0 no FAIL and no ERROR; 1 a FAIL (or a SKIP with --fail-on-skip); 2 an ERROR or
the run could not start. --timeout bounds the whole run; kubectl's --request-timeout bounds
each single request, and the shorter one wins.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetUsageTemplate(strings.NewReplacer("{{.UseLine}}", "kubectl {{.UseLine}}", "{{.CommandPath}}", "kubectl {{.CommandPath}}").Replace(root.UsageTemplate()))
	root.SetOut(streams.Out)
	root.SetErr(streams.ErrOut)
	root.SetIn(streams.In)
	addRun(root, streams, info)
	root.AddCommand(newListChecks(streams), newVersion(streams, info))
	return root
}
