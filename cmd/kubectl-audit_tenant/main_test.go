package main

import (
	"runtime/debug"
	"testing"

	"github.com/BodhiSatvik/kubectl-audit-tenant/internal/cli"
)

func TestFromBuildInfo(t *testing.T) {
	dev := cli.BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}
	vcs := []debug.BuildSetting{{Key: "vcs.revision", Value: "305fd695e539c87f"}, {Key: "vcs.time", Value: "2026-10-12T01:52:23Z"}}
	tests := []struct {
		name string
		in   cli.BuildInfo
		bi   debug.BuildInfo
		want cli.BuildInfo
	}{
		{"goreleaser ldflags win", cli.BuildInfo{Version: "0.1.0", Commit: "abc1234", Date: "d"}, debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}, Settings: vcs},
			cli.BuildInfo{Version: "0.1.0", Commit: "abc1234", Date: "d"}},
		{"go install names the module version", dev, debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			cli.BuildInfo{Version: "0.1.0", Commit: "none", Date: "unknown"}},
		{"go build in a checkout names the pseudo-version and commit", dev, debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261012015223-305fd695e539+dirty"}, Settings: vcs},
			cli.BuildInfo{Version: "0.0.0-20261012015223-305fd695e539+dirty", Commit: "305fd69", Date: "2026-10-12T01:52:23Z"}},
		{"-buildvcs=false keeps dev", dev, debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, dev},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromBuildInfo(tt.in, &tt.bi); got != tt.want {
				t.Errorf("fromBuildInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
