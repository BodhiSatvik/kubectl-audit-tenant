# Implementation Plan: kubectl audit-tenant v0.1

## Overview
Build the read-only kubectl plugin specified in `DESIGN.md` (approved after plan reviews, 2026-10-11): ten tenant-governance checks run as the tenant identity against one namespace, five output formats, exit codes for CI, unit + envtest + kind e2e, goreleaser + krew release. Tasks live in `TODOS.md` under "v0.1" (the repo's task list target per `CLAUDE.md`); this file holds the phases, checkpoints, risks and the parallel lanes.

## Architecture decisions (where the reasons live)
- Read-only by construction with an exact-path allowlist tested through a real HTTP client: `docs/adr/0001`, DESIGN.md section 4, decisions D10–D13, F19, F23.
- Checks run as the tenant (SSAR + server-side dry-run); controller and update paths that bypass RBAC yield SKIP, never PASS: `docs/adr/0002`, `0005`, D11, D23, F14, F21.
- Probes sized from the namespace LimitRange; control probes for pods and Services: `docs/adr/0005`, D12, D23.
- Headroom is an estimate; incomplete inputs are SKIP: `docs/adr/0006`, D24, F1–F3, F24; Argo Rollouts deferred (D8).
- Four verdicts, 0/1/2 exit codes, opt-in `--fail-on-skip`: `docs/adr/0004`, D22.
- Package layout (Original arrangement, D4): `cmd/`, `internal/{cli,cluster,safety,checks,probe,headroom,report}`, `test/{envtest,e2e}`.
- Quality bar: `CONSTRAINTS.md` (D30–D33). Every task ends with `make check` green.

## Task list (index; cards in TODOS.md)

### Phase 1: walking skeleton
- [x] B1 Module, tooling and CI
- [x] B2 Report types and check registry
- [x] B3 Safety transport
- [x] B4 Cluster client, startup and cache
- [x] B5 Runner
- [x] B6 SSAR helper and AT-01
- [x] B7 CLI command surface
- [x] B8 Checklist printer
- [x] B9 e2e scaffold on kind
- [x] B10 Release pipeline dry run

### Checkpoint 1: walking skeleton
- [ ] `make check` green; e2e asserts AT-01 for all four identities; snapshot release builds; review with Satvik

### Phase 2: behavioral checks
- [x] B11 AT-02 and AT-03
- [x] B12 Probe builders and sizing
- [x] B13 Rejection classifier
- [x] B14 AT-04
- [x] B15 AT-05
- [x] B16 e2e: AT-01 to AT-05

### Checkpoint 2
- [ ] Behavioral checks green on kind for all four identities; coverage gate green

### Phase 3: configuration checks and headroom
- [x] B17 AT-06 and AT-07
- [x] B18 AT-08 and AT-09
- [x] B19 Headroom math
- [x] B20 AT-10
- [x] B21 e2e: all ten checks

### Checkpoint 3
- [x] Full verdict matrix green; `make check` under 90 s (2026-10-20 at 9d44fcb: real e2e with upstream Kyverno matches runs 1-4, 37/37/31/34 requests; `make check` 16.9 s with a cold cache; D117)

### Phase 4: output formats and flags
- [x] B22 `-o wide`
- [x] B23 `-o json` and schema
- [x] B24 `-o policyreport`
- [x] B25 `--debug` and `--fail-on-skip`

### Checkpoint 4
- [x] Five printers golden-tested; schema and PolicyReport pass in e2e (2026-10-21 at cb82f89: goldens for checklist, plain, color, wide and wide-plain, json, policyreport and the --debug stderr trace; e2e runs 1-4 plus -o json validated against the schema and -o policyreport applied with --dry-run=server, 40/40/31/34/40/40 requests; CI check and kind green; D142 to D156)

### Phase 5: integration layer, benchmark and floor tests
- [x] B26 envtest layer
- [x] B27 Benchmark
- [ ] B28 Floor tests over the registry

### Checkpoint 5
- [ ] `make check-full` green in CI; CONSTRAINTS.md measured values recorded

### Phase 6: docs and release
- [ ] B29 README
- [ ] B30 Citation, changelog, diagrams
- [ ] B31 Pre-release gate and v0.1.0 tag (Satvik pushes, tags and opens the krew-index PR)

### Checkpoint 6: complete
- [ ] DESIGN.md acceptance criteria 1–11 met; ready for outreach

## Parallel lanes (from the eng review)
Do B1 and B2 alone (types and registry are the shared contract). Then four lanes: A: B3 → B4 → B5 → B7 (transport, cluster, runner, CLI); B: B12 → B13 (probes, classifier) and B6 → B11 (SSAR checks); C: B19 (headroom, pure) → B17/B18 after B6; D: B8 (checklist printer, report types only). Merge, then B9 and B10 together. Later phases are sequential per lane with one e2e extension per phase. One file per check and a registry written once in B2 keep lanes from colliding in `internal/checks`.

## Risks and mitigations
| Risk | Impact | Mitigation |
|------|--------|------------|
| Premise (platforms that hide Kubernetes have no tenant users) | High | Platform team is the primary user; TASK-48 interviews and the stop rule in DESIGN.md section 10 (undated by Satvik's choice, D25) |
| False PASS from a non-security gate or an untested path | High | Control probes (pods and Services), classifier table tests with real strings, controller/update SSAR branches → SKIP, exact-path allowlist |
| Classifier strings drift across Kubernetes versions | Med | Table tests with real strings; `--debug` for bug reports; weekly 1.28 run (Later) |
| Kyverno webhook not ready in e2e | Med | Readiness wait (Available plus a rejected dry-run Service) before run 1 |
| Hung credential plugin defeats the deadline | Med | Startup on a goroutine under the deadline; exit 2 message; tests with a sleeping helper |
| Date slips | Med | Fallback minimum AT-01, 02, 03, 04, 05, 06, 09 (D28); walking skeleton first so a release path exists from Checkpoint 1 |
| krew index lead time | Low | `--manifest-url` and `go install` paths in the README from day one |

## Open questions
- None. `audit-tenant.dev` was registered on 2026-10-11 (D20).

## How to run the tasks
Use Addy's `/build` for each task: it reads the next unchecked task here, takes the card (acceptance criteria, verify command, files) from `TODOS.md` section v0.1, writes the failing test first, implements, runs `make check`, commits once, and ticks the task in both files. Do not use `/build auto` in Phase 1: it requires a `SPEC.md` path this repo does not use (the spec is `DESIGN.md`), and the skeleton phase wants a human look at each step.
