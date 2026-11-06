# Playbook: from a test cluster to your real cluster

For anyone who tried a playbook on kind and now wants to use the tool where it matters. You check what the tool will do before you run it, run it on one namespace as one team, read before you fix anything, then scan every namespace and keep a before-and-after record.

| | |
|---|---|
| Time | about 30 minutes for the first namespace; the scan takes about a second per namespace |
| You need | the plugin ([setup](setup.md)), and a kubeconfig for your cluster (Kubernetes 1.28 or newer) |
| Changes | nothing in the cluster. The tool only asks access questions and sends dry runs. Fixes are yours to make, and step 4 says how to make them safely. |
| Tested | the scan in step 5, on kind with four namespaces, 2026-11-05. Managed clouds (EKS, GKE, AKS) were not part of this test. |

**With an AI agent**, give it this prompt. It stops before every change:

```text
Help me run kubectl audit-tenant on my real cluster, following:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/real-cluster.md
Show me the current kubectl context and ask me to confirm it before you run anything.
Run only kubectl audit-tenant, kubectl auth whoami, kubectl auth can-i, and kubectl get commands.
Never apply, label, patch or delete anything in this cluster: write the fix commands down for me instead.
Do not star the repository, open issues or post anything for me. At the end, give me the report link
from the playbook's last section.
```

## 1. Before you run it

