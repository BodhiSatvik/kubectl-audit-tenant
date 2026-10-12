// Command kubectl-audit_tenant is the kubectl plugin binary. The underscore
// follows the krew naming rule: kubectl runs it as "kubectl audit-tenant".
package main

import (
	"os"
	"runtime/debug"
	"strings"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	// Cloud login plugins (exec, oidc) so kubeconfigs from managed clusters work.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
)

// Set by goreleaser through -ldflags; "dev" otherwise, then filled from the Go build info.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	streams := genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	b := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	if bi, ok := debug.ReadBuildInfo(); ok {
		b = fromBuildInfo(b, bi)
	}
	os.Exit(cli.Main(os.Args[1:], b, streams))
}

// fromBuildInfo fills a "dev" build from what the Go toolchain embeds, so `version` and the
// User-Agent name a release: go install @tag gives the module version; go build in a checkout
// gives a pseudo-version, the commit and its time.
func fromBuildInfo(b cli.BuildInfo, bi *debug.BuildInfo) cli.BuildInfo {
	if b.Version != "dev" {
		return b // goreleaser set the ldflags
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		b.Version = strings.TrimPrefix(v, "v") // goreleaser's versions carry no "v" either
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value[:min(7, len(s.Value))]
		case "vcs.time":
			b.Date = s.Value
		}
	}
	return b
}
