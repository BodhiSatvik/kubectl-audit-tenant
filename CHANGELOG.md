# Changelog

All notable changes to this project are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - Unreleased

The first release.

### Added

- Ten tenant-governance checks that run as the tenant against one namespace and give fourteen results:
  - AT-01 to AT-03: cluster-scoped access, Secret access in another namespace, and privilege escalation, asked with SelfSubjectAccessReviews.
  - AT-04.1 to AT-04.5 and AT-05: privileged containers, hostPath volumes, hostPID, hostNetwork, privilege escalation and NodePort Services, sent as server-side dry runs after a compliant control probe.
  - AT-06 to AT-09: a complete ResourceQuota, LimitRange defaults, default-deny NetworkPolicies and the Pod Security Admission label.
  - AT-10: an estimate of whether the quota covers the rollout peak of the Deployments and StatefulSets.
- Self mode (the kubeconfig's identity) and impersonation (`--as`, `--as-group`), with warnings for impersonation flags that leave out groups.
- Four verdicts (PASS, FAIL, SKIP, ERROR). Each result has the deciding layer, the MTB ID (`-` when none exists) and a reason. Every FAIL, SKIP and ERROR also has a hint, and a note says when part of a check was not tested.
- Exit codes 0, 1 and 2, and `--fail-on-skip` for pipelines.
- Output formats: checklist (default), `-o wide`, `--plain`, `-o json` with a JSON Schema (`docs/report.schema.json`), and `-o policyreport` (`wgpolicyk8s.io/v1alpha2`).
- Machine output: `-o json` and `-o policyreport` show a control or format character in server text as `<U+XXXX>`. A PolicyReport names its mode, kubeconfig context and server in annotations. Both formats carry the "looks like a cluster admin" warning. The JSON Schema allows added fields; a removed or renamed field gets a new `apiVersion`.
- `-o policyreport` refuses a run that `--timeout` or a signal stopped before every check finished, so an applied partial report never replaces a full one. The refusal names any FAIL proven before the stop (exit 1) and the command that lists leftover probe objects after an unverified create.
- The tool refuses a kubeconfig server URL that holds user info (a name or token before the `@`), so no error message can print it.
- `--debug`: one line per API request and the identity steps on stderr, without request headers.
- `--checks`, `--skip`, `--other-namespace` and `--timeout`.
- The `list-checks` and `version` subcommands.
- A read-only HTTP transport with an exact allowlist: the tool never writes to the cluster and never reads a Secret.
- Binaries for Linux and macOS (amd64 and arm64) and Windows (amd64), with checksums and a krew manifest.

[0.1.0]: https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/tag/v0.1.0
