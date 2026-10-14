# audit-tenant: TODOs

Build list for this repo. Lab-level work (outreach, pilots, evidence) stays on the lab board (`../../backlog/`).

## v0.1

Build list from the approved spec (`DESIGN.md`, approved after plan reviews 2026-10-11) and the plan reviews' task lists. Order is the build order: a walking skeleton first, then checks by cost, then output formats, then the full test matrix, then docs and release. Phases and checkpoints: `tasks/plan.md`. Definition of done for every task: `make check` passes (CONSTRAINTS.md) and the task's own acceptance criteria hold. No effort numbers by Satvik's rule. Tasks marked (Satvik) are human steps.

### Phase 1: walking skeleton (one check end to end)

- [x] **B1 Module, tooling and CI.** Create `go.mod` (`github.com/BodhiSatvik/kubectl-audit-tenant`, `go 1.26`), pin `k8s.io/{client-go,api,apimachinery,cli-runtime}` v0.37.x and `github.com/spf13/cobra`; an empty `cmd/kubectl-audit_tenant/main.go` that prints `version`; `hack/check-public.sh` (fails on unfilled `{{...}}`); `.github/workflows/ci.yml` running `make check` plus `govulncheck`.
  - AC: `make check-fast` passes locally; CI is green on push; `hack/check-public.sh` fails on a planted placeholder and passes on the tree.
  - Verify: `make check-fast`; CI run.
  - Depends on: none. Files: `go.mod`, `go.sum`, `cmd/kubectl-audit_tenant/main.go`, `hack/check-public.sh`, `.github/workflows/ci.yml`.
- [x] **B2 Report types and check registry.** `internal/report`: `Verdict`, `Result` (id, name, kind, mtb, severity, verdict, layer, reason, hint), `Report`, `Summary`, exit-code function (0/1/2 and `--fail-on-skip`). `internal/checks`: `Spec` with a `Run` function field (the check interface), fixed registry of the ten checks (IDs, names, kinds, MTB IDs, severities, 14 result IDs), selection by `--checks`/`--skip` (families and sub-checks; unknown ID and empty set are errors).
  - AC: exit-code table test incl. `--fail-on-skip`; selection tests; registry test asserts ten checks and fourteen result IDs in order.
  - Verify: `go test ./internal/report/... ./internal/checks/...`.
  - Depends on: B1. Files: `internal/report/types.go`, `internal/report/exit.go`, `internal/checks/registry.go`, `internal/checks/select.go`, tests.
- [x] **B3 Safety transport.** `internal/safety.Transport`: exact-path allowlist (DESIGN.md section 4), refusal error for everything else, request recorder (method, path, status, duration, body for SSAR and dry-run responses). Test harness `internal/testutil`: `httptest.Server` with canned responses and a real clientset built through the transport.
  - AC: `TestSafety` replays a scripted request set and asserts only allowlisted requests pass; the deny-list (pod `exec` GET, secrets GET and LIST, PATCH, DELETE, POST without `dryRun=All`, another namespace) is refused before any HTTP call; coverage of `internal/safety` is at least 90%.
  - Verify: `make safety`; `hack/coverage-gate.sh`.
  - Depends on: B1. Files: `internal/safety/transport.go`, `internal/safety/recorder.go`, `internal/safety/transport_test.go`, `internal/testutil/server.go`.
- [x] **B4 Cluster client, startup and cache.** `internal/cluster`: client from cli-runtime flags with `WrapTransport`, QPS 50 / burst 300; startup on a goroutine under the run deadline: `/version` (unreachable, 401), SelfSubjectReview (403/404 → unverified), namespace GET (404 exit 2; 403 cached), `cannot impersonate` 403 message, first-SSAR 403 hint; identity struct for the header; per-run cache for resourcequotas, limitranges and the namespace.
  - AC: httptest cases for 401, `cannot impersonate`, namespace 404, a never-answering server and a sleeping credential helper all exit 2 with the DESIGN.md messages; identity shows `(unverified)` on 403.
  - Verify: `go test ./internal/cluster/...`.
  - Depends on: B3. Files: `internal/cluster/client.go`, `internal/cluster/startup.go`, tests (identity and the per-run cache live in these two files, D45).
- [x] **B5 Runner.** Sequential execution in ID order under one context; ERROR "not run: timeout after <t>" for unfinished checks at the deadline; SIGINT/SIGTERM → ERROR "interrupted" with hint, partial report, second signal exits; a check's API error makes that check ERROR and the run continues; every selected check always yields its results.
  - AC: cancel-mid-run test (after a FAIL and midway through a multi-result check); deadline test with a blocked httptest handler; ordering test.
  - Verify: `go test ./internal/checks/ -run Runner`.
  - Depends on: B2, B3. Files: `internal/checks/runner.go`, `internal/checks/runner_test.go`.
