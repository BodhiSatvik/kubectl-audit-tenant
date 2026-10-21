# Stages from CONSTRAINTS.md. fast: every edit. check: task end. check-full: CI.
SHELL := bash
.SHELLFLAGS := -o pipefail -ec
GO ?= go
PKG := ./...
SETUP_ENVTEST ?= $(GO) run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.2
# The envtest binaries and the release index that names them and their checksums, both pinned; bump on purpose (D162).
ENVTEST_K8S := 1.37.0
ENVTEST_INDEX := https://raw.githubusercontent.com/kubernetes-sigs/controller-tools/1031496fc98a4f51010c3bdfdeb57b5d67bea7bd/envtest-releases.yaml

.PHONY: check-fast check check-full lint vet secrets unit coverage-gate safety floor-guard vuln envtest e2e bench e2e-delete clean-room

check-fast: vet lint secrets
check: check-fast unit safety floor-guard
check-full: check vuln envtest clean-room # then e2e and bench in order: bench needs the cluster e2e leaves
	$(MAKE) e2e E2E_DELETE=
	$(MAKE) bench || { $(MAKE) e2e-delete; exit 1; }
	$(MAKE) e2e-delete
# Its own target: make -n runs every recipe line that holds $(MAKE), so a delete on such a line would run.
e2e-delete:
	@if [ "$(E2E_DELETE)" = 1 ]; then kind delete cluster --name "$${E2E_CLUSTER:-audit-tenant-e2e}"; fi

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
	python3 -I hack/floor-guard.py --self-test && python3 -I hack/floor-guard.py # -I: no module from the checkout can stand in for the standard library
vuln:
	govulncheck $(PKG)
envtest: # the API server and etcd binaries from setup-envtest; the build tag keeps the test out of make unit
	assets="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S) --index $(ENVTEST_INDEX) -p path)" && KUBEBUILDER_ASSETS="$$assets" $(GO) test -tags envtest ./test/envtest/... -count=1 -v
e2e:
	test/e2e/run.sh
bench:
	test/e2e/bench.sh --self-test && test/e2e/bench.sh
clean-room:
	@if [ -x ../../scripts/clean-room-check.sh ]; then ../../scripts/clean-room-check.sh . ; else echo "clean-room: lab script not present here (maintainer step)"; fi
