# Changelog

All notable changes to this project are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- In a pod with no kubeconfig, `--as` uses the pod's service account. Before, every `--as` run there called `http://localhost:8080` and failed with `API server unreachable`. The README's kubeconfig ConfigMap is needed only for v0.1.1 and earlier.
- With no kubeconfig outside a pod, the error says `no kubeconfig`.
- A failed credential plugin (such as a missing `aws` or `kubelogin`) and a request that `--request-timeout` ended now say so, not `API server unreachable`.
- A kubeconfig `proxy-url` whose user info does not parse gives a message that names the cluster only. Before, the error printed the whole URL, password included.
- A service-account name that the API server cannot split (another `:`, or an invalid namespace) is treated as a plain user, as the API server treats it. `--as-group system:authenticated` alone gets the no-groups warning.

### Changed

- `-o policyreport` accepts `--skip` and writes a `skip` row for each skipped result, so a shop that enforces pod security with a policy engine can skip AT-09 and keep a clean dashboard. `--checks` is still refused. Skip rows do not change the exit code.
- `-o policyreport` refuses a run where every result is SKIP, and a run whose identity the cluster changed (added or dropped groups, or another user), with exit 2. Such a report would replace the identity's report with results that do not describe it.
- The checklist warns when the cluster drops a group that the run asked for or names another user, and when an `--as` identity is unverified.
- `-o json` and `-o policyreport` also print their warnings on stderr.
- An empty `--as-group` exits 2. The README pipeline example passes `--as-group` only when its variable is set.
- `--plain` output is ASCII only: other characters in server text print as `?`.
- Text output ends with a line that names the tool version, the run time, the server and the Kubernetes version. The header names the server when no kubeconfig context exists.
- The `--other-namespace` warning says that the namespace is unconfirmed.
- `version` says when a `go install` build recorded no commit or date.
- RBAC FAILs (AT-01 to AT-03) name the binding that grants the access, from the API server's answer, in the reason and the fix line. In self mode, the fix line starts with `ask a cluster admin to`.
- The AT-05 fix line names the built-in fix first: a ResourceQuota with `services.nodeports: "0"`. The AT-04.5 fix line says that a ValidatingAdmissionPolicy needs Kubernetes 1.30 or newer.
- AT-10's FAIL reason puts the shortfall first (`requests.cpu short by 2: peak needs 6, hard is 4`), names at most three refused workloads, then "and N more", and compares with the quota's `status.hard` once the quota controller sets it.
- A PASS from an explicit deny notes that an authorizer other than RBAC gave it, such as an authorization webhook or an access proxy.

- AT-10 no longer fails a workload scaled to 0 for a container value it leaves unset, and names a `maxSurge` that the Deployment controller cannot read instead of counting it as 0.
- A ValidatingAdmissionPolicy whose message quotes the Pod Security text is reported as the policy, not as Pod Security Admission.
- A control pod that `--allow-privileged=false` refused (a mutation added a privileged container) gets the hint for that case.
- The reasons for an `--allow-privileged=false` refusal and for a mutated privileged probe no longer carry the random probe name, so a PolicyReport message stays the same between runs.
- The AT-10 note on several HPAs that target one workload says what the HPA controller still does.

### Removed

- The `completion` command. It made a script for a command named `audit-tenant`, which kubectl never runs.

## [0.1.1] - 2026-10-30

### Changed

- A run where every result is SKIP now exits 2, not 0, and says `nothing was proven` on stderr in every output format. A misspelled `-n`, `--as` or `--as-group` gives such a run, and exit 0 let it pass a pipeline. With `--fail-on-skip` it still exits 1 (ADR 0004, amended). A `--checks` subset whose results are all SKIP exits 2 too.
- The AT-09 fix line starts with `kubectl label --dry-run=server ...`, which warns about running pods that the level would refuse, so a pasted fix changes nothing. The AT-06 fix line, and AT-10's when no quota exists, say to make sure a LimitRange gives defaults first.
- `--help` shows the namespace and `--as` in the usage line, four examples, and the new exit-0 rule.
- The krew manifest's caveat says that exit 2 also means every check was SKIP. Its short description and description match the krew index entry.

### Added

- README: "Run it in a pod", with a kubeconfig for `--as` in a CronJob or a pipeline step, and impersonate rights scoped with `resourceNames`.

## [0.1.0] - 2026-10-26

The first release.

### Added

- Ten tenant-governance checks that run as the tenant against one namespace and give fourteen results:
  - AT-01 to AT-03: cluster-scoped access, Secret access in another namespace, and privilege escalation, asked with SelfSubjectAccessReviews.
  - AT-04.1 to AT-04.5 and AT-05: privileged containers, hostPath volumes, hostPID, hostNetwork, privilege escalation and NodePort Services, sent as server-side dry runs after a compliant control probe.
  - AT-06 to AT-09: a complete ResourceQuota, LimitRange defaults, default-deny NetworkPolicies and the Pod Security Admission label.
  - AT-10: an estimate of whether the quota covers the rollout peak of the Deployments and StatefulSets.
- Self mode (the kubeconfig's identity) and impersonation (`--as`, `--as-group`), with warnings for impersonation flags that leave out groups.
- Tested behind Teleport 18: tenant runs through the proxy give the same verdicts as direct runs when the proxy role passes the requests through. For an impersonated user that the SelfSubjectReview confirms, a warning names the groups the proxy adds. A startup answer from the server or a proxy (an HTTP error, or a page with no version) is reported with its HTTP status, not as an unreachable server.
- Four verdicts (PASS, FAIL, SKIP, ERROR). Each result has the deciding layer, the MTB ID (`-` when none exists) and a reason. Every FAIL, SKIP and ERROR also has a hint, and a note says when part of a check was not tested.
- Exit codes 0, 1 and 2, and `--fail-on-skip` for pipelines.
- Output formats: checklist (default), `-o wide`, `--plain`, `-o json` with a JSON Schema (`docs/report.schema.json`), and `-o policyreport` (`wgpolicyk8s.io/v1alpha2`).
- Machine output: `-o json` and `-o policyreport` show a control or format character in server text, other than newline and tab, as `<U+XXXX>`. A PolicyReport names its mode, kubeconfig context and server in annotations. Both formats carry the "looks like a cluster admin" warning. The JSON Schema allows added fields; a removed or renamed field gets a new `apiVersion`.
- `-o policyreport` refuses a run that `--timeout` or a signal stopped before every check finished, unless the run proved a FAIL: then it prints the partial report (exit 1). The refusal names the SKIPs that `--fail-on-skip` counts. After a create that was not verified as a dry run, stderr and the timeout hints name the command that lists leftover probe objects.
- The tool refuses a kubeconfig server URL that holds user info (a name or token before the `@`), so no error message can print it.
- `--debug`: one line per API request and the identity steps on stderr, without request headers.
- `--checks`, `--skip`, `--other-namespace` and `--timeout`.
- The `list-checks` and `version` subcommands.
- A read-only HTTP transport with an exact allowlist: the tool never writes to the cluster and never reads a Secret.
- Binaries for Linux and macOS (amd64 and arm64) and Windows (amd64), with checksums and a krew manifest.

[0.1.1]: https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/tag/v0.1.1
[0.1.0]: https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/tag/v0.1.0
