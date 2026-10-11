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
			_, err := fmt.Fprintf(streams.Out, "audit-tenant %s (commit %s, built %s)\n", info.Version, info.Commit, info.Date)
			return err
		},
	}
}
