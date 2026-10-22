# audit-tenant: done TODOs

Done items, moved out of `TODOS.md` on 2026-10-22. Open items stay in `TODOS.md` under the same headings. Phases and checkpoints: `tasks/plan.md`.

## v0.1

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
- [x] **B19 Headroom math.** `internal/headroom`: pod request and limit (pod-level `spec.resources` first; limit-only counts as request; sidecar rule; LimitRange defaults, else 0 with a note), replicas (HPA matched by group, kind and name → maxReplicas), peak (RollingUpdate surge absolute or percent rounded up, Recreate, StatefulSet), quota comparison per key with aliases, shortfall text.
  - AC: table tests incl. the fixture (5 pods, 0.5 cpu / 640Mi requests, 2.5 cpu / 2.5Gi limits); percent rounding; sidecar overlap; limit-only; pod-level resources; coverage at least 80%.
  - Verify: `go test ./internal/headroom/...`.
  - Depends on: B1. Files: `internal/headroom/pod.go`, `internal/headroom/peak.go`, `internal/headroom/compare.go`, tests.
- [x] **B20 AT-10.** Lists deployments, statefulsets, HPAs (quota and limitranges from the cache); edge cases in DESIGN.md order (no unscoped quota → FAIL; no workloads → PASS; unreadable deployments/statefulsets/quota/HPAs → SKIP; unreadable LimitRange with unsized containers → SKIP; partial sum over hard → FAIL); every reason starts with "estimate: …"; `used` values and the not-counted kinds named.
  - AC: unit cases for each edge; reason golden for the fixture.
  - Verify: `go test ./internal/checks/ -run AT10`.
  - Depends on: B19. Files: `internal/checks/at10.go`, tests.
- [x] **B21 e2e: all ten checks.** Assert the complete verdict table of DESIGN.md section 8 for runs 1 to 4, exit codes, header groups in run 2, hints on every non-PASS.
  - AC: e2e job green against the full table.
  - Verify: e2e workflow.
  - Depends on: B17, B18, B20. Files: `test/e2e/run.sh`, `test/e2e/expected/`.

**Checkpoint 3:** full verdict matrix green on kind; `make check` under 90 s; coverage gate green for every package.

### Phase 4: output formats and flags

- [x] **B22 `-o wide`.** MTB ID and deciding-layer columns (`RBAC`, `admission (PSA)`, `admission (webhook: name)`, `admission (policy: name)`, `admission (quota: name)` (D68), `config`, `-`); full reasons, no cap; sanitized.
  - AC: golden files; layer vocabulary test.
  - Verify: `go test ./internal/report/ -run Wide`.
  - Depends on: B8. Files: `internal/report/checklist.go` (`Checklist.Wide`), goldens.
