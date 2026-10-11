package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/checks"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
)

type checkJSON struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Kind     report.Kind     `json:"kind"`
	MTB      string          `json:"mtb"`
	Severity report.Severity `json:"severity"`
	Method   string          `json:"method"`
	Results  []resultJSON    `json:"results"`
}

type resultJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	MTB  string `json:"mtb"`
}

func newListChecks(streams genericiooptions.IOStreams) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list-checks",
		Short: "List the checks: ID, kind, MTB ID, severity, name and method",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			switch output {
			case "table":
				var b strings.Builder
				tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
				for _, s := range checks.Registry {
					if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Kind, mtb(s), s.Severity, s.Name, s.Method); err != nil {
						return err
					}
				}
				if err := tw.Flush(); err != nil {
					return err
				}
				_, err := io.WriteString(streams.Out, b.String())
				return err
			case "json":
				out := make([]checkJSON, 0, len(checks.Registry))
				for _, s := range checks.Registry {
					c := checkJSON{ID: s.ID, Name: s.Name, Kind: s.Kind, MTB: mtb(s), Severity: s.Severity, Method: s.Method}
					for _, r := range s.Results {
						c.Results = append(c.Results, resultJSON{ID: r.ID, Name: r.Name, MTB: r.MTB})
					}
					out = append(out, c)
				}
				enc := json.NewEncoder(streams.Out)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
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
