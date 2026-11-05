# Playbook: a pipeline gate and a nightly audit

For a DevOps or platform engineer who wants the proof to run by itself. Part 1 makes a gate: a script that builds a namespace from your template on a throwaway cluster and fails when a fence is missing, first on your machine and then in GitHub Actions on every pull request. Part 2 runs the audit every night inside the cluster, as a CronJob with only `impersonate` rights, and shows it catch a fence that someone removed.

| | |
|---|---|
| Time | about 20 minutes |
| You need | Docker, kind, kubectl and the plugin ([setup](setup.md)); for part 1 in CI, a GitHub repository of your own |
| Changes | one new kind cluster, `audit-ci`, a local image `audit-tenant-runner:0.1.2`, and files in the current folder; the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), 2026-11-05: the gate script under `bash -e`, and part 2 on kind. The workflow file in step 3 has not run on GitHub yet; it uses the same kind action as this repository's own CI. |

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/ci-and-nightly.md
Use only the kind cluster that the playbook creates (context kind-audit-ci). If the current context
is anything else before a kubectl command, stop and ask me. Ask me before you install a tool, and
before you push anything to GitHub. After each audit, compare its summary line with the playbook's
"Expect" line and tell me if they differ. Do not star the repository, open issues or post anything
for me. At the end, run the clean-up step and tell me, in a few lines, what each audit found.
```

## Part 1: the gate

### 1. The template and the gate script

The template is the set of objects every team's namespace gets (the [platform-team playbook](platform-team.md) explains each one). The gate makes a test namespace from it and proves it as the team.

```bash
cat > tenant.yaml <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: TENANT
  labels: {pod-security.kubernetes.io/enforce: restricted}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: deployer, namespace: TENANT}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: deployer-edit, namespace: TENANT}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: edit}
subjects:
- {kind: ServiceAccount, name: deployer, namespace: TENANT}
---
apiVersion: v1
kind: ResourceQuota
metadata: {name: compute, namespace: TENANT}
spec:
  hard: {requests.cpu: "2", requests.memory: 4Gi, limits.cpu: "4", limits.memory: 8Gi, pods: "20", services.nodeports: "0"}
---
apiVersion: v1
kind: LimitRange
metadata: {name: container-defaults, namespace: TENANT}
spec:
  limits:
  - {type: Container, default: {cpu: 500m, memory: 512Mi}, defaultRequest: {cpu: 100m, memory: 128Mi}}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-ingress, namespace: TENANT}
spec: {podSelector: {}, policyTypes: [Ingress]}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-egress, namespace: TENANT}
spec: {podSelector: {}, policyTypes: [Egress]}
EOF
cat > gate.sh <<'EOF'
#!/usr/bin/env bash
# gate.sh [NAMESPACE] [TEMPLATE]: make a namespace from the template and prove it as the team.
# Exit 0: every fence held. Exit 1: a fence is missing. Exit 2: the run could not decide.
set -euo pipefail
ns=${1:-gate-test}
template=${2:-tenant.yaml}
sed "s/TENANT/$ns/g" "$template" | kubectl apply -f - || exit 2 # the cluster refused the template: not a verdict
# a new RoleBinding can take a few seconds to take effect
for i in $(seq 30); do kubectl auth can-i list pods -n "$ns" --as "system:serviceaccount:$ns:deployer" >/dev/null && break; sleep 1; done
kubectl audit-tenant -n "$ns" --as "system:serviceaccount:$ns:deployer" -o json > audit.json || true # the record
kubectl audit-tenant -n "$ns" --as "system:serviceaccount:$ns:deployer" --fail-on-skip --plain     # the verdict
EOF
```

### 2. Run the gate on your machine

```bash
kind create cluster --name audit-ci
kubectl config current-context
bash gate.sh gate-test
```

Expect `kind-audit-ci`, then `14 passed | 0 failed | 0 skipped | 0 errors | exit 0`, and an `audit.json` file with the same results.

Now make the mistake the gate exists for. Someone edits the template and drops the NodePort limit:

```bash
sed 's/, services.nodeports: "0"//' tenant.yaml > tenant-bad.yaml
bash gate.sh gate-bad tenant-bad.yaml
echo "gate exit: $?"
```

Expect `13 passed | 1 failed (AT-05)` and `gate exit: 1`:

```text
FAIL  AT-05    NodePort services blocked           a NodePort Service was accepted in a dry run
```

### 3. The same gate in GitHub Actions

Put `tenant.yaml` and `gate.sh` in a repository of yours, and save this file as `.github/workflows/namespace-gate.yml`. Every pull request that changes the template then builds a kind cluster on GitHub's runner, applies the template, and fails when a fence is missing. No cluster of yours is involved.

```yaml
name: namespace-gate
on:
  pull_request:
    paths: [tenant.yaml, gate.sh, .github/workflows/namespace-gate.yml]
  workflow_dispatch:
permissions:
  contents: read
jobs:
  gate:
    runs-on: ubuntu-latest # Docker and kubectl come with the runner image
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5.1.0
      - uses: helm/kind-action@ef37e7f390d99f746eb8b610417061a60e82a6cc # v1.14.0
        with:
          version: v0.33.0
          cluster_name: gate
      - name: Install kubectl audit-tenant
        run: |
          f=kubectl-audit-tenant_0.1.2_linux_amd64.tar.gz
          curl -fsSLO "https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v0.1.2/$f"
          curl -fsSLO "https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v0.1.2/checksums.txt"
          grep " $f\$" checksums.txt | sha256sum -c -
          tar -xzf "$f" kubectl-audit_tenant
          sudo mv kubectl-audit_tenant /usr/local/bin/
      - run: bash gate.sh gate-test
      - if: always()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: audit
          path: audit.json
```

The job log shows the checklist, and the `audit` artifact keeps the JSON record ([schema](../docs/report.schema.json)). The same `gate.sh` works in GitLab CI, Jenkins or Tekton, on any runner with Docker, kind and kubectl.

To gate a real provisioning pipeline instead of a test cluster, run the audit right after it makes the namespace, against that cluster, as the new team's identity. The pipeline's identity then needs `impersonate` on that identity only ([docs/runner-role.md](../docs/runner-role.md)).

## Part 2: the nightly audit, in the cluster

### 4. Build the runner image

The image holds the release binary and a short loop over the tenants. There is no published image yet, so build one from the release, and check the checksum:

```bash
mkdir -p image
v=0.1.2
arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
f=kubectl-audit-tenant_${v}_linux_${arch}.tar.gz
curl -fsSL -o "image/$f" "https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v$v/$f"
curl -fsSL -o image/checksums.txt "https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v$v/checksums.txt"
(cd image && grep " $f\$" checksums.txt > sum.txt && { sha256sum -c sum.txt 2>/dev/null || shasum -a 256 -c sum.txt; } && tar -xzf "$f" kubectl-audit_tenant)
cat > image/audit-all.sh <<'EOF'
#!/bin/sh
# Audit each namespace in $TENANTS as its deployer service account; exit 1 if any audit is not clean.
rc=0
for ns in $TENANTS; do
  kubectl-audit_tenant -n "$ns" --as "system:serviceaccount:$ns:deployer" --fail-on-skip --plain || rc=1
done
exit $rc
EOF
chmod +x image/audit-all.sh
cat > image/Dockerfile <<'EOF'
FROM busybox:1.37
COPY kubectl-audit_tenant audit-all.sh /usr/local/bin/
USER 65532
ENTRYPOINT ["/usr/local/bin/audit-all.sh"]
EOF
docker build -t audit-tenant-runner:0.1.2 image
kind load docker-image audit-tenant-runner:0.1.2 --name audit-ci
```

The checksum line must print `OK`. The image uses the Linux binary for your machine's CPU, because kind runs Linux nodes on it. On a real cluster, push the image to your registry instead of `kind load`.

### 5. Two tenants, a runner, and the CronJob

```bash
for t in team-a team-b; do sed "s/TENANT/$t/g" tenant.yaml | kubectl apply -f -; done
cat > runner-access.yaml <<'EOF'
# The runner may impersonate this namespace's deployer service account, and nothing else.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: audit-runner-impersonate, namespace: TENANT}
rules:
- apiGroups: [""]
  resources: [serviceaccounts]
  verbs: [impersonate]
  resourceNames: [deployer]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: audit-runner-impersonate, namespace: TENANT}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: audit-runner-impersonate}