- [x] **B6 SSAR helper and AT-01.** `can(ctx, verb, group, resource, namespace)` reading answers per D10 as amended by D52 (allowed; allowed=false denied; explicit deny adds a note; evaluationError on a not-allowed answer → ERROR, allowed wins; an answer that does not echo the question → ERROR) and a positive control question (D55). AT-01: four cluster-scoped SSARs with no namespace; PASS/FAIL reason and hint.
  - AC: SSAR table test with the four response shapes; AT-01 PASS and FAIL cases; reason names the allowed action on FAIL.
  - Verify: `go test ./internal/checks/ -run 'SSAR|AT01'`.
  - Depends on: B5. Files: `internal/checks/ssar.go`, `internal/checks/at01.go`, tests.
- [x] **B7 CLI command surface.** cobra root `kubectl audit-tenant`: `-n` (default from context, else exit 2), `--as`/`--as-group` (group without user → exit 2), `-o`, `--plain`, `--no-color`, `--checks`, `--skip`, `--other-namespace`, `--timeout`, `--fail-on-skip`, `--debug`; cli-runtime kubeconfig flags; `list-checks [-o table|json]`; `version`; help text starts with `kubectl audit-tenant`; `--request-timeout` note.
  - AC: flag-validation table test (every exit-2 case in DESIGN.md section 6); `list-checks` prints ten lines; `version` prints the ldflags values.
  - Verify: `go test ./internal/cli/...`; `go run ./cmd/kubectl-audit_tenant --help`.
  - Depends on: B2, B4. Files: `internal/cli/root.go`, `internal/cli/run.go`, `internal/cli/listchecks.go`, `internal/cli/version.go`, tests.
- [x] **B8 Checklist printer.** Header (namespace, identity with `(self)`/`(unverified)`, groups, context), one line per result in ID order, `fix` line under every non-PASS, summary with up to three failed and three skipped IDs then "and N more", footer line; color rules (TTY, `NO_COLOR`, `--no-color`; Windows VT or none); `--plain` ASCII; server text stripped of control characters and ANSI, newlines folded, capped to width with `…` and the "full reasons" footer; admin hint line in self mode.
  - AC: golden files for the DESIGN.md sample, `--plain`, `NO_COLOR`, a 600-character multi-line ANSI reason, and the admin hint; width cap at 60 minimum / 120 non-TTY.
  - Verify: `go test ./internal/report/ -run Checklist`.
  - Depends on: B2. Files: `internal/report/checklist.go`, `internal/report/text.go`, `internal/report/color.go`, `internal/report/color_windows.go`, `internal/report/testdata/`.
- [x] **B9 e2e scaffold on kind.** `test/e2e/kind.yaml`, `versions.env` (kindest/node v1.37.0 digest, Kyverno chart 3.9.1 / app v1.19.1, `kyverno/policies` commit for `restrict-nodeport` with `Enforce`, PolicyReport CRD commit), fixtures `tenant-a` and `tenant-b` exactly as DESIGN.md section 8, Kyverno install with the readiness wait (Available plus a rejected dry-run NodePort Service), `run.sh` that runs the four identities and asserts AT-01 only for now plus exit codes, `.github/workflows/e2e.yml`.
  - AC: e2e job green on GitHub Actions; run 4 shows AT-01 FAIL (the admin header line is asserted from B16, when AT-03 exists); runs 1 to 3 show AT-01 PASS.
  - Verify: `make e2e` locally (Docker via colima) and the e2e workflow.
  - Depends on: B7, B8. Files: `test/e2e/kind.yaml`, `test/e2e/versions.env`, `test/e2e/fixtures/*.yaml`, `test/e2e/run.sh`, `.github/workflows/e2e.yml`.
- [x] **B10 Release pipeline dry run.** `.goreleaser.yaml` (darwin/linux amd64+arm64, windows/amd64; tar.gz and zip with LICENSE and README; `checksums.txt`; version/commit/date ldflags), the krew manifest rendered by goreleaser's `krews` block (no `.krew.yaml` template), `release.yml` on `v*` tags: build, upload `audit-tenant.yaml` as an asset, install smoke tests in clean `KREW_ROOT` dirs on ubuntu and macos (`--manifest --archive`, then `--manifest-url`), run `version` from each; a `goreleaser release --snapshot --clean` step in `ci.yml`.
  - AC: snapshot build yields five archives and a checksums file in CI; `kubectl krew install --manifest=... --archive=...` works on the maintainer's Mac (krew installed first).
  - Verify: CI snapshot step; local krew install.
  - Depends on: B7. Files: `.goreleaser.yaml`, `.github/workflows/release.yml`, `.github/workflows/ci.yml`.

