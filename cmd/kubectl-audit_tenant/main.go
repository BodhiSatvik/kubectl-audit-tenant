// Command kubectl-audit_tenant is the kubectl plugin binary. The underscore
// follows the krew naming rule: kubectl runs it as "kubectl audit-tenant".
package main

import (
	"os"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	// Cloud login plugins (exec, oidc) so kubeconfigs from managed clusters work.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
)

// Set by goreleaser through -ldflags; "dev" when built with plain go build.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	streams := genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	os.Exit(cli.Main(os.Args[1:], cli.BuildInfo{Version: version, Commit: commit, Date: date}, streams))
}
