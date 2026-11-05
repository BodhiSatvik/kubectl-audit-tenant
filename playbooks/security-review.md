# Playbook: a security review between teams

For a security engineer, an auditor, or a consultant who must answer "can team A reach team B?" with proof, and for anyone who must approve a new tool before it touches a real cluster. You build a cluster with two fenced teams, plant five RBAC mistakes of the kind found in real clusters, find them as each team, keep a dated evidence page, and then prove from the API server's own audit log that the tool changed nothing.

| | |
|---|---|
| Time | about 20 minutes |
| You need | Docker, kind, kubectl and the plugin ([setup](setup.md)) |
| Changes | one new kind cluster, `audit-security`, and some files in the current folder; the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), 2026-11-05, in zsh |

On macOS, run it in a folder under your home folder: Docker Desktop and Colima share only that folder with kind, and step 1 mounts a file from it.

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder under my home folder:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/security-review.md
Use only the kind cluster that the playbook creates (context kind-audit-security). If the current
context is anything else before a kubectl command, stop and ask me. Step 3 adds weak RBAC on purpose:
run it only with --context kind-audit-security, as written. Ask me before you install a tool.
After each audit, compare its summary line with the playbook's "Expect" line and tell me if they differ.
Do not star the repository, open issues or post anything for me. At the end, run the clean-up step
and tell me, in a few lines, what each audit found.
```

## 1. Make a cluster that keeps an audit log

The API server's audit log records every request, with the user who sent it. Step 6 reads it to check the tool's read-only claim.

```bash
cat > audit-policy.yaml <<'EOF'
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
- level: Metadata
EOF
cat > kind-audit.yaml <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraMounts:
  - {hostPath: $PWD/audit-policy.yaml, containerPath: /etc/kubernetes/audit-policy.yaml, readOnly: true}
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
      - {name: audit-policy-file, value: /etc/kubernetes/audit-policy.yaml}
      - {name: audit-log-path, value: /var/log/kubernetes/audit.log}
      extraVolumes:
      - {name: audit-policy, hostPath: /etc/kubernetes/audit-policy.yaml, mountPath: /etc/kubernetes/audit-policy.yaml, readOnly: true, pathType: File}
      - {name: audit-log, hostPath: /var/log/kubernetes, mountPath: /var/log/kubernetes, pathType: DirectoryOrCreate}