- [x] **B23 `-o json` and schema.** JSON report per DESIGN.md section 6; `docs/report.schema.json`; unit test validates the printer output against the schema; a fifth e2e run (run 1's identity, `-o json`) validates too.
  - AC: schema validation passes in unit and e2e; `summary` counts are per result.
  - Verify: `go test ./internal/report/ -run JSON`; e2e workflow.
  - Depends on: B8. Files: `internal/report/json.go`, `docs/report.schema.json`, tests, `test/e2e/run.sh`.
- [x] **B24 `-o policyreport`.** `wgpolicyk8s.io/v1alpha2` with the per-identity name (slug plus a 32-hex hash of the requested identity, D144, D147), identity annotations, result mapping, severity, properties incl. identity; e2e installs the CRD and runs `kubectl apply --dry-run=server`.
  - AC: slug tests (service account, long identity); apply succeeds in the sixth e2e run (run 1's identity, `-o policyreport`).
  - Verify: `go test ./internal/report/ -run PolicyReport`; e2e workflow.
  - Depends on: B8. Files: `internal/report/policyreport.go`, tests, `test/e2e/run.sh`, `test/e2e/versions.env`.
- [x] **B25 `--debug` and `--fail-on-skip`.** `--debug` writes the recorder trace, identity steps and SSAR/probe bodies to stderr, never headers (the `userInfo.extra` redaction landed with D51 in `internal/safety`; keep it under the stderr golden); `--fail-on-skip` summary wording and exit code; `-o json` reports the resulting exitCode.
  - AC: stderr golden for a fake run; a test asserts no `Authorization` or `Impersonate-` header text in `--debug` output (acceptance criterion 2); exit-code case with a SKIP.
  - Verify: `go test ./internal/cli/ -run 'Debug|FailOnSkip'`.
  - Depends on: B7, B3. Files: `internal/cli/run.go`, `internal/safety/transport.go` (`createSummary`), `internal/cli/debug_test.go`, tests.

**Checkpoint 4:** all five printers golden-tested; schema and PolicyReport pass in e2e; `--debug` header test green.

### Phase 5: integration layer, benchmark and floor tests

- [x] **B26 envtest layer.** `setup-envtest use 1.37.x` in CI; Role and RoleBinding fixtures for PASS and FAIL per AT-01..03; SSAR under impersonation; `--as-group` without `--as` exits 2 against the real server.
  - AC: envtest job green; FAIL fixtures produce FAIL, PASS fixtures PASS.
  - Verify: `make envtest` in CI.
  - Depends on: B11. Files: `test/envtest/rbac_test.go`, `test/envtest/fixtures/`, `.github/workflows/ci.yml`.
- [x] **B27 Benchmark.** `test/e2e/bench.sh`: five full runs, median and maximum wall time, request count from the `--debug` trace; asserts median under 10 s and at most 40 requests; writes `BENCHMARK.md` rows with the exact command; kubectl-mtb baseline attempt recorded as a finding if it does not build.
  - AC: `BENCHMARK.md` has measured rows for v0.1.0-rc; CI fails if a budget is missed.
  - Verify: `make bench` inside the e2e job.
  - Depends on: B21, B25. Files: `test/e2e/bench.sh`, `BENCHMARK.md`, `.github/workflows/e2e.yml`.
- [x] **B28 Floor tests over the registry.** A unit test that drives every registered check through its FAIL, SKIP and ERROR paths with scripted answers and asserts a non-empty hint on each; `list-checks` golden; the safety replay now covers all ten checks.
  - AC: hint test green for all 14 result IDs; `TestSafety` replays every check.
  - Verify: `go test ./internal/checks/ -run 'Hints|ListChecks'`; `make safety`.
  - Depends on: B21. Files: `internal/checks/hints_test.go`, `internal/safety/transport_test.go`.

**Checkpoint 5:** the CI `check` job (vet, lint, unit, safety, govulncheck, envtest) and `e2e` job (e2e, bench) green; the `floor-guard` job red only for the D164 findings Satvik has read; CONSTRAINTS.md "Measured" table filled with the first real numbers.

### Phase 5 /review (2026-10-21)

Found by the Phase 5 /review: three cycles, each with six specialists, Red Team, Claude adversarial and Codex adversarial and structured. Decided and done in this branch:
- D160: keep D158 (a Measured row's Today cell is a record), with its bypasses closed.
- D161: a worse Today record is loud; D165: a "stays under N" record is worse only at N.
- D162: the envtest binaries and their release index are pinned.
- D163: one source for the e2e budgets (`test/e2e/budgets.sh` reads CONSTRAINTS.md); the `envtest` lint tag stays global on purpose, so lint checks `test/envtest` on every edit (about 2 s more on a cold `make check-fast`).
- D164: cycles 1 and 2 found about 46 ways past line-by-line rules, so the floor guard fails closed on any change to CONSTRAINTS.md and the gate files; it runs in its own CI job, which also runs the base commit's copy. Cycle 3 closed the rest it found: comment lines, carriage returns, quoted paths, unit changes, more gate files and an isolated Python run.

### Phase 4 /review (2026-10-21)

Found by /review (core pass, seven specialists, Red Team, Claude adversarial, Codex adversarial and structured) on phase-4-output; decisions D143 to D146.
- [x] D143: the `--allow-privileged=false` PASS applied to every AT-04 probe, so a privileged sidecar a webhook added to the hostPath probe made AT-04.2 PASS (Codex adversarial, red first). Only AT-04.1 PASSes on that layer now; other probes are SKIP. An AT-04.1 PASS after a mutated form says one form never reached a security gate; AT-09 counts the layer as a fence; the form choice reads the error, not the hint.
- [x] D144: PolicyReport names collided for case, replaced characters and group sets (`--as audit-probe --as-group <group>`); every name now ends with a hash of the username and sorted groups. Annotations add `other-namespace` and `warnings`; `groups` is a JSON array.
- [x] D145: `--debug` keeps the probe's security fields of an accepted create, so a mutation that removed a dangerous field can be diagnosed (Claude adversarial, Red Team).
- [x] D146: warnings for a kubeconfig `as:` user with no groups, a service account with `--as-group` (no `system:serviceaccounts` groups, checked on kind), and a `--checks`/`--skip` run that leaves results out.
- [x] Small: `-o json` printed a kubeconfig server URL with its user:password (security; the client now drops the userinfo); two stale comments; two duplicate test rows; rule 4b keeps the whole server text.
- [x] Pass 2 (D147): `-o policyreport` refuses a `--checks`/`--skip` run (exit 2); the name hash is 32 hex characters of the requested identity, so verified and unverified runs share a report and an 8-hex collision (Codex) cannot happen; a form counts as mutated only when classifier rule 4c decides it (a policy error quoting the mutation text could have turned ERROR into PASS, Codex); the two-form fold's 36 cells are tested; `--debug` drops `windowsOptions` (GMSA credential specs); the server URL also drops its query and fragment; the service-account warning fires only when its own groups are missing; the namespace warning only when AT-02 runs; the hints name the change correctly.
- [x] /ship review (D149): the name hash dropped a service account's own two groups even when `--as-group` gave other groups, so an under-grouped run (which the warning calls incomplete) replaced the full identity's report. The pair is dropped now only when it is the whole group set, as the API server adds it. One untested two-form cell got its note checked; two stale test comments.
- [x] /ship review, Red Team (D151): `--as alice` with no groups took the name of alice's own self-mode report, so a run without her identity provider's groups could replace it; an impersonated user (not a service account) is now keyed apart. A self-mode run that no SelfSubjectReview answer named took the kubeconfig's user entry name, which other people and in-cluster runs share; `-o policyreport` now exits 2 there. DESIGN.md section 4 now says 40 requests per run, not about 35.

### Phase 4 /ship review, verification pass (2026-10-21)

Found by the second ship review pass (six specialists and Red Team) on the cycle-1 fixes; left open because the ship review gets one fix cycle (D148, D153). Satvik decides whether a further cycle fixes them before merge.
- [x] D149 misses one case: `--as <service account> --as-group system:authenticated` keys as the plain service account, though a given group stops the API server from adding the service account's own groups, so this weaker run replaces the full report (Red Team CRITICAL, security). Fix in `requestedIdentity`: test the pair first, then key an otherwise empty group set as `system:authenticated` when the requested groups were not empty; add the row to the verified/unverified name test. Done (D154): the name hashes the run inputs as given, so the two runs get two names.
- [x] DESIGN.md section 4 says "at most 40 requests per run", but CONSTRAINTS.md limits only e2e runs, and a read-only tenant's run sends about 63 (section 7, this file's B27 items). Say "at most 40 per e2e run" (Red Team, maintainability). Done (D159): section 4 says "per e2e run".
- [x] DESIGN.md section 6 says identities that differ in groups keep separate reports; that holds only with impersonation. In self mode the username alone names the report, so two client certificates with one CN and different O share it. Narrow the sentence, or key self mode by groups now that D151 makes every self-mode PolicyReport verified (maintainability). Done (D154): self mode keys by the verified username and groups, and the sentence now describes the run inputs.
- [x] Repeated `--as-group` values give one identity two names; `slices.Compact` after the sort (testing). Done (D154).

### Phase 4 /ship final review and adversarial passes (2026-10-21)

Found by the third ship review pass (six specialists and Red Team, no edits) and Step 11 (Claude adversarial, Codex adversarial, Codex structured); open under D148 and D153. Satvik chose D154 (key the name on the exact run inputs), which closes the naming items marked done here and above. Codex structured: P2 only (the service-account item above), gate pass. Most items are one question: which run inputs name a PolicyReport (D154).
- [x] The PolicyReport name ignores `--other-namespace`, so a run against another boundary replaces the identity's AT-02 result (Red Team, security, Codex adversarial). Key it, or refuse a non-default value as D147 does for `--checks`. Done (D154): the other namespace is in the key.
- [x] The name ignores `--as-uid` and `--as-user-extra` (also kubeconfig `as-user-extra`), which the client sends and an authorizer or a ValidatingAdmissionPolicy can read; the header, `-o json` and `--debug` do not show the extras either. Dropping `system:authenticated` merges an identity that carries `system:unauthenticated` with one that does not (the API server adds `system:authenticated` only without it) (Codex adversarial, High and Medium; Claude adversarial rerun). Done for the name (D154): uid, extras and the groups as given are in the key. Still open: the header, `-o json` and `--debug` do not show the uid or the extras.

### Phase 3 follow-ups (build, 2026-10-14)

Found while building B17 to B21; each names the card or decision that owns it.
- [x] The checklist cuts reasons near 120 columns off a terminal, and AT-10's required 104-character "estimate: ..." prefix fills most of that, so the peak numbers show only in `-o wide` and `-o json`. Decide whether the long caveat moves to the note and the reason keeps a short "estimate: " start (Checkpoint 3 question; ADR 0006 asks only that the reason start with "estimate"). Done (D106 = short start): the reason starts "estimate: " and the note carries the caveat with the kinds not counted.
- [x] The e2e asserts AT-10's verdict and note, not the fixture numbers (5 pods, 500m/640Mi, 2500m/2560Mi), because the checklist cuts them; assert them from `-o json` (B23). The unit golden and a kind run match today. Done: the e2e's `-o json` run asserts each number from AT-10's full reason.
- [x] AT-06 needs one unscoped quota with all five keys (DESIGN.md section 5). Two unscoped quotas that split the keys (one for requests, one for limits and pods) bound the namespace just as well but FAIL today; decide whether AT-06 reads the union (Checkpoint 3 question). Done (D107 = union): AT-06 passes when the unscoped quotas together set the five keys and names which quota sets which; a quota that sets all five alone is named alone.

### Phase 3 /review (2026-10-20)

Found by /ponytail-review, gstack /review (specialists, Red Team, Claude adversarial, Codex) and its kind QA smoke; decisions D110 to D115.
- [x] D110: six wrong outputs. The headroom multiply wrapped int64 on a huge peak (now `Quantity.Mul`); AT-10 FAILed from a partial sum while HPAs were unreadable (now SKIP: an HPA can lower replicas); an empty `scopeSelector: {}` counted as scoped (reproduced on kind); a pod-level limit with no pod-level request counted as the request (now the containers' summed requests, as `SetDefaults_Pod`); AT-09 named AT-04 "blocking every probe" after a partial AT-04; AT-08's PASS said "every pod is denied" next to allow-all policies (now "default deny for ...", and the note names policies that select every pod and allow traffic).
- [x] D112: AT-07 FAILed two LimitRanges that split the Container defaults, and AT-10 took defaults from the first range only, though LimitRanger fills each missing value from every range (reproduced on kind). Both now merge the ranges; each value comes from the first range by name that sets it.
- [x] D111: AT-10 counted a value a container leaves unset as 0 and could PASS a rollout whose pods the quota refuses ("must specify", reproduced on kind). It now FAILs naming the quota, workload and keys; it SKIPs for unreadable LimitRanges only when such a tracked value is unset; the note says "left a cpu or memory request or limit unset".
- [x] D114: tests for an AT-08 deny whose selector uses matchExpressions, AT-09 and AT-10 runs that end mid-check, and pod-level requests without limits; the e2e fails a run above 40 API requests (CONSTRAINTS.md); the AT-10 note names at most three RuntimeClass workloads; "every AT-10 reason starts with estimate" now says PASS, FAIL and SKIP.
- [x] D113: AT-09 keeps PASS with a "not tested" note when the update/patch question fails, and leaves a run that ends during it to the runner (no change; test added).

### Phase 3 /ship review (2026-10-20)

Found by the /ship re-review (specialists, Red Team, kind QA smoke) and its Step 11 adversarial passes (Claude, Codex); decisions D119 to D135.
- [x] A huge maxSurge percent (the API bounds none) wrapped the AT-10 peak negative: "pods -9223372036854774809 of 20" as a PASS on kind. The scaled surge is clamped at MaxInt32 (D110 class).
- [x] D119: namespaces/status and namespaces/finalize both change labels (proven on kind); AT-09's D105 note asks them too. Tenant e2e runs now use 40 of the 40-request budget until B27.
- [x] D120: D111's refusal FAIL keeps; the hint and DESIGN.md say a webhook that sets sizes only on pods is not seen. Kyverno's autogen writes the template and is seen (proven on kind).
- [x] D121: AT-10 names only the LimitRanges a container took a default from.
- [x] D122: AT-09 treats any PSA layer among AT-04's admission layers as a cluster default.
- [x] Small: the AT-04 probe count comes from `at04Results`; the PSA layer is `probe.LayerPSA`; the e2e budget gate also fails when it counts no requests; comment and doc fixes.
- [x] Cycle 2 (D125, D126): AT-09 notes a pinned enforce-version other than latest (restricted at v1.7 admitted the AT-04.5 pod on kind while AT-09 passed); AT-10 counts spec.replicas raised to the largest minReplicas when several HPAs target one workload, since the controller scales none (AmbiguousSelector on kind), and names them in the note. Small: test rows for an alias quota's PASS text, the limit-side default credit, realistic LimitRange fixtures, AT-09 reading Classify's real layers; ADR 0006 and the TODOS request count.
- [x] Cycle 3 (D128): the AT-10 note names what the peak counts (Deployments and StatefulSets at template sizes) and what it does not (other workloads, sizes set at admission). The "several HPAs" clause names at most three workloads (D114 rule); stale comments fixed.
- [x] D132 (after the cap; no full panel read it again): a pod that sets any pod-level request or limit skips quota admission's "must specify" check, so AT-10 no longer FAILs it as refused, though unreadable LimitRanges still SKIP it, since LimitRanger fills its containers (a regression in the first D132 commit, found by the Codex adversarial re-run); a pod-level limit with a container request of 0 takes 0 as the pod's request, not the limit (Claude adversarial and Codex, both from upstream source).

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
- [x] Verify every Registry MTB ID against kubectl-mtb before `-o wide` and `-o json` show them (B22): AT-04.2 and AT-05 both carry `MTB-PL1-BC-HI-1`. Done: all ten match the benchmark configs at `kubernetes-sigs/multi-tenancy` 44dad58 (2026-10-20); upstream itself gives hostPath and NodePort the same ID; AT-07 and AT-08 have no upstream match.
- [x] An SSAR answer without `status.allowed` reads as a denial; require the field when AT-02 and AT-03 reuse `can()` (B11). Done: an answer with no `allowed` field is ERROR with the not-the-API-server hint; the request carries no status, so a proxy that echoes it back is caught too (D64, Phase 2 review).
- [x] A namespace GET that fails with a 5xx lets the run continue; only a 403 should (B18, with AT-09). Done: any other error exits 2 "could not read namespace <n>: <error>" (DESIGN.md section 6).
- [x] The runner abandons a check 100 ms after the deadline, so a grant held by a check stuck in a credential helper is lost; consider reporting grants as they are proven (B11). Done (D61): checks hand each proven FAIL to the runner, which keeps it when a check never returns or crashes.
- [x] An interrupted run turns a proven FAIL (exit 1) into exit 2 (DESIGN.md section 8 already lists "interruption after a FAIL" for B16). Done (D63): a FAIL proven before the signal exits 1, as after --timeout.
- [x] `--as <user>` without `--as-group` tests the user without its identity-provider groups; warn in the header (B25). Done (D141): a `warning:` header line and `-o json` `warnings`.
- [x] `--debug` will print whole dry-run Pod answers once AT-04 lands; decide what to redact (webhook-injected env) with the stderr golden (B25). Done (D140, D145): an accepted create shows only its kind, metadata and the probe's security fields; `internal/cli/testdata/stderr-trace.golden`.
- [x] CONSTRAINTS.md lists an envtest gate (AT-01 to AT-03 under real RBAC); `test/envtest/` does not exist yet (B26). Done: `make envtest` runs `test/envtest/rbac_test.go` (build tag `envtest`) on the setup-envtest 1.37 binaries in the ci job; five RBAC fixtures and the `--as-group` exit 2.

### Review follow-ups from the Phase 2 review (2026-10-13, D74)

Found by the end-of-phase review (specialists, red team, Claude and Codex adversarial passes) and left for the card that owns them.
- [x] `--debug` now prints whole dry-run Pod answers, which can carry values a mutating webhook injected; the README tells users to paste `--debug` into issues (B25, with the existing redaction item). Done (D140).
- [x] A tenant that may `update` or `patch` its own Namespace can remove the PSA label that made AT-04 PASS; AT-09 should ask it (B18, D81). Done (D105 = note): an AT-09 PASS asks update, then patch, on the namespace object and notes either grant; the verdict stays.
- [x] A misspelled `--other-namespace` gives a silent AT-02 PASS; a tenant cannot tell a missing namespace from a forbidden one. Warn when the name differs from common system namespaces, or document it (B25, D87). Done (D141): any value other than `kube-system` gets a `warning:` line.
- [x] `-o json` and the B23 schema must carry each result's `note` (what it did not test, D86). Done: `note` is in the schema and the JSON golden.
- [x] A mutating policy that sets `allowPrivilegeEscalation: false` when it is absent turns AT-04.1's first form into a 422 validation error, which reads as "a bug in audit-tenant" (ERROR, exit 2), and `worse` keeps it over the second form's verdict; an API server with `--allow-privileged=false` gives the same 422 for both forms. Read these 422 texts as their own outcomes (B22; /ship final review). Done (D138): `--allow-privileged=false` is PASS on the layer `API server (allow-privileged=false)` (captured on kind); the mutated form is SKIP and AT-04.1 lets the other form decide.

### Closed in the sort of 2026-10-22

Checked against the code on `phase-6-docs` (from `1778b02`). One more item was merged into its Phase 2 twin (the accepted-probe mutation item, now in `TODOS.md` group 7).
- [x] B26 and B27 were ticked on local runs; confirm the `make envtest` step (`check` job) and the `make bench` step (`e2e` job) green on the PR's first CI run. Done: PR #7's first CI run has `check` (it runs `make envtest`) and `kind` (it runs `make bench`) green.
- [x] This file's "Phase 4 /review" header names D143 to D146 only; the two deferred items there do not name D150 and D152 (maintainability). Done: the sort gave each item its own decision id (D150 and D152).
- [x] The /ship review stopped at its 3-cycle cap without converging (D131): cycle 1 fixed 4 kind-confirmed defects, cycle 2 two, cycle 3 only wording; each cycle found smaller AT-10/AT-09 edges. Expect the same class in Phase 4 reviews. Closed: Phases 4 and 5 hit the same limit; the review stop rule (D166) replaces this note.
- [x] A default run drops unbuilt checks without a line saying so; D28 trims the Registry at v0.1.0, or print "not in this build" until then (B31). Done: all ten checks have a `Run` function, so a default run drops none.