**Checkpoint 1 (walking skeleton):** `make check` green; e2e asserts AT-01 across the four runs; snapshot release builds; the first `BENCHMARK.md` timing of the one-check run recorded. Review with Satvik before Phase 2.

### Phase 2: behavioral checks

- [x] **B11 AT-02 and AT-03.** AT-02: `get`, `list` and `watch` secrets in `--other-namespace` (watch added in D66); AT-03: escalate/bind on clusterroles (no namespace; the in-namespace roles questions were dropped in D62), impersonate users/groups/serviceaccounts (no namespace), cluster-wide wildcard; PASS notes state the untested cases (D86) (in-namespace wildcard, in-namespace serviceaccount impersonation, `resourceNames` grants); admin-hint signal for the header when the wildcard is allowed in self mode.
  - AC: PASS and FAIL unit cases per check; reason texts golden.
  - Verify: `go test ./internal/checks/ -run 'AT02|AT03'`.
  - Depends on: B6. Files: `internal/checks/at02.go`, `internal/checks/at03.go`, tests.
- [x] **B12 Probe builders and sizing.** Control pod (restricted-compliant), mutators (AT-04.1 in two forms: `privileged: true` with `allowPrivilegeEscalation` unset and `true`; AT-04.5 `allowPrivilegeEscalation: true` only; D70, D78), sizes from the LimitRange via the cache (always explicit: 1m/1Mi raised to the highest LimitRange min; 1m/1Mi on a 403; any other list error is ERROR; D85), ClusterIP control Service and NodePort probe Service, labels and generateName.
  - AC: builders produce the exact fields per DESIGN.md; sizing table test for the four LimitRange cases; objects pass `kubectl apply --dry-run=server` on kind in B16.
  - Verify: `go test ./internal/probe/ -run 'Build|Size'`.
  - Depends on: B4. Files: `internal/probe/pod.go`, `internal/probe/service.go`, `internal/probe/size.go`, tests.
- [x] **B13 Rejection classifier.** Ordered rule table: 429 or 5xx → ERROR (D95), ValidatingAdmissionPolicy evaluation error (D99, D102), webhook denied, PSA, ValidatingAdmissionPolicy denied, no dry-run support, NodePort quota, quota or LimitRange text (every text rule needs a 4xx), HTTP 400/422, other (D76, D82, D84) (full text, or "HTTP <code> <reason>" when empty); accepted → FAIL; layer vocabulary incl. `admission (policy: name)`.
  - AC: table test with one real server string per rule and the empty-message case; rule order test (webhook before 400); coverage of `internal/probe` at least 90%.
  - Verify: `go test ./internal/probe/ -run Classify`; `hack/coverage-gate.sh`.
  - Depends on: B12. Files: `internal/probe/classify.go`, `internal/probe/classify_test.go`, `internal/probe/testdata/messages.txt`.
- [x] **B14 AT-04.** Order per DESIGN.md: SSAR create pods; denied → create/update/patch SSARs on the seven controller kinds, ephemeral containers and four `/scale` subresources (D66, D77), then the no-rights anchor (D80) → PASS (RBAC) or SKIP; allowed → control pod → six probes (AT-04.1 in two forms, D78) → classifier, and a 429, 5xx or failed send ends the probes with the rest ERROR (D91); control rejected → five SKIPs, an API failure on the control → five ERRORs (D65); the control runs only when an AT-04 sub-check is selected.
  - AC: unit cases: allowed with PSA rejections; all denied; denied with `update deployments` allowed; control rejected by quota; `--checks AT-04.2` alone.
  - Verify: `go test ./internal/checks/ -run AT04`.
  - Depends on: B13. Files: `internal/checks/at04.go`, tests.
- [x] **B15 AT-05.** SSAR create services; denied → update/patch SSARs → PASS or SKIP; allowed → ClusterIP control → NodePort probe → classifier.
  - AC: unit cases: webhook rejects NodePort (PASS, layer webhook); control rejected (SKIP); accepted (FAIL); update allowed (SKIP).
  - Verify: `go test ./internal/checks/ -run AT05`.
  - Depends on: B13. Files: `internal/checks/at05.go`, tests.
- [x] **B16 e2e: AT-01 to AT-05.** Extend `run.sh` assertions to AT-01..05 for runs 1 to 4 (AT-04 FAIL on tenant-b, AT-05 PASS via the Kyverno webhook, AT-04 PASS via PSA on tenant-a); `hints()` in run.sh already asserts a fix line under every FAIL, SKIP and ERROR.
  - AC: e2e job green with the extended expectations; run 4 shows the admin header line.
  - Verify: e2e workflow.
  - Depends on: B14, B15. Files: `test/e2e/run.sh`, `test/e2e/expected/`.

