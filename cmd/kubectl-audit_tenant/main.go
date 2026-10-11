// Command kubectl-audit_tenant is the kubectl plugin binary. The underscore
// follows the krew naming rule: kubectl runs it as "kubectl audit-tenant".
package main

import (
	"fmt"

	// Cloud login plugins (exec, oidc) so kubeconfigs from managed clusters work.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// Set by goreleaser through -ldflags; "dev" when built with plain go build.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	fmt.Printf("audit-tenant %s (commit %s, built %s)\n", version, commit, date)
}
