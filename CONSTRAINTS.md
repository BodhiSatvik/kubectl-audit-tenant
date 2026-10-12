# Constraints

Last reviewed: 2026-10-11 by Satvik Bhasin (decisions D30 to D33 in the plan-review session).

Read this file before writing code. Do not weaken it to make a change pass. Tightening is silent; loosening is loud: `hack/floor-guard.py` flags any lowered number, removed rule or new exception in this file.

## Floor (always enforced, no setup required)

- No new lint suppressions without a reason: a `//nolint` must name the rule and the reason (`//nolint:errcheck // closing a read-only body`); no bare `//nolint`, no `#nosec`, no `gitleaks:allow`.
- No skipped or deleted tests without a reason in the commit message: no new `t.Skip`, `t.SkipNow` or `t.Skipf`; no deleted `_test.go` files; no assertions removed from a test that stays.
- No unimplemented stubs in shipped code: no `panic("not implemented")`, `panic("TODO")`, `// TODO` or `// FIXME` in non-test Go files. Open work goes to `TODOS.md`.
- No secrets in source (`gitleaks`, redacted output only).
- The safety test (`internal/safety`: allowlist replay of every check plus the deny-list) is never skipped, never short-only, never behind a build tag.
- Every registered check's FAIL, SKIP and ERROR results carry a non-empty hint: a unit test over the check registry asserts it, and the e2e asserts it on real output (DESIGN.md acceptance criterion 6).
- `--debug` output never contains request headers (acceptance criterion 2).
- No names, code, numbers or identifiers internal to the maintainer's employer: `../../scripts/clean-room-check.sh .` passes before every release (maintainer step; the lab script is not in this repo).
- This file does not get weakened to make a change pass.

## Enforced with numbers

| Dimension | Rule | Checked by | Runs at |
|-----------|------|-----------|---------|
| Vet | Zero findings | `go vet ./...` | every edit (`make check-fast`) |
| Lint | Zero issues from `.golangci.yml` | `golangci-lint run` | every edit |
| Architecture | depguard: `internal/headroom` imports no `k8s.io/client-go`; `internal/report` imports no `internal/checks` or `internal/probe`; only `internal/safety` and `internal/cluster` import `k8s.io/client-go/transport` | `golangci-lint run` (depguard rules in `.golangci.yml`) | every edit |
| Secrets | Zero findings | `gitleaks detect --redact --no-banner` | every edit |
| Floor guard | Zero floor violations in the diff against `origin/main` | `python3 hack/floor-guard.py` | task end (`make check`), CI |
| Coverage | At least 80% of statements in every package under `internal/`; at least 90% for `internal/safety` and `internal/probe` | `go test ./... -coverprofile=coverage.out` then `hack/coverage-gate.sh` | task end, CI |
| Safety invariant | Zero requests outside the allowlist; zero Secret bodies; every deny-list request refused | `go test ./internal/safety/... -run 'TestSafety' -count=1 -v` | task end, CI |
| Security: dependencies | No known vulnerability reachable from the binary | `govulncheck ./...` | CI (`make check-full`) |
| Integration | AT-01 to AT-03 PASS and FAIL fixtures under real RBAC; `--as-group` with no user to impersonate exits 2 | `make envtest` (controller-runtime envtest 1.37.x) | CI |
| End-to-end | Exactly the verdict table in DESIGN.md section 8 for all four runs; JSON schema valid; PolicyReport applies with `--dry-run=server` | `make e2e` (kind + Kyverno, versions in `test/e2e/versions.env`) | CI |
| Performance | Median of 5 full runs on kind under 10 s; no e2e run makes more than 40 API requests | `make bench` inside the e2e job; writes `BENCHMARK.md` | CI |
| Release | Five archives and `checksums.txt`; krew installs in clean roots on ubuntu and macos; `--manifest-url` install works; `version` prints the tag | release workflow (goreleaser) | on tag |

Every row names the command that produces the verdict. A dimension with a number and no command is an aspiration, not a constraint.

## Measured, not yet enforced

| Metric | Today | Direction |
|--------|-------|-----------|
| Project statement coverage (all packages) | not measured yet (no code); record at the first green CI run | must not fall (0.5% tolerance) |
| Binary size, linux/amd64 | record at v0.1.0 | must not grow more than 10% per minor release without a note in CHANGELOG |
| `make check` wall time on the maintainer's Mac | record at the first green run | stays under 90 s |

## Stages and budgets

| Stage | Command | Runs | Budget |
|-------|---------|------|--------|
| fast | `make check-fast` | vet, lint (with depguard), gitleaks | under 10 s |
| task | `make check` | fast, then unit tests with the coverage gate, the safety test, the floor guard | under 90 s |
| full | `make check-full` | task, then govulncheck, envtest, e2e with bench, clean-room script when present | CI, minutes |

Policy (D31): block everywhere. Every stage exits non-zero on a failure; CI blocks the merge; `--no-verify` is not used. An unreachable threshold is fixed by a test or routed through a tracked exception, never by editing the number.

## Exceptions

| ID | Rule | Path | Reason | Owner | Expires |
|----|------|------|--------|-------|---------|
| (none yet) | | | | | |

An exception needs an owner and an expiry no more than 90 days out.