**Checkpoint 2:** behavioral checks green on kind for all four identities; classifier table test covers every rule; coverage gate green.

### Phase 3: configuration checks and headroom

- [x] **B17 AT-06 and AT-07.** AT-06: unscoped quota with the five keys (aliases `cpu`, `memory`, `count/pods`); AT-07: Container LimitRange with default and defaultRequest for cpu and memory; both from the cache; 403 → SKIP with the permission.
  - AC: table tests for scoped-only quota, missing keys, aliases, several LimitRanges, 403.
  - Verify: `go test ./internal/checks/ -run 'AT06|AT07'`.
  - Depends on: B6. Files: `internal/checks/at06.go`, `internal/checks/at07.go`, tests.
- [x] **B18 AT-08 and AT-09.** AT-08: default-deny ingress and egress policies (empty podSelector, policyTypes, no rules; same or different objects), two-policy hint, network-plugin note; AT-09: PSA enforce label baseline or restricted from the cached namespace; cluster-default and policy-engine notes (ADR 0007); 403 → SKIP.
  - AC: table tests: two policies, one combined policy, inline-DNS egress (FAIL with the two-policy hint), privileged label, missing label with AT-04 admission note.
  - Verify: `go test ./internal/checks/ -run 'AT08|AT09'`.
  - Depends on: B6. Files: `internal/checks/at08.go`, `internal/checks/at09.go`, tests.
- [ ] **B19 Headroom math.** `internal/headroom`: pod request and limit (pod-level `spec.resources` first; limit-only counts as request; sidecar rule; LimitRange defaults, else 0 with a note), replicas (HPA matched by group, kind and name → maxReplicas), peak (RollingUpdate surge absolute or percent rounded up, Recreate, StatefulSet), quota comparison per key with aliases, shortfall text.
  - AC: table tests incl. the fixture (5 pods, 0.5 cpu / 640Mi requests, 2.5 cpu / 2.5Gi limits); percent rounding; sidecar overlap; limit-only; pod-level resources; coverage at least 80%.
  - Verify: `go test ./internal/headroom/...`.
  - Depends on: B1. Files: `internal/headroom/pod.go`, `internal/headroom/peak.go`, `internal/headroom/compare.go`, tests.
- [ ] **B20 AT-10.** Lists deployments, statefulsets, HPAs (quota and limitranges from the cache); edge cases in DESIGN.md order (no unscoped quota → FAIL; no workloads → PASS; unreadable deployments/statefulsets/quota/HPAs → SKIP; unreadable LimitRange with unsized containers → SKIP; partial sum over hard → FAIL); every reason starts with "estimate: …"; `used` values and the not-counted kinds named.
  - AC: unit cases for each edge; reason golden for the fixture.
  - Verify: `go test ./internal/checks/ -run AT10`.
  - Depends on: B19. Files: `internal/checks/at10.go`, tests.
- [ ] **B21 e2e: all ten checks.** Assert the complete verdict table of DESIGN.md section 8 for runs 1 to 4, exit codes, header groups in run 2, hints on every non-PASS.
  - AC: e2e job green against the full table.
  - Verify: e2e workflow.
  - Depends on: B17, B18, B20. Files: `test/e2e/run.sh`, `test/e2e/expected/`.

**Checkpoint 3:** full verdict matrix green on kind; `make check` under 90 s; coverage gate green for every package.

### Phase 4: output formats and flags

- [ ] **B22 `-o wide`.** MTB ID and deciding-layer columns (`RBAC`, `admission (PSA)`, `admission (webhook: name)`, `admission (policy: name)`, `admission (quota: name)` (D68), `config`, `-`); full reasons, no cap; sanitized.
  - AC: golden files; layer vocabulary test.
  - Verify: `go test ./internal/report/ -run Wide`.
  - Depends on: B8. Files: `internal/report/wide.go`, goldens.
- [ ] **B23 `-o json` and schema.** JSON report per DESIGN.md section 6; `docs/report.schema.json`; unit test validates the printer output against the schema; e2e run 1 validates too.
  - AC: schema validation passes in unit and e2e; `summary` counts are per result.
  - Verify: `go test ./internal/report/ -run JSON`; e2e workflow.
  - Depends on: B8. Files: `internal/report/json.go`, `docs/report.schema.json`, tests, `test/e2e/run.sh`.
- [ ] **B24 `-o policyreport`.** `wgpolicyk8s.io/v1alpha2` with the per-identity name (slug plus hash when truncated), identity annotations, result mapping, severity, properties incl. identity; e2e installs the CRD and runs `kubectl apply --dry-run=server`.
  - AC: slug tests (service account, long identity); apply succeeds in e2e run 1.
  - Verify: `go test ./internal/report/ -run PolicyReport`; e2e workflow.
  - Depends on: B8. Files: `internal/report/policyreport.go`, tests, `test/e2e/run.sh`, `test/e2e/versions.env`.