EOF
kind create cluster --name audit-security --config kind-audit.yaml
kubectl config current-context
```

Expect `kind-audit-security`. If `kind create cluster` fails, two causes are common: the folder is not shared with Docker (see the macOS note above), or other kind clusters on the machine used up the inotify limits ([setup.md](setup.md#if-kind-create-cluster-fails)).

## 2. Two fenced teams

The teams are people, as in a cluster that signs users in with OIDC: `alice` is in the group `team-a-devs`, and `bob` is in `team-b-devs`. Each group gets the built-in `edit` role in its own namespace, and each namespace gets the usual fences.

```bash
cat > fence.yaml <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: TENANT
  labels: {pod-security.kubernetes.io/enforce: restricted}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: devs-edit, namespace: TENANT}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: edit}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: TENANT-devs}
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
for t in team-a team-b; do sed "s/TENANT/$t/g" fence.yaml | kubectl apply -f -; done
for i in $(seq 30); do kubectl auth can-i list pods -n team-b --as bob --as-group team-b-devs >/dev/null && break; sleep 1; done
kubectl audit-tenant -n team-a --as alice --as-group team-a-devs
kubectl audit-tenant -n team-b --as bob --as-group team-b-devs
```

Expect `14 passed · 0 failed` for each team. This is the clean baseline.

You run as the cluster admin and impersonate each team with `--as` and `--as-group`, so every request is decided as that team. On a real cluster, `kubectl auth whoami`, run with a team member's own login, prints the exact user and groups to pass.

## 3. Plant five RBAC mistakes

Each one is a shortcut that teams take in real clusters. This step weakens the cluster on purpose, so the command names the kind context.

```bash
cat > leaks.yaml <<'EOF'
# Leak 1: "temporary" debug access for team-a to the Secrets in kube-system
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: debug-secret-reader, namespace: kube-system}
rules:
- {apiGroups: [""], resources: [secrets], verbs: [get, list, watch]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: team-a-debug, namespace: kube-system}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: debug-secret-reader}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: team-a-devs}
---
# Leak 2: team-b shared its Secrets with team-a "for the migration"
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: secret-reader, namespace: team-b}
rules:
- {apiGroups: [""], resources: [secrets], verbs: [get, list, watch]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: team-a-migration, namespace: team-b}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: secret-reader}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: team-a-devs}
---
# Leak 3: team-b's CI tool may impersonate any service account in the cluster
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: ci-impersonator}
rules:
- {apiGroups: [""], resources: [serviceaccounts], verbs: [impersonate]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: team-b-ci-impersonator}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: ci-impersonator}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: team-b-devs}
---
# Leak 4: team-b may create PersistentVolumes, which can point at a node's disk
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: pv-provisioner}
rules:
- {apiGroups: [""], resources: [persistentvolumes], verbs: [create]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: team-b-pv-provisioner}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: pv-provisioner}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: team-b-devs}
---
# Leak 5, a blind spot: team-b may read one named Secret in kube-system
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: registry-credentials-reader, namespace: kube-system}
rules:
- {apiGroups: [""], resources: [secrets], verbs: [get], resourceNames: [registry-credentials]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: team-b-registry, namespace: kube-system}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: registry-credentials-reader}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: Group, name: team-b-devs}
EOF
kubectl --context kind-audit-security apply -f leaks.yaml
```

## 4. Find them as each team

```bash
kubectl audit-tenant -n team-a --as alice --as-group team-a-devs
```

Expect `13 passed · 1 failed (AT-02)`, exit 1. The FAIL names the binding that grants the access:

```text
FAIL  AT-02    No cross-namespace secret access    RBAC allows get secrets in kube-system, list secrets in kube-system, watch secrets in kube-system; granted by RoleBindi…
      fix      remove or change RoleBinding "team-a-debug/kube-system" of Role "debug-secret-reader" to Group "team-a-devs" and any other binding that grants the same (the API server names only the first it matched); a tenant reads secrets only in its own namespace
```

```bash
kubectl audit-tenant -n team-b --as bob --as-group team-b-devs
```

Expect `12 passed · 2 failed (AT-01, AT-03)`, exit 1:

```text
FAIL  AT-01    No cluster-scoped access            RBAC allows create persistentvolumes; granted by ClusterRoleBinding "team-b-pv-provisioner" of ClusterRole "pv-provisio…
FAIL  AT-03    No privilege escalation             RBAC allows impersonate serviceaccounts; granted by ClusterRoleBinding "team-b-ci-impersonator" of ClusterRole "ci-impe…
```

Leak 5 does not show as a FAIL. AT-02 passes for team-b with the line `note not tested: grants limited to named secrets (resourceNames)`. An access review (SSAR) asks about a resource type, not about one named object, so grants by name are a known blind spot, and the output says so. Read the `note` lines: they list what each PASS did not test.

Leak 2 is between two tenants. AT-02 checks `kube-system` by default; `--other-namespace` points it at another namespace. Check every pair of teams:

```bash
while read -r user team; do
  for other in team-a team-b; do
    [ "$other" = "$team" ] && continue
    echo "== $team -> $other"
    kubectl audit-tenant -n "$team" --as "$user" --as-group "$team-devs" --checks AT-02 --other-namespace "$other" --plain </dev/null | grep -E '^(PASS|FAIL)'
  done
