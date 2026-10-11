package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/checks"
)

func newListChecks(streams genericiooptions.IOStreams) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list-checks",
		Short: "List the checks: ID, kind, MTB ID, severity, name and method",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			switch output {
			case "table":
				tw := tabwriter.NewWriter(streams.Out, 0, 0, 2, ' ', 0)
				for _, s := range checks.Registry {
					if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Kind, mtb(s), s.Severity, s.Name, s.Method); err != nil {
						return err
					}
				}
				return tw.Flush()
			case "json":
				enc := json.NewEncoder(streams.Out)
				enc.SetIndent("", "  ")
				return enc.Encode(checks.Registry)
			}
			return exit2("unknown output format %q (choose table or json)", output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table or json")
	return cmd
}

// mtb joins the MTB IDs of a check's results; AT-04 has one per sub-check.
func mtb(s checks.Spec) string {
	ids := make([]string, 0, len(s.Results))
	for _, r := range s.Results {
		ids = append(ids, r.MTB)
	}
	return strings.Join(ids, ",")
}