- [ ] **B25 `--debug` and `--fail-on-skip`.** `--debug` writes the recorder trace, identity steps and SSAR/probe bodies to stderr, never headers (the `userInfo.extra` redaction landed with D51 in `internal/safety`; keep it under the stderr golden); `--fail-on-skip` summary wording and exit code; `-o json` reports the resulting exitCode.
  - AC: stderr golden for a fake run; a test asserts no `Authorization` or `Impersonate-` header text in `--debug` output (acceptance criterion 2); exit-code case with a SKIP.
  - Verify: `go test ./internal/cli/ -run 'Debug|FailOnSkip'`.
  - Depends on: B7, B3. Files: `internal/cli/debug.go`, `internal/report/exit.go`, tests.

**Checkpoint 4:** all five printers golden-tested; schema and PolicyReport pass in e2e; `--debug` header test green.

### Phase 5: integration layer, benchmark and floor tests

- [ ] **B26 envtest layer.** `setup-envtest use 1.37.x` in CI; Role and RoleBinding fixtures for PASS and FAIL per AT-01..03; SSAR under impersonation; `--as-group` without `--as` exits 2 against the real server.
  - AC: envtest job green; FAIL fixtures produce FAIL, PASS fixtures PASS.
  - Verify: `make envtest` in CI.
  - Depends on: B11. Files: `test/envtest/rbac_test.go`, `test/envtest/fixtures/`, `.github/workflows/ci.yml`.
- [ ] **B27 Benchmark.** `test/e2e/bench.sh`: five full runs, median and maximum wall time, request count from the `--debug` trace; asserts median under 10 s and at most 40 requests; writes `BENCHMARK.md` rows with the exact command; kubectl-mtb baseline attempt recorded as a finding if it does not build.
  - AC: `BENCHMARK.md` has measured rows for v0.1.0-rc; CI fails if a budget is missed.
  - Verify: `make bench` inside the e2e job.
  - Depends on: B21, B25. Files: `test/e2e/bench.sh`, `BENCHMARK.md`, `.github/workflows/e2e.yml`.
- [ ] **B28 Floor tests over the registry.** A unit test that drives every registered check through its FAIL, SKIP and ERROR paths with scripted answers and asserts a non-empty hint on each; `list-checks` golden; the safety replay now covers all ten checks.
  - AC: hint test green for all 14 result IDs; `TestSafety` replays every check.
  - Verify: `go test ./internal/checks/ -run 'Hints|ListChecks'`; `make safety`.
  - Depends on: B21. Files: `internal/checks/hints_test.go`, `internal/safety/transport_test.go`.

**Checkpoint 5:** `make check-full` green in CI (vet, lint, unit, safety, floor guard, govulncheck, envtest, e2e, bench); CONSTRAINTS.md "Measured" table filled with the first real numbers.

### Phase 6: docs and release

- [ ] **B29 README.** Problem in two sentences; quick start on kind with the three install paths; the check table with the AT-04.5 note; "how this differs" (popeye, kubescape, kyverno audit mode, kubectl-mtb, NamespaceHound, kuota-calc, the can-i script); safety section with the audit-log note and "what a PASS does not prove"; troubleshooting (one entry per SKIP/ERROR hint, `--debug` line; the "timed out during startup" entry says a hung credential helper may keep running after the tool exits); pipeline example with `--fail-on-skip`; prior art and citation.
  - AC: quick start works on a clean machine with Docker, kind and kubectl in five minutes (acceptance criterion 9); `hack/check-public.sh` passes.
  - Verify: fresh-machine run (Satvik or a clean VM); `make check-fast`.
  - Depends on: B21, B25. Files: `README.md`.
- [ ] **B30 Citation, changelog, diagrams.** `CITATION.cff` with the paper DOI; `CHANGELOG.md` v0.1.0 entry; re-render `docs/diagrams/architecture.{svg,png,excalidraw}` from the updated `.mmd` (admission label); GLOSSARY.md: deciding layer gains "policy".
  - AC: CFF validates; SVG shows "PSA, webhook or policy".
  - Verify: `cffconvert --validate` or GitHub's CFF check; visual check of the SVG.
  - Depends on: none. Files: `CITATION.cff`, `CHANGELOG.md`, `docs/diagrams/*`, `GLOSSARY.md`.
