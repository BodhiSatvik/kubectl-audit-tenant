# audit-tenant: TODOs

Build list for this repo. Lab-level work (outreach, pilots, evidence) stays on the lab board (`../../backlog/`). Done items are in `_archive/TODOS-done.md`.

## v0.1

Build list from the approved spec (`DESIGN.md`, approved after plan reviews 2026-10-11) and the plan reviews' task lists. Order is the build order: a walking skeleton first, then checks by cost, then output formats, then the full test matrix, then docs and release. Phases and checkpoints: `tasks/plan.md`. Definition of done for every task: `make check` passes (CONSTRAINTS.md) and the task's own acceptance criteria hold. No effort numbers by Satvik's rule. Tasks marked (Satvik) are human steps.

### Phase 6: docs and release

- [ ] **B29 README.** Problem in two sentences; quick start on kind with the three install paths; the check table with the AT-04.5 note; "how this differs" (popeye, kubescape, kyverno audit mode, kubectl-mtb, NamespaceHound, kuota-calc, the can-i script); safety section with the audit-log note and "what a PASS does not prove"; troubleshooting (one entry per SKIP/ERROR hint, `--debug` line; the "timed out during startup" entry says a hung credential helper may keep running after the tool exits); pipeline example with `--fail-on-skip`; prior art and citation.
  - AC: quick start works on a clean machine with Docker, kind and kubectl in five minutes (acceptance criterion 9); `hack/check-public.sh` passes.
  - Verify: fresh-machine run (Satvik or a clean VM); `make check-fast`.
  - Depends on: B21, B25. Files: `README.md`.
  - Status (2026-10-22): built on `phase-6-docs`. `hack/check-public.sh` and `make check-fast` pass. The quick start, run word for word on this Mac with a local build in place of the release, took 17 s (node image cached) and printed the verdicts the README shows. Open: the clean-machine run with a release install, after v0.1.0 exists (Satvik or a clean VM).
- [ ] **B31 Pre-release gate (Satvik and agent).** `audit-tenant.dev` is registered (done 2026-10-11, D20); `../../scripts/clean-room-check.sh .` passes; CONSTRAINTS.md "Measured" values recorded; `make check-full` green on main; Satvik pushes and tags `v0.1.0`; release job publishes five archives, checksums and the krew manifest; Satvik opens the krew-index PR.
  - AC: acceptance criteria 1 to 11 in DESIGN.md section 12 all met; the release page shows the assets; `kubectl krew install --manifest-url` works from the release.
  - Verify: release workflow; manual install on macOS and Linux.
  - Depends on: B27, B28, B29, B30. Files: `CONSTRAINTS.md`, `TODOS.md`, release assets.

**Checkpoint 6 (v0.1.0):** every acceptance criterion met; `evidence/` row in the lab for the first outside install when it happens.

### Open review items, sorted (2026-10-22)

The review sections of Phases 1 to 5 are sorted here by what they block. Each item keeps its text; the prefix names the review that found it. Done items are in `_archive/TODOS-done.md`; the old grouping is in git history (`3f24d79`).

#### 1. README (card B29)

All seven items are done; they are in `_archive/TODOS-done.md`.

#### 2. Decide before v0.1.0 (Satvik)

All fourteen items are decided (D169 to D186) and built; they are in `_archive/TODOS-done.md`. The proxy-url item from the same Red Team is in group 4 (D181).

#### 3. Release and repository settings (card B31)

