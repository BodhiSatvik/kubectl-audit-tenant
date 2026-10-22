# Security policy

## Report a security problem

Do not open a public issue for a security problem. Report it privately with GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**, or go to <https://github.com/BodhiSatvik/kubectl-audit-tenant/security/advisories/new>. Only the repository's maintainers see the report.

Examples of security problems in this tool:

- It sends a write to the cluster, or a create without `dryRun=All`.
- It reads a Secret, or the read-only transport lets through a request that its allowlist should refuse.
- `--debug`, `-o json` or `-o policyreport` prints a credential.
- A verdict says PASS when the cluster does not hold the fence, in a way an attacker can cause.

Include the version (`kubectl audit-tenant version`), the command you ran, and the `--debug` output. Remove tokens, server names and any other private values first.

## Supported versions

Fixes go into the latest release.