- [ ] **B31 Pre-release gate (Satvik and agent).** `audit-tenant.dev` is registered (done 2026-10-11, D20); `../../scripts/clean-room-check.sh .` passes; CONSTRAINTS.md "Measured" values recorded; `make check-full` green on main; Satvik pushes and tags `v0.1.0`; release job publishes five archives, checksums and the krew manifest; Satvik opens the krew-index PR.
  - AC: acceptance criteria 1 to 11 in DESIGN.md section 12 all met; the release page shows the assets; `kubectl krew install --manifest-url` works from the release.
  - Verify: release workflow; manual install on macOS and Linux.
  - Depends on: B27, B28, B29, B30. Files: `CONSTRAINTS.md`, `TODOS.md`, release assets.

**Checkpoint 6 (v0.1.0):** every acceptance criterion met; `evidence/` row in the lab for the first outside install when it happens.

### Before merging Phase 2 (/ship final round, 2026-10-14)

Found by the /ship verification round on 7e886d0, after the review's fix-cycle limit. The PR stays a draft until the first two are fixed.
- [x] The D101 latch fires only on a visible `resourceVersion`. A create answer with no generated name, an unreadable or oversized 2xx answer, a failed send, or a 429/5xx also leaves the run unsure whether an object was stored, and AT-05 still sends its creates (reproduced: AT-05 FAIL after a nameless control answer). Set the latch on every create answer that is not a verified dry run, reword its reason to "could not be verified as a dry run", and test each case (Claude adversarial, security specialist, red team). Done (D103 = A): the safety transport checks every pods and services answer and latches on any that is not a verified dry run; `TestSafetySendsNoCreateAfterAnAnswerItCannotVerify` and `TestAnUnverifiedCreateStopsEveryLaterCreate` cover each case.
- [x] client-go retries a create up to 10 times on a 429 or 5xx with Retry-After, below every check, so one probe can be 11 real writes if a hop drops dryRun=All. Strip Retry-After from pods and services POST answers in the safety transport (or send creates with MaxRetries(0)), with a safety test (security specialist, red team). Done (D103): the transport removes Retry-After from create answers; `TestSafetySendsEachCreateOnce`.
- [x] After a stored create, an interrupted result still says "re-run; the cluster was not changed" (runner `notRun`, DESIGN.md section 6); drop that clause once the latch is set (Claude adversarial). Done: the hint lists leftovers by the managed-by label instead.
- [x] Tests: the code-below-400 test over every text rule, asserting ERROR; a D102 row whose expression quotes the webhook phrase; the AT-04 `unverifiedEarlier` guard (or drop it until a creating check runs before AT-04). Done: every `messages.txt` row below 400 is ERROR; a D102 row quotes the webhook phrase; `TestAT04SendsNoCreateAfterAnUnverifiedOne`.
- [x] The AT-05 FAIL hint names only `create services`; also name `update` and `patch`, which can turn an existing Service into NodePort (Codex structured review, P2). Done: the hint names a policy on create and update, or removing `create`, `update` and `patch`.
- [x] Small: one re-run hint text lives in two packages (`hintRetry`, `hintRequestFailed`); `messages.txt` claims one string per classifier rule but has no 429 or policy-error row; the D91 test gives its 429 the reason ServiceUnavailable; one AT-05 test row pins client-go's error wording. Done: `probe.HintRetry` is the one text; `messages.txt` gains a `policy-error` row captured on kind and a `throttled` row (client-go's text for a plain 429); the D91 test sends TooManyRequests; the AT-05 row checks only the question and the server text.

### Review follow-ups from the Phase 1 ship (2026-10-12, D59)

Found by the /ship adversarial reviews and left for the phase that owns them. Each one names its card.
- [ ] Verify every Registry MTB ID against kubectl-mtb before `-o wide` and `-o json` show them (B22): AT-04.2 and AT-05 both carry `MTB-PL1-BC-HI-1`.
- [x] An SSAR answer without `status.allowed` reads as a denial; require the field when AT-02 and AT-03 reuse `can()` (B11). Done: an answer with no `allowed` field is ERROR with the not-the-API-server hint; the request carries no status, so a proxy that echoes it back is caught too (D64, Phase 2 review).
- [x] A namespace GET that fails with a 5xx lets the run continue; only a 403 should (B18, with AT-09). Done: any other error exits 2 "could not read namespace <n>: <error>" (DESIGN.md section 6).
- [x] The runner abandons a check 100 ms after the deadline, so a grant held by a check stuck in a credential helper is lost; consider reporting grants as they are proven (B11). Done (D61): checks hand each proven FAIL to the runner, which keeps it when a check never returns or crashes.
- [x] An interrupted run turns a proven FAIL (exit 1) into exit 2 (DESIGN.md section 8 already lists "interruption after a FAIL" for B16). Done (D63): a FAIL proven before the signal exits 1, as after --timeout.
- [ ] `--as <user>` without `--as-group` tests the user without its identity-provider groups; warn in the header (B25).
- [ ] `--debug` will print whole dry-run Pod answers once AT-04 lands; decide what to redact (webhook-injected env) with the stderr golden (B25).
- [ ] Release: goreleaser `release.draft: true`, publish only after the krew-install job passes; check the krew download checksum; release only tags on main (B31). GitHub Actions SHA pinning (D43) before the public release.
- [ ] `hack/check-public.sh` passes when `git ls-files` fails (not a checkout); make it fail closed (B29).
- [ ] CONSTRAINTS.md lists an envtest gate (AT-01 to AT-03 under real RBAC); `test/envtest/` does not exist yet (B26).
- [ ] A default run drops unbuilt checks without a line saying so; D28 trims the Registry at v0.1.0, or print "not in this build" until then (B31).
- [ ] Teleport-style access proxies (D57) are covered by unit tests only; try one live before the release (B31).

