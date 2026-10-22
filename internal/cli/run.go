package cli

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
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

// defaultOtherNamespace exists on every cluster; any other --other-namespace gets a warning (D141).
const defaultOtherNamespace = "kube-system"

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
	f.StringVarP(&o.output, "output", "o", "checklist", "output format: checklist, wide (adds MTB IDs and deciding layers, never cuts a reason), json or policyreport (a wgpolicyk8s.io/v1alpha2 PolicyReport to apply); every format keeps the exit code, so a report with a FAIL exits 1")
	f.BoolVar(&o.plain, "plain", false, "ASCII only: no color, no special characters")
	f.BoolVar(&o.noColor, "no-color", false, "no color (NO_COLOR in the environment does the same)")
	f.StringSliceVar(&o.checks, "checks", nil, "run only these check IDs (AT-04 selects its sub-checks; AT-04.2 one of them)")
	f.StringSliceVar(&o.skip, "skip", nil, "skip these check IDs")
	f.StringVar(&o.otherNamespace, "other-namespace", defaultOtherNamespace, "a namespace the tenant must not reach (AT-02)")
	f.DurationVar(&o.timeout, "timeout", 30*time.Second, "deadline for the whole run")
	f.BoolVar(&o.failOnSkip, "fail-on-skip", false, "exit 1 when any check is SKIP (for pipelines that must not accept an unproven namespace)")
	f.BoolVar(&o.debug, "debug", false, "write every API request to stderr (never request headers)")
	root.RunE = func(cmd *cobra.Command, _ []string) error { return o.run(cmd, streams, info) }
}

func (o *runOptions) run(cmd *cobra.Command, streams genericiooptions.IOStreams, info BuildInfo) error {
	if !slices.Contains([]string{"checklist", "wide", "json", "policyreport"}, o.output) {
		return exit2("unknown output format %q (choose checklist, wide, json or policyreport)", o.output)
	}
	if o.timeout <= 0 {
		return exit2("--timeout must be longer than 0")
	}
	specs, err := checks.Select(o.checks, o.skip)
	if err != nil {
		return exit2("%v", err)
	}
	every, _ := checks.Select(nil, nil) // every check this build runs
	ran, all := len(checks.ResultIDs(specs)), len(checks.ResultIDs(every))
	if ran < all && o.output == "policyreport" { // D147: dashboards show results, not annotations
		return exit2("-o policyreport needs every check: a report from --checks or --skip would replace the identity's full report; drop those flags")
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
		return exit2("--as-group and --as-uid need a user to impersonate (--as, or as: in the kubeconfig user): the API requires a user with the groups; use `--as audit-probe --as-group <group>` to test a group alone")
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
	var warnings []string // D141, D146, D147: what the flags leave untested; no request needed
	saNamespace, sa := strings.CutPrefix(as, "system:serviceaccount:")
	saNamespace, _, _ = strings.Cut(saNamespace, ":")
	switch missing := slices.DeleteFunc([]string{"system:serviceaccounts", "system:serviceaccounts:" + saNamespace}, func(g string) bool { return slices.Contains(asGroups, g) }); {
	case as != "" && !sa && len(asGroups) == 0:
		warnings = append(warnings, "impersonating "+as+" with no groups (--as-group, or as-groups in the kubeconfig): the run lacks the groups an identity provider adds, and the API server does not check that the user exists")
	case sa && len(asGroups) > 0 && len(missing) > 0: // the API server adds them only when no group is given
		warnings = append(warnings, "impersonating service account "+as+" with groups that leave out "+strings.Join(missing, " and ")+": the API server adds a service account's own groups only when no group is given (--as-group, or as-groups in the kubeconfig)")
	}
	if ran < all {
		warnings = append(warnings, fmt.Sprintf("--checks or --skip ran %d of %d results; the others are not in this report", ran, all))
	}
	if o.otherNamespace != defaultOtherNamespace && slices.ContainsFunc(specs, func(s checks.Spec) bool { return s.ID == "AT-02" }) {
		warnings = append(warnings, "--other-namespace "+o.otherNamespace+" is not checked: AT-02 cannot tell a missing namespace from a forbidden one, so a misspelled name passes")
	}
	impersonation := "no impersonation"
	if as != "" {
		impersonation = fmt.Sprintf("impersonating %s (groups: %s)", as, cmp.Or(strings.Join(asGroups, ", "), "none"))
	}

	cfg.UserAgent = "audit-tenant/" + info.Version // what the API audit log shows
	rec := &safety.Recorder{}
	var c *cluster.Client
	trace := func() { // --debug: the identity steps and one line per request, never headers; bodies of the POST answers indented
		if !o.debug {
			return
		}
		cmd.PrintErrln("identity: " + report.Sanitize(fmt.Sprintf("context %s, kubeconfig user %s, %s", contextName, user, impersonation)))
		for _, r := range rec.Requests() {
			cmd.PrintErrf("%s %s %d %s\n", r.Method, report.Sanitize(r.Path), r.Status, r.Duration.Round(time.Millisecond))
			if r.Body != "" {
				cmd.PrintErrf("  %s\n", report.Sanitize(r.Body))
			}
		}
		if c == nil {
			return // startup failed; the error line says why
		}
		resolved := "no SelfSubjectReview answer named the identity, so the header shows " + c.Identity.Username + " from the kubeconfig and --as, marked unverified"
		if c.Identity.Verified {
			resolved = fmt.Sprintf("the SelfSubjectReview names %s (groups: %s), shown as verified", c.Identity.Username, cmp.Or(strings.Join(c.Identity.Groups, ", "), "none"))
		}
		cmd.PrintErrln("identity: " + report.Sanitize(resolved))
	}
	defer trace()
	ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // after the first signal, a second one gets Go's default: exit at once

	c, err = cluster.Connect(ctx, cfg, cluster.Options{
		Namespace: namespace, OtherNamespace: o.otherNamespace, Context: contextName, User: user,
		As: as, AsGroups: asGroups, Timeout: o.timeout, Recorder: rec,
	})
	if err != nil {
		return exit2("%v", err)
	}
	if o.output == "policyreport" && c.Identity.Mode == report.ModeSelf && !c.Identity.Verified { // D151: the report name needs the identity
		return exit2("-o policyreport needs to know who runs the tool: no SelfSubjectReview answer named the identity, and the kubeconfig names only its user entry %q; use --as, or -o json", c.Identity.Username)
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
		Warnings:   warnings,
		Timestamp:  time.Now().UTC(),
		Results:    results,
		FailOnSkip: o.failOnSkip,
		AdminHint:  c.Identity.Mode == report.ModeSelf && checks.LooksLikeAdmin(results),
	}
	if as != "" {
		rep.Request = report.Request{User: as, Groups: asGroups, UID: cfg.Impersonate.UID, Extra: cfg.Impersonate.Extra}
	}
	rep.Summary = report.Summarize(results, o.failOnSkip) // an interrupted run's unfinished checks are ERROR; a proven FAIL still wins (D63)
	switch o.output {
	case "json":
		err = report.JSON(streams.Out, rep)
	case "policyreport":
		err = report.PolicyReport(streams.Out, rep)
	default:
		err = report.Checklist{Out: streams.Out, Color: report.WantColor(streams.Out, o.noColor), Plain: o.plain, Width: report.Width(streams.Out), Wide: o.output == "wide"}.Print(rep)
	}
	if err != nil {
		return exit2("cannot write the report: %v", err)
	}
	if rep.Summary.ExitCode == 0 {
		return nil
	}
	return &exitError{code: rep.Summary.ExitCode}
}
