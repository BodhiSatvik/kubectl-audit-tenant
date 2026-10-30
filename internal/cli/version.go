package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

func newVersion(streams genericiooptions.IOStreams, info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			detail := fmt.Sprintf("commit %s, built %s", info.Commit, info.Date)
			if info.Commit == "none" && info.Date == "unknown" { // go install from the module proxy records neither (S1-F5)
				detail = "go install build: no commit or date recorded"
			}
			_, err := fmt.Fprintf(streams.Out, "audit-tenant %s (%s)\n", info.Version, detail)
			return err
		},
	}
}
