package cli

import (
	"cmp"
	"context"
	"errors"
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
	"k8s.io/client-go/rest"

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

// inClusterConfig is the pod's own config; a test replaces it.
var inClusterConfig = rest.InClusterConfig

// restConfig is the flags' config. clientcmd falls back to the pod's config only when no flag changes
// its default, so in a pod with no kubeconfig --as or --request-timeout called http://localhost:8080
// (S5-F4). This makes the same fallback with those flags set aside, then puts them back.
func (o *runOptions) restConfig() (*rest.Config, error) {
	loader := o.flags.ToRawKubeConfigLoader()
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, exit2("cannot load the kubeconfig: %v", err)
	}
	rules, ok := loader.ConfigAccess().(interface{ IsDefaultConfig(*rest.Config) bool })
	bare := rest.CopyConfig(cfg)
	bare.Impersonate, bare.Timeout = rest.ImpersonationConfig{}, 0
	if !ok || !rules.IsDefaultConfig(bare) {
		return cfg, nil
	}
	pod, err := inClusterConfig()
	if errors.Is(err, rest.ErrNotInCluster) { // S1-F6: else the run would call localhost:8080
		return nil, exit2("no kubeconfig: set KUBECONFIG or pass --kubeconfig (the tool reads it as kubectl does); in a pod, it uses the pod's service account")
	}
	if err != nil {
		return nil, exit2("cannot load the pod's in-cluster config: %v", err)
	}
	pod.Impersonate, pod.Timeout = cfg.Impersonate, cfg.Timeout
	return pod, nil
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
	if ran < all && len(o.checks) > 0 && o.output == "policyreport" { // D147: dashboards show results, not annotations; D202: --skip writes skip rows
		return exit2("-o policyreport needs every check: a report from --checks would replace the identity's full report; drop --checks, or use --skip, which writes each skipped check as a skip row")
	}
	raw, err := o.flags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return exit2("cannot load the kubeconfig: %v", err)
	}
	contextName := cmp.Or(*o.flags.Context, raw.CurrentContext)
	namespace, user, clusterName := *o.flags.Namespace, *o.flags.AuthInfoName, *o.flags.ClusterName // --user and --cluster override the context's
	if kctx := raw.Contexts[contextName]; kctx != nil {
		namespace, user, clusterName = cmp.Or(namespace, kctx.Namespace), cmp.Or(user, kctx.AuthInfo), cmp.Or(clusterName, kctx.Cluster)
	}
	asUser := *o.flags.Impersonate // or the kubeconfig user's as:, which --as-group adds groups to
	if ai := raw.AuthInfos[user]; ai != nil {
		asUser = cmp.Or(asUser, ai.Impersonate)
	}
	if asUser == "" && (len(*o.flags.ImpersonateGroup) > 0 || *o.flags.ImpersonateUID != "") {
		return exit2("--as-group and --as-uid need a user to impersonate (--as, or as: in the kubeconfig user): the API requires a user with the groups; use `--as audit-probe --as-group <group>` to test a group alone")
	}
	if slices.Contains(*o.flags.ImpersonateGroup, "") { // S5-F2: an empty group also drops a service account's own groups
		return exit2(`--as-group needs a group name, and one was empty; in a script, pass it only when set: ${GROUP:+--as-group "$GROUP"}`)
	}
	cfg, err := o.restConfig()
	if kc := raw.Clusters[clusterName]; err != nil && kc != nil && strings.Contains(kc.ProxyURL, "@") { // D181: clientcmd's parse error quotes the whole proxy URL
		return exit2("cannot load the kubeconfig: the proxy-url of cluster %q holds user info (a name or password before the @) that does not parse; percent-encode it (a %% is %%25). audit-tenant does not print the URL", clusterName)
	}
	if err != nil {
		return err
	}
	if namespace == "" && contextName == "" {
		return exit2("namespace required: pass -n (no kubeconfig context is set, so none gives a namespace)")
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
	// The effective impersonation: the --as flags or the kubeconfig's as and as-groups entries
	// (clientcmd already refuses groups without a user there).
	as, asGroups := cfg.Impersonate.UserName, cfg.Impersonate.Groups
	var warnings []string // D141, D146, D147: what the flags leave untested, no request needed; D196 adds one from the SelfSubjectReview
	saNamespace, sa := strings.CutPrefix(as, "system:serviceaccount:")
	saNamespace, saName, _ := strings.Cut(saNamespace, ":")
	// The API server's own split: a name with another ":" or an invalid namespace is a plain user to it.
	sa = sa && len(validation.IsDNS1123Label(saNamespace)) == 0 && len(validation.IsDNS1123Subdomain(saName)) == 0
	// system:authenticated alone adds nothing: the API server gives it to every impersonated user.
	groupless := !slices.ContainsFunc(asGroups, func(g string) bool { return g != "system:authenticated" })
	switch missing := slices.DeleteFunc([]string{"system:serviceaccounts", "system:serviceaccounts:" + saNamespace}, func(g string) bool { return slices.Contains(asGroups, g) }); {
	case as != "" && !sa && groupless:
		warnings = append(warnings, "impersonating "+as+" with no groups (--as-group, or as-groups in the kubeconfig): the run lacks the groups an identity provider adds, and the API server does not check that the user exists")
	case sa && len(asGroups) > 0 && len(missing) > 0: // the API server adds them only when no group is given
		warnings = append(warnings, "impersonating service account "+as+" with groups that leave out "+strings.Join(missing, " and ")+": the API server adds a service account's own groups only when no group is given (--as-group, or as-groups in the kubeconfig)")
	}
	switch {
	case ran < all && o.output == "policyreport":
		warnings = append(warnings, fmt.Sprintf("--skip ran %d of %d results; the others are skip rows in this report", ran, all))
	case ran < all:
		warnings = append(warnings, fmt.Sprintf("--checks or --skip ran %d of %d results; the others are not in this report", ran, all))
	}
	if o.otherNamespace != defaultOtherNamespace && slices.ContainsFunc(specs, func(s checks.Spec) bool { return s.ID == "AT-02" }) { // S3-F4: the namespace, not AT-02, is unconfirmed
		warnings = append(warnings, "--other-namespace "+o.otherNamespace+": the run cannot confirm that this namespace exists (AT-02 reads a missing namespace and a forbidden one alike), so a misspelled name passes")
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
		entry := fmt.Sprintf("only its user entry %q", c.Identity.Username)
		if c.Identity.Username == "" {
			entry = "no user entry (an in-cluster run)"
		}
		return exit2("-o policyreport needs to know who runs the tool: no SelfSubjectReview answer named the identity, and the kubeconfig names %s; re-run if the cluster was busy (Kubernetes 1.28 or newer answers it), or use --as or -o json", entry)
	}
	var proxied []string // D196: what shows that something between the tool and the API server changed the impersonation
	switch {
	case c.Identity.Mode == report.ModeImpersonation && c.Identity.Verified:
		asked := slices.Clone(asGroups) // plus the groups the API server's impersonation adds itself
		if sa && len(asGroups) == 0 {
			asked = append(asked, "system:serviceaccounts", "system:serviceaccounts:"+saNamespace)
		}
		switch {
		case as == "system:anonymous":
			asked = append(asked, "system:unauthenticated")
		case !slices.Contains(asked, "system:unauthenticated"):
			asked = append(asked, "system:authenticated")
		}
		added := slices.DeleteFunc(slices.Clone(c.Identity.Groups), func(g string) bool { return slices.Contains(asked, g) })
		slices.Sort(added)
		if added = slices.Compact(added); len(added) > 0 { // an access proxy such as Teleport adds its role's groups
			proxied = append(proxied, "the cluster gives "+c.Identity.Username+" groups that the run did not ask for: "+strings.Join(added, ", "))
			warnings = append(warnings, proxied[len(proxied)-1]+"; an access proxy such as Teleport adds its own role's groups to the impersonated user, so the results describe the identity with those groups")
		}
		// The API server never drops an asked group or changes the user; a proxy that ignored --as does.
		if c.Identity.Username != as {
			proxied = append(proxied, "the cluster names the identity "+c.Identity.Username+", not the --as user "+as)
			warnings = append(warnings, proxied[len(proxied)-1]+": an access proxy may have replaced or ignored the impersonation, so the results describe "+c.Identity.Username)
		}
		if dropped := slices.DeleteFunc(slices.Clone(asGroups), func(g string) bool { return slices.Contains(c.Identity.Groups, g) }); len(dropped) > 0 {
			proxied = append(proxied, "the cluster does not give "+c.Identity.Username+" these groups the run asked for: "+strings.Join(dropped, ", "))
			warnings = append(warnings, proxied[len(proxied)-1]+"; an access proxy may have dropped them, so the results describe the identity without them")
		}
	case c.Identity.Mode == report.ModeImpersonation:
		warnings = append(warnings, "the cluster did not confirm the impersonated identity (no SelfSubjectReview answer), so the run cannot see whether an access proxy changed its user or groups")
	}
	if o.output == "policyreport" && len(proxied) > 0 { // D203: the results describe another identity, and the report would replace this one's
		return exit2("-o policyreport needs results that describe the identity the flags name, and %s; fix the access proxy's role or impersonation, or use -o json", strings.Join(proxied, "; "))
	}
	results := checks.Run(ctx, c, specs, o.timeout)
	if len(results) == 0 {
		return exit2("no check produced a result; this is a bug in audit-tenant")
	}
	verdict := func(v report.Verdict) func(report.Result) bool {
		return func(r report.Result) bool { return r.Verdict == v }
	}
	if o.output == "policyreport" && ctx.Err() != nil && slices.ContainsFunc(results, verdict(report.Error)) && !slices.ContainsFunc(results, verdict(report.Fail)) {
		// D175: as D147, a partial report would replace the identity's full one. D182: a stopped run that
		// proved a FAIL prints, since its FAIL can only make the dashboard look worse. D178: the refusal
		// keeps where leftovers may be (D103); D185: and the SKIPs that make --fail-on-skip exit 1.
		stopped := "was interrupted"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			stopped = "hit --timeout " + o.timeout.String()
		}
		msg := "-o policyreport needs a finished run, and this run " + stopped + ": a partial report would replace the identity's full report; re-run, with a longer --timeout if it timed out"
		if o.failOnSkip {
			var skipped []string
			for _, r := range results {
				if r.Verdict == report.Skip {
					skipped = append(skipped, r.ID)
				}
			}
			if len(skipped) > 0 {
				msg += "; before it stopped, it proved SKIP for " + strings.Join(skipped, ", ") + ", which --fail-on-skip counts as a failure"
			}
		}
		if c.Unverified.Load() {
			msg += "; " + checks.LeftoverHint(c.Namespace)
		}
		return &exitError{code: report.Summarize(results, o.failOnSkip).ExitCode, msg: msg}
	}
	if o.output == "policyreport" && report.Summarize(results, false).NothingProven() { // D203: SKIP rows would replace the identity's FAIL rows
		return exit2("-o policyreport needs a run that proved something: every check was SKIP, and the report would replace the identity's report, FAIL rows included; read the SKIP reasons with -o wide (a misspelled -n, --as or --as-group skips every check)")
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
	if o.output == "policyreport" { // D202: one row per result, so a check skipped on purpose shows as skip, not as a missing row
		ranIDs := checks.ResultIDs(specs)
		for _, s := range every {
			for _, r := range s.Results {
				if !slices.Contains(ranIDs, r.ID) {
					rep.SkippedByFlag = append(rep.SkippedByFlag, report.Result{ID: r.ID, Name: r.Name, Kind: s.Kind, MTB: r.MTB, Severity: s.Severity,
						Verdict: report.Skip, Layer: "-", Reason: "skipped with --skip: this run did not test it", Hint: "drop --skip " + r.ID + " to test it"})
				}
			}
		}
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
	if o.output == "json" || o.output == "policyreport" { // S5-F3: inside the document alone, a pipeline log never shows them
		for _, w := range warnings {
			cmd.PrintErrln("warning: " + report.Sanitize(w))
		}
	}
	var msgs []string
	if rep.Summary.NothingProven() { // D197: on stderr, so a pipeline log shows it whatever stdout goes to
		msgs = append(msgs, "nothing was proven: every check was SKIP; read the SKIP reasons (a misspelled -n, --as or --as-group skips every check)")
	}
	if o.output == "policyreport" && c.Unverified.Load() { // D182: stdout goes to kubectl apply, so stderr is what a person reads
		msgs = append(msgs, checks.LeftoverHint(c.Namespace))
	}
	if len(msgs) > 0 {
		return &exitError{code: rep.Summary.ExitCode, msg: strings.Join(msgs, "; ")}
	}
	if rep.Summary.ExitCode == 0 {
		return nil
	}
	return &exitError{code: rep.Summary.ExitCode}
}