### Review follow-ups from the Phase 2 review (2026-10-13, D74)

Found by the end-of-phase review (specialists, red team, Claude and Codex adversarial passes) and left for the card that owns them.
- [ ] A mutating webhook can remove the dangerous field, so an accepted dry run is not proof of FAIL; check the returned object before calling it FAIL, and report PASS "admission (mutation)" when the field is gone (Later; the error is a false FAIL, the safe direction).
- [ ] AT-05 probes only `type: NodePort`; `type: LoadBalancer` also allocates node ports by default. The PASS note says so now; decide whether to add a LoadBalancer probe (Later).
- [ ] AT-03 does not see grants from RoleBindings in other namespaces (`impersonate` on kube-system service accounts, `create serviceaccounts/token` or `create pods` there), changes to webhook configurations or ValidatingAdmissionPolicy bindings, or `get nodes/proxy`. The PASS note names the first; decide the question set before v0.1.0 (B31 spec pass).
- [ ] On OpenShift (restricted-v2 SCC) or with registry allowlists, the control pod's fixed `runAsUser: 65532` and the public pause image are rejected, so AT-04 is SKIP; an SCC 403 on a probe matches no classifier rule (ERROR). Consider dropping `runAsUser`, adding `readOnlyRootFilesystem: true`, an SCC classifier rule and a `--probe-image` flag (Later).
- [ ] Pods a controller creates can pass under a PSA exemption that the tenant's own probes do not have; the README's "what a PASS does not prove" must say so (B29).
- [ ] `--debug` now prints whole dry-run Pod answers, which can carry values a mutating webhook injected; the README tells users to paste `--debug` into issues (B25, with the existing redaction item).
- [ ] Redesign AT-04's and AT-05's RBAC-only branch around one SelfSubjectRulesReview (one request; SSAR fallback when `incomplete`): it would cover every way to get a pod or Service, including the in-namespace paths the PASS note names today and the paths that make a controller create pods from an existing template (raising an HPA's minReplicas, deleting or evicting pods; Codex /ship final round) (creating Secrets of type service-account-token, RoleBindings, service account tokens, impersonating local service accounts). A read-only tenant's run already costs about 53 requests (31 controller SSARs), over the 40 budget (B27, D77).
- [ ] AT-04 probes test regular containers only; add an initContainers form, allowed hostPath prefixes, and a hostPort probe for AT-04.4's MTB ID, or narrow the MTB tags (B22 with the MTB ID check, D78).
- [ ] Running as root (`runAsUser: 0`) left AT-04.5 in D78; decide whether it becomes its own result (B31 spec pass).
- [ ] When the tenant may create pods, AT-04 does not ask about `pods/ephemeralcontainers`; a webhook or ValidatingAdmissionPolicy whose rules list `pods` does not match that subresource, so a policy PASS can leave privileged ephemeral containers open (PSA covers them). The AT-04.1 and AT-04.5 notes say so now; ask update/patch on the subresource on the probe branch and SKIP or note a non-PSA PASS when allowed (B22, D97; /ship review pass 2).
- [x] A tenant that may `update` or `patch` its own Namespace can remove the PSA label that made AT-04 PASS; AT-09 should ask it (B18, D81). Done (D105 = note): an AT-09 PASS asks update, then patch, on the namespace object and notes either grant; the verdict stays.
- [ ] A tenant bound to `admin` may delete namespaced Kyverno Policies (the chart aggregates that right) or create PolicyExceptions, so a webhook PASS can be liftable (B31 question-set pass, D81).
- [ ] AT-03 does not ask about approving CertificateSigningRequests (`update certificatesigningrequests/approval`, `approve` on signers); add it to the B31 question-set pass.
- [ ] Probes have one valid form where policies can demand another: the NodePort Service has no selector, the hostNetwork pod the default DNS policy. A policy that requires a selector or `ClusterFirstWithHostNet` rejects the probe for an unrelated reason and fakes a PASS; add those forms (Later, D87).
- [ ] A misspelled `--other-namespace` gives a silent AT-02 PASS; a tenant cannot tell a missing namespace from a forbidden one. Warn when the name differs from common system namespaces, or document it (B25, D87).
- [ ] The no-rights anchor (D80) passes for a misspelled `--as` user when a cluster grants `list pods` to `system:authenticated` (B25 identity warnings, D87).
- [ ] Exemptions keyed on fields the tenant controls (PSA `exemptions.runtimeClasses`, Gatekeeper `exemptImages`, Kyverno exclusions by label or name) make a probe's rejection unrepresentative (B31 question-set pass, D87).
- [ ] `-o json` and the B23 schema must carry each result's `note` (what it did not test, D86).
- [ ] Unverified (red team, confidence 3/10): a policy engine whose own rule errors under failurePolicy Fail may also answer "admission webhook ... denied the request", which rule 1 reads as a PASS, as D99 handles for ValidatingAdmissionPolicy. Confirm Kyverno's and Gatekeeper's error-rejection texts and add them as ERROR before rule 1, or close this item (B31 spec pass; /ship review pass 3).
- [ ] AT-03 asks no cluster-scope `escalate`/`bind` on namespaced `roles` and no cluster-scope `create serviceaccounts/token`; a ClusterRoleBinding granting either is cluster-wide escalation, and AT-03 still PASSes (D62 dropped only the in-namespace questions). Add the three questions with no namespace, update the DESIGN.md row and the PASS note (B31 question-set pass; /ship final review, red team).
- [ ] A stored dangerous probe (dryRun=All dropped) is ERROR (exit 2), not FAIL, although admission accepted the object; decide whether it becomes FAIL with the stored-object warning (B31; /ship final review, Claude adversarial).
- [ ] A mutating policy that sets `allowPrivilegeEscalation: false` when it is absent turns AT-04.1's first form into a 422 validation error, which reads as "a bug in audit-tenant" (ERROR, exit 2), and `worse` keeps it over the second form's verdict; an API server with `--allow-privileged=false` gives the same 422 for both forms. Read these 422 texts as their own outcomes (B22; /ship final review).
- [ ] Probes are easy to recognize (fixed generateName, label, annotation, dryRun=true), so a tenant that may create its own admission policies could deny only the probes and fake a PASS; extends the liftable-fence items (B31; /ship final review, Claude adversarial).
- [ ] ResourceQuota is the last validating admission plugin, after PodSecurity, ValidatingAdmissionPolicy and the webhooks, so an "exceeded quota" answer to a dangerous probe means every security gate admitted it. Today it is SKIP (a full non-zero `services.nodeports` quota too, D76) and a control rejected by quota makes all of AT-04 SKIP. Decide whether a probe's quota rejection with a non-zero limit is FAIL and whether the probes still run after a quota-rejected control; the hint no longer claims quota runs before the gates (B31 spec pass, D96; /ship review pass 2).

## Later (not v0.1; locked in the spec session, 2026-10-10)
- [ ] Network behavior tests (need running pods)
- [ ] Write the PolicyReport into the cluster (`--apply`)
- [ ] Capsule `Tenant` discovery (audit every namespace of a tenant)
- [ ] SARIF output
- [ ] Gatekeeper constraint mapping
- [ ] Helm chart or operator for scheduled runs
- [ ] Many namespaces or clusters in one run
- [ ] `--baseline` file: pick checks and severities (v0.2)
- [ ] DaemonSets, Jobs, CronJobs and bare Pods in AT-10
- [ ] Argo Rollouts in AT-10 (v0.2): read as unstructured objects when the CRD exists; blueGreen = replicas + (previewReplicaCount else replicas); canary = replicas + maxSurge, or 2 × replicas when `trafficRouting` is set and `dynamicStableScale` is not (deferred in the CEO review, 2026-10-10)
- [ ] `--as-serviceaccount ns/name` shortcut; cloud identity lookups
- [ ] Detect whether the network plugin enforces NetworkPolicy
- [ ] Weekly CI run against the oldest supported server (1.28)
- [ ] krew-release-bot for releases after v0.1.0
- [ ] Migrate the e2e Kyverno policy from the legacy `restrict-nodeport` ClusterPolicy (pinned in `test/e2e/versions.env`; kyverno/policies keeps only CEL policies since 2026-09-29) to a CEL `ValidatingPolicy` at the next Kyverno bump (Checkpoint 1, D39)
- [ ] Signed releases and SBOM (v0.2): goreleaser `signs` with cosign keyless (`id-token: write`), `sboms` with syft, and a README "verify the download" section (CEO review D21)