done <<'EOF'
alice team-a
bob team-b
EOF
```

Expect one FAIL, from team-a to team-b:

```text
== team-a -> team-b
FAIL  AT-02  No cross-namespace secret access  RBAC allows get secrets in team-b, list secrets in team-b, watch secrets in team-b; granted by RoleBinding "team-a-mi...
== team-b -> team-a
PASS  AT-02  No cross-namespace secret access  no general get, list or watch on secrets in team-a
```

The `grep` keeps only the verdict lines. The full output also has a warning: the tool cannot confirm that the other namespace exists, so a misspelled name passes. Check the names in the loop.

## 5. Keep a dated evidence page

`-o wide` adds the benchmark ID and the deciding layer to each line, and `--plain` keeps the page ASCII:

```bash
mkdir -p evidence
kubectl audit-tenant -n team-a --as alice --as-group team-a-devs -o wide --plain > "evidence/before-team-a-$(date +%F).txt"
kubectl audit-tenant -n team-b --as bob --as-group team-b-devs -o wide --plain > "evidence/before-team-b-$(date +%F).txt"
tail -3 evidence/before-team-a-*.txt
```

```text
13 passed | 1 failed (AT-02) | 0 skipped | 0 errors | exit 1
audit-tenant 0.1.2 | 2026-11-05T21:22:42Z | server https://127.0.0.1:63920 | Kubernetes v1.37.0
machine output: -o json, -o policyreport
```

The line under the summary names the tool version, the time (UTC), the server and the Kubernetes version, so the page is evidence on its own. For machine-readable records, use `-o json` (schema: [`docs/report.schema.json`](../docs/report.schema.json)). Keep the records outside the cluster.

## 6. Prove that the tool changed nothing

Copy the audit log out of the kind node, then look only at the requests whose user agent is the tool's:

```bash
docker exec audit-security-control-plane cat /var/log/kubernetes/audit.log > audit.log
grep '"userAgent":"audit-tenant/' audit.log | grep '"stage":"ResponseComplete"' | grep -o '"verb":"[a-z]*"' | sort | uniq -c
```

Expect only `create`, `get` and `list` (the counts depend on how many runs you did):

```text
 234 "verb":"create"
  18 "verb":"get"
  35 "verb":"list"
```

Every `create` must be an access review, an identity review, or a dry run that the API server checks and then throws away. This command prints any other write. Expect no output:

```bash
grep '"userAgent":"audit-tenant/' audit.log | grep -E '"verb":"(create|update|patch|delete|deletecollection)"' | grep -v 'dryRun=All' | grep -v -E 'selfsubject(access)?reviews'
```

The log also shows who ran the tool and whom it impersonated (`"user":{"username":"kubernetes-admin"...}` and `"impersonatedUser":{"username":"alice"...}`). On a real cluster, the same check works on your managed audit log (for example CloudWatch Logs on EKS, Cloud Audit Logs on GKE, or Azure Monitor on AKS): filter on the user agent `audit-tenant/0.1.2`. The [Safety](../README.md#safety) section lists every request the tool may send.

## 7. Fix, and prove the fix

```bash
kubectl --context kind-audit-security delete -f leaks.yaml
kubectl audit-tenant -n team-a --as alice --as-group team-a-devs -o wide --plain > "evidence/after-team-a-$(date +%F).txt"
kubectl audit-tenant -n team-b --as bob --as-group team-b-devs -o wide --plain > "evidence/after-team-b-$(date +%F).txt"
grep -h -E '^[0-9]+ passed' evidence/after-*.txt
```

Expect `14 passed` for each team. Run the loop from step 4 again: both directions now PASS. The `before` and `after` pages together show the finding and the fix.

## 8. Clean up

```bash
kind delete cluster --name audit-security
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## Use it on your cluster

- Start with [real-cluster.md](real-cluster.md): one namespace, as one team, read-only.
- Use the names that `kubectl auth whoami` prints for a real team member, and the groups your identity provider gives them.
- The person who runs it needs only `impersonate` on the team's identity ([docs/runner-role.md](../docs/runner-role.md)), or no extra rights at all in self mode.
- Repeat the same commands next quarter and compare the pages. For a review every night, see [ci-and-nightly.md](ci-and-nightly.md).
- A customer's security questionnaire often asks how you keep customers apart. A dated page per customer namespace, with each FAIL fixed, is a concrete answer.

Did it help, or did something not work? [Tell us](README.md#tell-us).
