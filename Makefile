# Stages from CONSTRAINTS.md. fast: every edit. check: task end. check-full: CI.
SHELL := bash
.SHELLFLAGS := -o pipefail -ec
GO ?= go
PKG := ./...
SETUP_ENVTEST ?= $(GO) run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.2

.PHONY: check-fast check check-full lint vet secrets unit coverage-gate safety floor-guard vuln envtest e2e bench clean-room

check-fast: vet lint secrets
check: check-fast unit safety floor-guard
check-full: check vuln envtest e2e clean-room

vet:
	$(GO) vet $(PKG)
lint:
	golangci-lint run
secrets:
	gitleaks detect --redact --no-banner
unit:
	set -o pipefail; $(GO) test $(PKG) -race -coverprofile=coverage.out -covermode=atomic 2>&1 | tee test.log # in the recipe: make 3.81 (macOS) ignores .SHELLFLAGS
	hack/coverage-gate.sh test.log
safety:
	$(GO) test ./internal/safety/... -run 'TestSafety' -count=1 -v
floor-guard:
	python3 hack/floor-guard.py --self-test && python3 hack/floor-guard.py
vuln:
	govulncheck $(PKG)
envtest: # the API server and etcd binaries from setup-envtest; the build tag keeps the test out of make unit
	assets="$$($(SETUP_ENVTEST) use 1.37.x -p path)" && KUBEBUILDER_ASSETS="$$assets" $(GO) test -tags envtest ./test/envtest/... -count=1 -v
e2e:
	test/e2e/run.sh
bench:
	test/e2e/bench.sh
clean-room:
	@if [ -x ../../scripts/clean-room-check.sh ]; then ../../scripts/clean-room-check.sh . ; else echo "clean-room: lab script not present here (maintainer step)"; fi
