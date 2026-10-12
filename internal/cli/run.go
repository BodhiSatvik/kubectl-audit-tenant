package cli

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/checks"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cluster"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/report"
	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/safety"
)

type runOptions struct {
	flags          *genericclioptions.ConfigFlags
	output         string
	plain, noColor bool
	checks, skip   []string
	otherNamespace string
	timeout        time.Duration
	failOnSkip     bool
	debug          bool
}

// addRun wires the run flags and RunE onto root: the root command is the run.
func addRun(root *cobra.Command, streams genericiooptions.IOStreams, info BuildInfo) {
	o := &runOptions{flags: genericclioptions.NewConfigFlags(true)}
	o.flags.AddFlags(root.Flags())
	f := root.Flags()
	f.StringVarP(&o.output, "output", "o", "checklist", "output format: checklist")
	f.BoolVar(&o.plain, "plain", false, "ASCII only: no color, no special characters")
	f.BoolVar(&o.noColor, "no-color", false, "no color (NO_COLOR in the environment does the same)")
	f.StringSliceVar(&o.checks, "checks", nil, "run only these check IDs (AT-04 selects its sub-checks; AT-04.2 one of them)")
	f.StringSliceVar(&o.skip, "skip", nil, "skip these check IDs")
	f.StringVar(&o.otherNamespace, "other-namespace", "kube-system", "a namespace the tenant must not reach (AT-02)")
	f.DurationVar(&o.timeout, "timeout", 30*time.Second, "deadline for the whole run")
	f.BoolVar(&o.failOnSkip, "fail-on-skip", false, "exit 1 when any check is SKIP (for pipelines that must not accept an unproven namespace)")
	f.BoolVar(&o.debug, "debug", false, "write every API request to stderr (never request headers)")
	root.RunE = func(cmd *cobra.Command, _ []string) error { return o.run(cmd, streams, info) }
}

func (o *runOptions) run(cmd *cobra.Command, streams genericiooptions.IOStreams, info BuildInfo) error {
	if o.output != "checklist" {
		return exit2("unknown output format %q (choose checklist)", o.output)
	}
	if o.timeout <= 0 {
		return exit2("--timeout must be longer than 0")
	}
	specs, err := checks.Select(o.checks, o.skip)
	if err != nil {
		return exit2("%v", err)
	}
	raw, err := o.flags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return exit2("cannot load the kubeconfig: %v", err)
	}
	contextName := cmp.Or(*o.flags.Context, raw.CurrentContext)
	namespace, user := *o.flags.Namespace, *o.flags.AuthInfoName // --user overrides the context's user
	if kctx := raw.Contexts[contextName]; kctx != nil {
		namespace, user = cmp.Or(namespace, kctx.Namespace), cmp.Or(user, kctx.AuthInfo)
	}
	asUser := *o.flags.Impersonate // or the kubeconfig user's as:, which --as-group adds groups to
	if ai := raw.AuthInfos[user]; ai != nil {
		asUser = cmp.Or(asUser, ai.Impersonate)
	}
	if asUser == "" && (len(*o.flags.ImpersonateGroup) > 0 || *o.flags.ImpersonateUID != "") {
		return exit2("--as-group and --as-uid need --as: the API requires a user with the groups; use `--as audit-probe --as-group <group>` to test a group alone")
	}
	if namespace == "" {
		return exit2("namespace required: pass -n, or set a namespace in context %q", contextName)
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return exit2("invalid namespace %q: a namespace is one DNS label (lowercase letters, digits and dashes)", namespace)
	}
	if o.otherNamespace == namespace {
		return exit2("--other-namespace must differ from -n (%s)", namespace)
	}
	if errs := validation.IsDNS1123Label(o.otherNamespace); len(errs) > 0 {
		return exit2("invalid --other-namespace %q: a namespace is one DNS label (lowercase letters, digits and dashes)", o.otherNamespace)
	}
	cfg, err := o.flags.ToRESTConfig()
	if err != nil {
		return exit2("cannot load the kubeconfig: %v", err)
	}
	// The effective impersonation: the --as flags or the kubeconfig's as and as-groups entries
	// (clientcmd already refuses groups without a user there).
	as, asGroups := cfg.Impersonate.UserName, cfg.Impersonate.Groups

	cfg.UserAgent = "audit-tenant/" + info.Version // what the API audit log shows
	rec := &safety.Recorder{}
	trace := func() { // --debug: one line per request, never headers; bodies of the POST answers indented
		if !o.debug {
			return
		}
		for _, r := range rec.Requests() {
			cmd.PrintErrf("%s %s %d %s\n", r.Method, report.Sanitize(r.Path), r.Status, r.Duration.Round(time.Millisecond))
			if r.Body != "" {
				cmd.PrintErrf("  %s\n", report.Sanitize(r.Body))
			}
		}
	}
	defer trace()
	ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // after the first signal, a second one gets Go's default: exit at once

	c, err := cluster.Connect(ctx, cfg, cluster.Options{
		Namespace: namespace, Context: contextName, User: user,
		As: as, AsGroups: asGroups, Timeout: o.timeout, Recorder: rec,
	})
	if err != nil {
		return exit2("%v", err)
	}
	results := checks.Run(ctx, c, specs, o.timeout)
	if len(results) == 0 {
		return exit2("no check produced a result; this is a bug in audit-tenant")
	}
	rep := report.Report{
		Tool:      report.Tool{Name: "audit-tenant", Version: info.Version},
		Cluster:   report.Cluster{Context: contextName, Server: c.Server, Version: c.Version},
		Namespace: namespace, OtherNamespace: o.otherNamespace,
		Identity:   c.Identity,
		Timestamp:  time.Now().UTC(),
		Results:    results,
		FailOnSkip: o.failOnSkip,
	}
	rep.Summary = report.Summarize(results, o.failOnSkip)
	if errors.Is(ctx.Err(), context.Canceled) {
		rep.Summary.ExitCode = 2 // interrupted: the report is partial
	}
	printer := report.Checklist{Out: streams.Out, Color: report.WantColor(streams.Out, o.noColor), Plain: o.plain, Width: report.Width(streams.Out)}
	if err := printer.Print(rep); err != nil {
		return exit2("cannot write the report: %v", err)
	}
	if rep.Summary.ExitCode == 0 {
		return nil
	}
	return &exitError{code: rep.Summary.ExitCode}
}