subjects:
- {kind: ServiceAccount, name: runner, namespace: platform-audit}
EOF
cat > nightly.yaml <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: platform-audit
  labels: {pod-security.kubernetes.io/enforce: restricted}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: runner, namespace: platform-audit}
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: audit-tenants, namespace: platform-audit}
spec:
  schedule: "0 3 * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      backoffLimit: 0
      template:
        spec:
          serviceAccountName: runner
          restartPolicy: Never
          securityContext: {runAsNonRoot: true, runAsUser: 65532, seccompProfile: {type: RuntimeDefault}}
          containers:
          - name: audit
            image: audit-tenant-runner:0.1.2
            imagePullPolicy: Never # kind: the image was loaded, not pulled
            env:
            - {name: TENANTS, value: "team-a team-b"}
            securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
EOF
kubectl apply -f nightly.yaml
for t in team-a team-b; do sed "s/TENANT/$t/g" runner-access.yaml | kubectl apply -f -; done
```

The runner may only impersonate each team's `deployer` service account. It cannot read anything on its own: every request is decided as the team. Add `runner-access.yaml` to your tenant template, so that each new namespace is audited from its first night.

### 6. Run it now

```bash
kubectl create job --from=cronjob/audit-tenants audit-now -n platform-audit
kubectl wait --for=condition=complete job/audit-now -n platform-audit --timeout=120s
kubectl logs job/audit-now -n platform-audit
```

Expect `condition met`, and in the log one checklist per team, each with `14 passed | 0 failed | 0 skipped | 0 errors | exit 0`.

### 7. Catch drift

Someone removes a fence by hand:

```bash
kubectl --context kind-audit-ci delete networkpolicy default-deny-egress -n team-b
kubectl create job --from=cronjob/audit-tenants audit-now-2 -n platform-audit
kubectl wait --for=condition=failed job/audit-now-2 -n platform-audit --timeout=120s
kubectl get job audit-now-2 -n platform-audit
kubectl logs job/audit-now-2 -n platform-audit | grep -E '^(namespace|FAIL|[0-9]+ passed)'
```

Expect the Job `Failed`, and in the log:

```text
namespace team-a | as system:serviceaccount:team-a:deployer (groups: ...) | cluster https://10.96.0.1:443
14 passed | 0 failed | 0 skipped | 0 errors | exit 0
namespace team-b | as system:serviceaccount:team-b:deployer (groups: ...) | cluster https://10.96.0.1:443
FAIL  AT-08    Default-deny NetworkPolicy          no default deny for egress
13 passed | 1 failed (AT-08) | 0 skipped | 0 errors | exit 1
```

A failed Job is an alert you may already have: for example `kube_job_status_failed` from kube-state-metrics, or your platform's Job notifications. The log says which team and which fence.

## 8. Clean up

```bash
kind delete cluster --name audit-ci
docker rmi audit-tenant-runner:0.1.2
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## Use it on your cluster

- Part 1 needs no cluster of yours: put the gate on the repository that holds your namespace template.
- For part 2, push the image to your registry, set `TENANTS` (or make one CronJob per team from your template), and give the runner the role in `runner-access.yaml` in each team's namespace. Run the CronJob in a namespace where only platform admins may create pods: whoever can start a pod there holds the runner's rights.
- For human tenants with groups, use the user-tenant role in [docs/runner-role.md](../docs/runner-role.md) and pass `--as` and `--as-group` in the loop.
- To show the verdicts on a dashboard instead of in a log, see [kyverno-dashboard.md](kyverno-dashboard.md).

Did it help, or did something not work? [Tell us](README.md#tell-us).