Read [Safety](../README.md#safety) in the README. In short:

- **It changes nothing.** It sends access reviews (SelfSubjectAccessReview, the API call that asks "may this identity do X?"), one SelfSubjectReview, and pod and Service creates with `dryRun=All`, which the API server checks and then throws away. Its HTTP transport refuses every other request before it leaves the process. It never reads a Secret.
- **It talks only to your API server.** No telemetry, no update check.
- **Others will see it.** Your API audit log records every request, with the user agent `audit-tenant/0.1.2`. Admission webhooks and policy engines see the dry-run pods, including the dangerous ones that they refuse. If your security team alerts on refused requests, tell them before you run it. To check the read-only claim yourself first, do part 6 of [security-review.md](security-review.md) on kind.
- **Check the binary.** The release has a `checksums.txt`; [setup.md](setup.md) shows how to check it.

## 2. Who runs it

Pick one:

- **Self mode**: a member of the team runs it with their own login. It needs no extra rights.
- **As a platform engineer, with `--as`**: you need `impersonate` on the team's identity, and nothing else. [docs/runner-role.md](../docs/runner-role.md) has the roles, limited to named identities.

Do not run it as a cluster admin without `--as`: the results then describe the admin's rights, not the team's.

## 3. One namespace, one team

Find the exact names the cluster gives a team member. Run this with their login (or ask them to):

```bash
kubectl auth whoami
```

On EKS, GKE and AKS it shows the names that the cluster maps the cloud identity to. Use the username with `--as`, and each group that gives the team its access with `--as-group`. For a service account, give no group.

Check that you are on the cluster you mean, then run the audit and keep the page:

```bash
kubectl config current-context
kubectl audit-tenant -n <namespace> --as <user> --as-group <group> -o wide --plain > "before-<namespace>-$(date +%F).txt"
cat "before-<namespace>-$(date +%F).txt"
```

Read the first line: it must name the identity you meant. If every check is SKIP, the run exits 2 with `nothing was proven`: the namespace, the user or the group is misspelled, or the identity has no role there. The [README troubleshooting](../README.md#troubleshooting) lists every message.

## 4. Read before you fix

The `fix` lines assume a new, empty namespace. On a namespace that runs apps, a fence can stop the next rollout. Talk to the team that owns it, and work in this order (the [platform-team playbook](platform-team.md), step 6, shows each step on kind):

1. **Pod Security**: ask what a level would refuse before you set it: `kubectl label --dry-run=server --overwrite namespace <namespace> pod-security.kubernetes.io/enforce=restricted`. If it warns about running pods, enforce `baseline` and only `warn` for `restricted` until the apps are ready.
2. **LimitRange before ResourceQuota**: with a compute quota and no defaults, the API server refuses every new pod that sets no CPU or memory.
3. **Allow rules before default-deny NetworkPolicies**: a default deny blocks all traffic that no other policy allows, DNS included.
4. **RBAC FAILs** name the binding that grants the access. Ask who made it and why before you remove it; then remove or narrow it.
5. Re-run the audit after each change. AT-10 tells you whether the next rollout still fits the quota.

## 5. Every namespace: a before-and-after record

`scan.sh` audits a list of namespaces, keeps one page per namespace, and counts how many namespaces fail each check. Change its `--as` line to match how your teams get access:

- each namespace has a deploy service account: `--as "system:serviceaccount:$ns:deployer"` (as written);
- each namespace has a group: `--as audit-probe --as-group "$ns-devs"` (any user name works when the group carries the access).

```bash
cat > scan.sh <<'EOF'
#!/usr/bin/env bash
# scan.sh FOLDER NAMESPACE...: audit each namespace as its team, keep one page per namespace, print a summary.
# Change the --as line below to match how your teams get access.
set -u
out=$1; shift
mkdir -p "$out"
for ns in "$@"; do
  kubectl audit-tenant -n "$ns" --as "system:serviceaccount:$ns:deployer" -o wide --plain > "$out/$ns.txt" </dev/null
done
for ns in "$@"; do printf '%-24s %s\n' "$ns" "$(grep -E '^[0-9]+ passed' "$out/$ns.txt")"; done
echo "namespaces that fail each check:"
cat "$out"/*.txt | grep -E '^FAIL ' | awk '{print $2}' | sort | uniq -c | sort -rn
EOF
bash scan.sh "audit-$(date +%F)" team-a team-b team-c team-d
```

To scan every namespace with a label, pass the list: `bash scan.sh "audit-$(date +%F)" $(kubectl get namespaces -l <your label> -o jsonpath='{.items[*].metadata.name}')`.

This is the output from the test, on kind, with three of four namespaces broken on purpose:

```text
team-a                   14 passed | 0 failed | 0 skipped | 0 errors | exit 0
team-b                   13 passed | 1 failed (AT-08) | 0 skipped | 0 errors | exit 1
team-c                   7 passed | 7 failed (AT-04.1, AT-04.2, AT-04.3 and 4 more) | 0 skipped | 0 errors | exit 1
team-d                   13 passed | 1 failed (AT-08) | 0 skipped | 0 errors | exit 1
namespaces that fail each check:
   2 AT-08
   1 AT-09
   1 AT-07
   1 AT-04.5
...
```

After the fixes, the same command into a new folder gave `14 passed` for all four namespaces and an empty list. Keep both folders: together they show what the tool found and what you changed, with dates, the tool version and the server on every page.

## 6. Keep it true

- Gate new namespaces and run the audit every night: [ci-and-nightly.md](ci-and-nightly.md).
- Show the verdicts on your policy dashboard: [kyverno-dashboard.md](kyverno-dashboard.md).
- Run `scan.sh` again after a Kubernetes upgrade, a policy-engine change, or a move from one tool to another (for example from Pod Security labels to Kyverno, or between Capsule versions). The audit tests the result, not the configuration, so a change that drops a fence shows up.

## Tell us what changed

After your first real run, a [playbook report](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new?template=playbook-report.yml&playbook=real-cluster) takes a few minutes, and the playbook name is already filled in. Choose the kind of cluster you ran it on, and share only what you may share. The answers that help most for this playbook:

- What did the first run find, and which finding surprised you?
- What did you change because of it, and how many namespaces did it touch? Counts help other teams decide, in a form such as "N of M namespaces had no default-deny egress policy; after the fixes, none".
- What stopped you, if anything: approvals, rights, noise in the output?

If your team keeps running it, please also open an ["I use this" issue](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new?template=i-use-this.yml). Bugs and rough edges: [Tell us](README.md#tell-us).