- [ ] Turn on GitHub private vulnerability reporting (Settings, Security) when the repo goes public. `SECURITY.md` already points reporters there (D168, 2026-10-22; Satvik, B31 with TASK-58).
- [x] P1 /ship: Release: goreleaser `release.draft: true`, publish only after the krew-install job passes; check the krew download checksum; release only tags on main (B31). GitHub Actions SHA pinning (D43) before the public release. **Built on `b31-release-hardening`, with the B31 review fixes D188 to D190: goreleaser (built with `go install`) makes a draft; krew installs the build's archives on ubuntu and macos; the workflow publishes the draft only when its `checksums.txt` equals the tested build's, then installs from the `--manifest-url`. One run per tag; the tests run in a read-only job; no Go cache; the checked krew v0.5.0 binary runs itself (`hack/install-krew.sh`, which `ci.yml` also runs on every PR against the snapshot); every action is pinned to a commit SHA. A tag whose commit is not on `main`'s first-parent history fails the release, but the check runs in the tagged commit's own workflow, so the tag ruleset below is the binding control.**
- [ ] B31 /review (security specialist, Claude adversarial, 2026-10-25): a tag push runs the `release.yml` of the tagged commit, so a tag on an older commit or on a branch skips the new checks. Add a tag ruleset on `refs/tags/v*` that lets only the maintainer create, update or delete those tags (it also stops a released tag from moving), and turn on immutable releases. The rulesets API needs a public repo (Satvik, with TASK-58).
- [x] P1 /ship: Teleport-style access proxies (D57) are covered by unit tests only; try one live before the release (B31). **D195 (2026-10-26): tried live with Teleport 18.11.1 (the official container) on the kind e2e cluster. Self-mode runs through Teleport matched direct runs (14 of 14 verdicts for tenant-a and tenant-b). A narrow Teleport role (pods in tenant-a only) gave explicit-deny PASSes for AT-01 to AT-03 and AT-05, SKIPs for AT-06 to AT-08 and AT-10, and an unverified identity. D196 fixed the two outputs that misled: an HTTP answer at /version read as unreachable, and no warning for the groups Teleport adds to `--as` (`system:masters`).**
- [ ] P5 /review: A pull request still runs its own copy of `ci.yml`, so it can drop the `floor-guard` job. Only a required workflow or ruleset that the branch cannot edit (or a `pull_request_target` job that runs no branch code) makes "the base copy judges" hold (Claude adversarial, cycle 3; Satvik's GitHub settings, B31).
- [ ] P5 /review: On a push to main the floor guard diffs main against itself and passes, so the bar on main rests on branch protection: make `ci` and `e2e` required checks before the public release (Claude adversarial; Satvik's GitHub setting, B31).

#### 4. Small fixes, no decision needed

Wording, hints, edge cases and tests. None changes a public contract; do them before v0.1.0 if time allows, else in v0.1.1.
- [ ] B31 /review Red Team: a cluster `proxy-url` with user info that does not parse (a password with a literal `%`) fails in `ToRESTConfig`, and clientcmd's error quotes the whole proxy URL on stderr (reproduced). D169 and D179 cover only the server URL. Return a fixed message when the selected cluster's proxy URL holds an `@`, or narrow the README and CHANGELOG claims to the server URL. **D181: v0.1.1; kubectl prints the same error, and the docs promise only the server URL.**
- [ ] D195 live test: behind a Teleport role that denies by itself, AT-01 to AT-03 and AT-05 PASS with the layer `RBAC`, though Teleport's authorizer, not Kubernetes RBAC, gave the explicit deny. Name the deciding layer `authorizer` for an explicit deny, or add a note that an access proxy can answer SelfSubjectAccessReviews.
- [ ] D196 /review (Claude adversarial, 2026-10-26): the added-groups warning looks only for added groups. Warn too when a group asked for with `--as-group` is missing from the SelfSubjectReview, or when its username differs from `--as` (a proxy that dropped or ignored the impersonation); the API server never does either, so neither can fire falsely on a direct cluster. A run whose `--as` identity is unverified cannot see proxy-added groups at all; say so.
- [ ] D196 /review (Claude adversarial): `-o policyreport` prints a report whose identity carries groups a proxy added (such as `system:masters`); its FAILs do not describe the tenant, and the warning travels only as an annotation. Decide whether it refuses such a run, as D147, D151 and D175 do.
- [ ] D196 /review: tested only behind Teleport. Rancher, Pinniped, the Tailscale API server proxy and OpenShift (which adds no groups to an impersonated user) are not checked, and whether Teleport forwards only the `--as-group` groups is not verified.
- [ ] Docs review (Codex, 2026-10-22): `test/e2e/bench.sh` writes "kind v1.37.0" in BENCHMARK.md's Setup column, but v1.37.0 is the Kubernetes node image, not the kind version (CI uses kind v0.33.0). Write "Kubernetes v1.37.0 on kind <version>" in new rows.
- [ ] Docs review (2026-10-22): cobra adds a default `completion` command, but its script completes `audit-tenant`, not `kubectl audit-tenant`. Hide it (`CompletionOptions.DisableDefaultCmd`), or ship a `kubectl_complete-audit_tenant` script as krew plugins do.
- [ ] P4 /ship pass 2: The D151 refusal text says "use --as, or -o json"; on a transient SelfSubjectReview 5xx or 429 the fix is a re-run, a tenant can rarely impersonate, an in-cluster run prints `user entry ""`, and the `--debug` identity line still mentions a header that never prints (Red Team).
- [ ] P4 /ship pass 3: A control pod refused by `--allow-privileged=false` (a webhook added a privileged container to every pod) gets the hint "the namespace rejects even a compliant pod", not the added-privileged hint (Claude adversarial).
- [ ] P4 /ship pass 3: Service accounts are found by the `system:serviceaccount:` prefix only; a name the API server does not split (`system:serviceaccount:ns:name:x`, an upper-case namespace) is impersonated as a plain user but warned and keyed as a service account (Claude adversarial).
- [ ] P4 /ship pass 3: `--as bob --as-group system:authenticated` has the same groups as `--as bob` but gets no no-groups warning (Claude adversarial).
- [ ] P4 /ship pass 3: `--debug` shows a non-JSON 2xx create answer only as "not a JSON object", which hides what a user needs for the D79 ERROR (a proxy's HTML page); show its content type and length (Claude adversarial).
- [ ] P4 /ship pass 3: Rules 4b and 4c keep the whole server text, so their reasons carry the random probe name (and 4c a security context dump); the PolicyReport `message` changes on every run. Use `from()` as the other PASS rules do (Claude adversarial).
- [ ] P4 /ship pass 3: `--plain` drops color and the separators but not other non-ASCII server text, which `-o wide` now prints whole; `TestWideSanitizesLayerAndReasonAndStaysASCIIWhenPlain` does not prove ASCII (Claude adversarial rerun).
- [ ] P3 /ship: D133: AT-10's reason names every refused workload and quota with no cap; use `firstThree` with "and N more", as the HPA and RuntimeClass clauses do (D114 rule; Claude adversarial).
- [ ] P3 /ship: D133: the D126 note says the HPA controller "scales none" of the HPAs that target one workload, but kind showed it still clamps the replicas to `maxReplicas`; reword the note (Claude adversarial, cycle 3 kind check).
- [ ] P3 /ship: D135: a maxSurge percent of 2^63 or more passes API validation but does not fit in 64 bits; `intstr.GetScaledValueFromIntOrPercent` returns an error, AT-10 counts surge 0, and the comment in `headroom/peak.go` says the error is never set. The Deployment controller fails every sync for such a value, so the quota does not block it; report the bad maxSurge instead (Codex adversarial re-run).
- [ ] P3 /ship: D133: a ValidatingAdmissionPolicy whose message quotes "violates PodSecurity" is classified as PSA, because that rule comes before the policy rule in `probe/classify.go`; AT-09's FAIL then names a PSA cluster default that does not exist (Claude adversarial; D102 class).
- [ ] P3 /ship: D133: a Deployment scaled to 0 whose containers leave a tracked value unset gets the AT-10 refusal FAIL, though its peak is 0 and the quota refuses nothing until it scales up (Claude adversarial).
- [ ] P3 /ship: D133: quota admission compares usage with `status.hard`, but AT-06 and AT-10 read `spec.hard`; the two differ until the quota controller syncs a new `spec.hard` (Claude adversarial).
- [x] P4 /ship pass 3: `TestServerLeavesOutUserinfo` reads `c.Server` when `Connect` failed, so a failing change panics instead of failing the test; split the check (testing). Done on `b31-pre-release`: `TestServerLeavesOutQueryAndFragment` stops on the error first (D169 replaced the userinfo case).
- [ ] P4 /ship pass 2: `TestPolicyReportNeedsAVerifiedIdentityInSelfMode` does not prove the refusal comes before any check; assert the server saw only the startup requests (testing).
- [ ] P4 /ship pass 2: `TestAT04OneResultTakesTheWorstOfItsForms` keeps the row "rejected, then mutated", which the stronger test also covers (testing).

- [ ] P2 customer test run S5-F4 (D197): in a pod with no kubeconfig, any `--as` run calls `http://localhost:8080`: client-go's `DeferredLoadingClientConfig.ClientConfig()` returns the merged config, not the in-cluster one, once the impersonation overrides make it differ from the default. Start from `rest.InClusterConfig()` when no kubeconfig loads and in-cluster config is possible, then apply the impersonation flags; add an e2e Job case; then drop the kubeconfig ConfigMap from the README "Run it in a pod" section.
- [ ] P2 /review Claude adversarial (D201): an all-SKIP `-o policyreport` run prints its report and exits 2. The README's apply script applies any non-empty report, so an identity whose RoleBinding was removed replaces its FAIL rows with SKIP rows on the dashboard (true before D197 too, with exit 0). Decide whether to refuse such a report, as D147 and D175 refuse partial ones.

#### 5. Question-set pass: what the checks ask (v0.2, D176)

D176 (2026-10-23): all of group 5 waits for v0.2. The v0.2 pass starts with the first item, the request budget, before it adds any question.

Each item adds questions or probes, and run 1's identity already uses 40 of its 40 requests. The PASS notes name the gaps today.
- [ ] P5 /review: Run 1's identity uses 40 of 40 requests, and client-go retries count, so one 429 or webhook 5xx retry fails e2e and bench with no code change. Count first attempts apart from retries, or win requests back (Red Team, cycle 2; B31 question-set pass).
- [ ] P2 /review: Redesign AT-04's and AT-05's RBAC-only branch around one SelfSubjectRulesReview (one request; SSAR fallback when `incomplete`): it would cover every way to get a pod or Service, including the in-namespace paths the PASS note names today and the paths that make a controller create pods from an existing template (raising an HPA's minReplicas, deleting or evicting pods; Codex /ship final round) (creating Secrets of type service-account-token, RoleBindings, service account tokens, impersonating local service accounts). A read-only tenant's run already costs about 63 requests (31 controller SSARs, plus up to 11 for AT-06 to AT-10, 5 of them AT-09's D105/D119 SSARs), over the 40 budget (B27, D77; the e2e now fails a run above 40). D159: decided in the B31 question-set pass.
- [ ] P2 /review: AT-03 does not see grants from RoleBindings in other namespaces (`impersonate` on kube-system service accounts, `create serviceaccounts/token` or `create pods` there), changes to webhook configurations or ValidatingAdmissionPolicy bindings, or `get nodes/proxy`. The PASS note names the first; decide the question set before v0.1.0 (B31 spec pass).
- [ ] P2 /review: AT-03 asks no cluster-scope `escalate`/`bind` on namespaced `roles` and no cluster-scope `create serviceaccounts/token`; a ClusterRoleBinding granting either is cluster-wide escalation, and AT-03 still PASSes (D62 dropped only the in-namespace questions). Add the three questions with no namespace, update the DESIGN.md row and the PASS note (B31 question-set pass; /ship final review, red team).
- [ ] P2 /review: AT-03 does not ask about approving CertificateSigningRequests (`update certificatesigningrequests/approval`, `approve` on signers); add it to the B31 question-set pass.
- [ ] P2 /review: AT-04 probes test regular containers only; add an initContainers form, allowed hostPath prefixes, and a hostPort probe for AT-04.4's MTB ID, or narrow the MTB tags (B22 with the MTB ID check, D78). D139: keep the tags, whose PASS notes name the untested forms; the extra probes cost requests, so they wait for B27's budget. D159: B31 question-set pass.
- [ ] P2 /review: Running as root (`runAsUser: 0`) left AT-04.5 in D78; decide whether it becomes its own result (B31 spec pass).
- [ ] P2 /review: When the tenant may create pods, AT-04 does not ask about `pods/ephemeralcontainers`; a webhook or ValidatingAdmissionPolicy whose rules list `pods` does not match that subresource, so a policy PASS can leave privileged ephemeral containers open (PSA covers them). The AT-04.1 and AT-04.5 notes say so now; ask update/patch on the subresource on the probe branch and SKIP or note a non-PSA PASS when allowed (B22, D97; /ship review pass 2). D139: two more SSARs per run; waits for B27's budget. D159: B31 question-set pass.
- [ ] P2 /review: A tenant bound to `admin` may delete namespaced Kyverno Policies (the chart aggregates that right) or create PolicyExceptions, so a webhook PASS can be liftable (B31 question-set pass, D81).
- [ ] P2 /review: Exemptions keyed on fields the tenant controls (PSA `exemptions.runtimeClasses`, Gatekeeper `exemptImages`, Kyverno exclusions by label or name) make a probe's rejection unrepresentative (B31 question-set pass, D87).
- [ ] P2 /review: Probes are easy to recognize (fixed generateName, label, annotation, dryRun=true), so a tenant that may create its own admission policies could deny only the probes and fake a PASS; extends the liftable-fence items (B31; /ship final review, Claude adversarial). D138 adds a weaker path: a tenant policy that sets `allowPrivilegeEscalation: false` on both privileged forms turns AT-04.1 into SKIP (Phase 4 /review, Claude adversarial).
- [ ] P2 /review: Unverified (red team, confidence 3/10): a policy engine whose own rule errors under failurePolicy Fail may also answer "admission webhook ... denied the request", which rule 1 reads as a PASS, as D99 handles for ValidatingAdmissionPolicy. Confirm Kyverno's and Gatekeeper's error-rejection texts and add them as ERROR before rule 1, or close this item (B31 spec pass; /ship review pass 3).
- [ ] P2 /review: A stored dangerous probe (dryRun=All dropped) is ERROR (exit 2), not FAIL, although admission accepted the object; decide whether it becomes FAIL with the stored-object warning (B31; /ship final review, Claude adversarial).
- [ ] P2 /review: ResourceQuota is the last validating admission plugin, after PodSecurity, ValidatingAdmissionPolicy and the webhooks, so an "exceeded quota" answer to a dangerous probe means every security gate admitted it. Today it is SKIP (a full non-zero `services.nodeports` quota too, D76) and a control rejected by quota makes all of AT-04 SKIP. Decide whether a probe's quota rejection with a non-zero limit is FAIL and whether the probes still run after a quota-rejected control; the hint no longer claims quota runs before the gates (B31 spec pass, D96; /ship review pass 2).
- [ ] P3 /review: AT-08 does not ask whether the identity may delete, update or patch the deny policies, the gap D105 closed for AT-09's label; a PASS note like D105's (Claude adversarial; B22 or later).
- [ ] P3 /review: AT-09 passes on the label alone even when the same run's AT-04 shows PSA did not enforce it (an exempt namespace or user); a note or FAIL from `spec.earlier` (Red Team; needs a decision).
- [ ] P3 /review: AT-10 counts a Deployment scaled to 0 with an HPA at maxReplicas plus surge, though the HPA does not scale a target at 0 (Claude adversarial; KEDA scales such targets back up, so decide with B27). B27 closed without it: decide in the B31 spec pass (Phase 5 /review).
- [ ] P3 /ship: D129: the HPA controller also stops scaling HPAs whose targets' pods overlap by labels (web's app=web selector matching web-v2's pods); AT-10 counts each at its maxReplicas and can FAIL falsely. Detect overlapping selectors across counted workloads (with B27). B27 closed without it: decide in the B31 spec pass (Phase 5 /review).
- [ ] P4 /ship pass 3: Rule 4c reads one mutation-made validation error only. A mutating policy that sets `hostUsers: false` makes the hostPID and hostNetwork probes fail validation, which now reads ERROR "a bug in audit-tenant" (exit 2); decide whether every 422 on a field the probe never sets is a mutation (Claude adversarial rerun; wording not captured on kind).

#### 6. Quality gates and CI (after v0.1.0)

The floor guard and the benchmark evidence. None changes what a user sees.
- [ ] PR #10: The floor guard diffs with `--text` on purpose (`hack/floor-guard.py`), so a changed binary file gives one `[carriage-return]` finding per changed line: 336 findings for the B30 `docs/diagrams/architecture.png`. Report one finding per binary file instead; `git diff --numstat` shows `-` for a binary file (found on PR #10, 2026-10-22).
- [ ] P5 /review: The request budget counts lines of the `--debug` trace, which `internal/cli/run.go` prints; that file is not protected, so a change to the printer changes the count. Count in `internal/safety` (the Recorder) or at the API server (Claude adversarial, cycle 3; B31).
- [ ] P5 /review: A Measured record's reference moves with each new record: coverage can fall 0.5 points per pull request, and a size can grow 10% per pull request within one minor release. Judge coverage against its best record and size against the last release tag's record (state-machines and Codex, cycle 3; B31).
- [ ] P5 /review: Outside the protected paths, a weakened test still passes the guard: an early `return`, a looser `if`, or a table row removed from a test that stays (only removed assertion calls, skips and build tags are caught). Decide whether `internal/` test files join the protected set or each test reports what it checked (Claude adversarial, cycle 2; B31).
- [ ] P5 /review: A Measured record that is never updated always passes: coverage can fall below its Today value with no finding. Have `hack/coverage-gate.sh` read the coverage Today cell and fail past the tolerance (Red Team, cycle 2; B31).
- [ ] P5 /review: The CI bench rows name the pull request's merge commit, which disappears; run bench on pushes to main with the commit as `BENCH_VERSION`, and copy those rows (Red Team, cycle 2; B31).
- [ ] P5 /review: CI downloads the envtest binaries (about 170 MB) on every run; cache them with `actions/cache` keyed on the setup-envtest version and `ENVTEST_K8S` (performance; after v0.1.0: the D43 pinning pass shipped without it).
- [ ] P5 /review: `make bench` counts writes from the `--debug` trace, which holds only allowlisted requests; an object stored by a hop that drops `dryRun=All` (D79) would not show. Count the pods and Services in tenant-a before and after the runs if the "0 writes" row is to be evidence of that (Claude adversarial; B31).
- [ ] B31 /review (D191): Dependabot for `github-actions`, so the SHA pins get security bumps. Each bump turns the floor guard red by design, so Satvik merges each one.
- [ ] B31 /review (D191): a check that every `uses:` line in `.github/workflows/` stays pinned to a 40-character commit SHA (in `hack/check-public.sh` with a planted-line self-test, or a Go test).
- [ ] B31 /review (D191): the release install-tests only linux/amd64 and darwin/arm64. Add runner legs for linux/arm64 (`ubuntu-24.04-arm`), darwin/amd64 and windows/amd64 (a pwsh variant), each with its krew sha256 in `hack/install-krew.sh`, or name the tested platforms in the release notes.
- [ ] B31 /review (D191): `main.date={{ .Date }}` makes every build differ; `{{ .CommitDate }}` makes builds reproducible but changes what `version` prints.

#### 7. Later (v0.2 or after)

- [ ] B31 /review (Claude adversarial, 2026-10-23): D170 marks every format character, so a username with a zero-width joiner or non-joiner (U+200C, U+200D: Persian and Indic names, emoji) reads `<U+200C>` in `identity` fields and no longer equals the real username for a consumer that joins on it. A newline in server text is kept and can still forge a line in `jq -r` output. Decide whether identity fields keep ZWJ and ZWNJ, or carry the raw value next to the marked one.
- [ ] P2 /review, P4 /review: A mutating webhook can remove the dangerous field, so an accepted dry run is not proof of FAIL; check the returned object before calling it FAIL, and report PASS "admission (mutation)" when the field is gone (Later; the error is a false FAIL, the safe direction). Phase 4 /review found it again; D145 makes it visible in `--debug`.
- [ ] P2 /review: AT-05 probes only `type: NodePort`; `type: LoadBalancer` also allocates node ports by default. The PASS note says so now; decide whether to add a LoadBalancer probe (Later).
- [ ] P2 /review: On OpenShift (restricted-v2 SCC) or with registry allowlists, the control pod's fixed `runAsUser: 65532` and the public pause image are rejected, so AT-04 is SKIP; an SCC 403 on a probe matches no classifier rule (ERROR). Consider dropping `runAsUser`, adding `readOnlyRootFilesystem: true`, an SCC classifier rule and a `--probe-image` flag (Later).
- [ ] P2 /review: Probes have one valid form where policies can demand another: the NodePort Service has no selector, the hostNetwork pod the default DNS policy. A policy that requires a selector or `ClusterFirstWithHostNet` rejects the probe for an unrelated reason and fakes a PASS; add those forms (Later, D87).
- [ ] P3 /review: AT-10 compares only unscoped quotas; a scoped quota that matches every counted pod (NotTerminating, NotBestEffort, a matching PriorityClass) can be tighter and block the rollout. Evaluate scopes against the pod templates, or note the skipped scoped quotas (Red Team; v0.2 with Argo Rollouts).

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
- [ ] Argo Rollouts in AT-10 (v0.2): read as unstructured objects when the CRD exists; blueGreen = replicas + (previewReplicaCount else replicas); canary = replicas + maxSurge, or 2 × replicas when `trafficRouting` is set and `dynamicStableScale` is not (deferred in the CEO review, 2026-10-10) Also extend the kind list in `headroom.Breakdown`, which names only Deployment and StatefulSet, so a Rollout shows in the reason (D124).
- [ ] `--as-serviceaccount ns/name` shortcut; cloud identity lookups
- [ ] Detect whether the network plugin enforces NetworkPolicy
- [ ] Weekly CI run against the oldest supported server (1.28)
- [ ] krew-release-bot for releases after v0.1.0
- [ ] Migrate the e2e Kyverno policy from the legacy `restrict-nodeport` ClusterPolicy (pinned in `test/e2e/versions.env`; kyverno/policies keeps only CEL policies since 2026-09-29) to a CEL `ValidatingPolicy` at the next Kyverno bump (Checkpoint 1, D39)
- [ ] Signed releases and SBOM (v0.2): goreleaser `signs` with cosign keyless (`id-token: write`), `sboms` with syft, and a README "verify the download" section (CEO review D21)
