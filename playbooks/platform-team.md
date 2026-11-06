# Playbook: a minimal platform, from zero

For a platform team that shares one cluster between teams. You build the smallest useful platform: a cluster, a namespace template with the fences every team gets, and a provisioning script that refuses to hand over a namespace that `kubectl audit-tenant` cannot prove. Then you fence a namespace that already runs an app, without breaking it.

| | |
|---|---|
| Time | about 15 minutes |
| You need | Docker, kind, kubectl and the plugin ([setup](setup.md)) |
| Changes | one new kind cluster, `audit-platform`, and two files in the current folder; the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), 2026-11-05, in zsh |

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/platform-team.md
Use only the kind cluster that the playbook creates (context kind-audit-platform). If the current
context is anything else before a kubectl command, stop and ask me. Ask me before you install a tool.
After each audit, compare its summary line with the playbook's "Expect" line and tell me if they differ.
Do not star the repository, open issues or post anything for me. At the end, run the clean-up step
and tell me, in a few lines, what each audit found. Then give me the report link from the playbook's last section.
```

## 1. Make the cluster

```bash
kind create cluster --name audit-platform
kubectl config current-context
```

Expect `kind-audit-platform`. Every later step runs against this context.

## 2. See the problem: a namespace made the usual way

Many teams get a namespace, a deploy identity and the built-in `edit` role, and nothing else:

```bash
kubectl create namespace legacy
kubectl create serviceaccount deployer -n legacy
kubectl create rolebinding deployer-edit -n legacy --clusterrole=edit --serviceaccount=legacy:deployer
kubectl create deployment web -n legacy --image=nginx:1.27 --replicas=2
```

Audit it as the team's identity. The loop waits until the new role takes effect, which can take a few seconds on a new cluster:

```bash
for i in $(seq 30); do kubectl auth can-i list pods -n legacy --as system:serviceaccount:legacy:deployer >/dev/null && break; sleep 1; done
kubectl audit-tenant -n legacy --as system:serviceaccount:legacy:deployer
```

Expect `3 passed · 11 failed`, exit 1. An excerpt:

```text
PASS  AT-01    No cluster-scoped access            RBAC denied all 4 actions
FAIL  AT-04.1  Privileged container blocked        a privileged container was accepted in a dry run
      fix      set the namespace label pod-security.kubernetes.io/enforce=baseline (or restricted), or add a policy that blocks privileged containers
FAIL  AT-05    NodePort services blocked           a NodePort Service was accepted in a dry run
FAIL  AT-06    ResourceQuota present and complete  no ResourceQuota in legacy
FAIL  AT-08    Default-deny NetworkPolicy          no NetworkPolicy in legacy
...
3 passed · 11 failed (AT-04.1, AT-04.2, AT-04.3 and 8 more) · 0 skipped · 0 errors · exit 1
```

RBAC keeps the team inside its namespace (AT-01 to AT-03 pass). But inside it, the team may run a privileged pod, mount the node's disk, open a port on every node, use the whole cluster's CPU, and reach every other namespace over the network. The tool proved each FAIL with a server-side dry run: the API server accepted the request and stored nothing.

## 3. Write the namespace template

This file is the platform: every team's namespace gets these objects. `TENANT` is replaced by the namespace name.

```bash
cat > tenant.yaml <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: TENANT
  labels:
    pod-security.kubernetes.io/enforce: restricted   # AT-04, AT-09
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: deployer, namespace: TENANT}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding                                    # AT-01 to AT-03: edit stays inside the namespace
metadata: {name: deployer-edit, namespace: TENANT}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: edit}
subjects:
- {kind: ServiceAccount, name: deployer, namespace: TENANT}
---
apiVersion: v1
kind: ResourceQuota                                  # AT-05, AT-06, AT-10
metadata: {name: compute, namespace: TENANT}
spec:
  hard:
    requests.cpu: "2"
    requests.memory: 4Gi
    limits.cpu: "4"
    limits.memory: 8Gi
    pods: "20"
    services.nodeports: "0"
---
apiVersion: v1
kind: LimitRange                                     # AT-07
metadata: {name: container-defaults, namespace: TENANT}
spec:
  limits:
  - type: Container
    default: {cpu: 500m, memory: 512Mi}
    defaultRequest: {cpu: 100m, memory: 128Mi}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy                                  # AT-08
metadata: {name: default-deny-ingress, namespace: TENANT}
spec:
  podSelector: {}
  policyTypes: [Ingress]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy                                  # AT-08
metadata: {name: default-deny-egress, namespace: TENANT}
spec:
  podSelector: {}
  policyTypes: [Egress]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: allow-dns, namespace: TENANT}
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
  - to:
    - namespaceSelector:
        matchLabels: {kubernetes.io/metadata.name: kube-system}
      podSelector:
        matchLabels: {k8s-app: kube-dns}
    ports:
    - {protocol: UDP, port: 53}
    - {protocol: TCP, port: 53}
EOF
```

## 4. Provision with a gate

The provisioning script applies the template, then proves the result as the team. A non-zero exit means the namespace is not ready to hand over.

```bash
cat > provision.sh <<'EOF'
#!/usr/bin/env bash
# provision.sh NAMESPACE: make a fenced namespace from tenant.yaml, then prove it as the team.
set -euo pipefail
ns=$1
sed "s/TENANT/$ns/g" tenant.yaml | kubectl apply -f - || exit 2 # the cluster refused the template: not a verdict
# a new RoleBinding can take a few seconds to take effect
for i in $(seq 30); do kubectl auth can-i list pods -n "$ns" --as "system:serviceaccount:$ns:deployer" >/dev/null && break; sleep 1; done
kubectl audit-tenant -n "$ns" --as "system:serviceaccount:$ns:deployer" --fail-on-skip
EOF
chmod +x provision.sh
./provision.sh team-a
```

Expect `14 passed · 0 failed · 0 skipped · 0 errors · exit 0`. Each PASS names the layer that decided it, for example:

```text
PASS  AT-04.1  Privileged container blocked        violates PodSecurity "restricted:latest": privileged (container "probe" must not set securityContext.privileged=true), …
PASS  AT-05    NodePort services blocked           exceeded quota: compute, requested: services.nodeports=1, used: services.nodeports=0, limited: services.nodeports=0
PASS  AT-08    Default-deny NetworkPolicy          default deny for ingress by default-deny-ingress and egress by default-deny-egress
```

`--fail-on-skip` makes a check that could not run fail the script too, so a typo in the namespace or the identity never passes.

## 5. Catch a bad change

A teammate needs a debug pod and sets the namespace to `privileged`. This weakens the namespace on purpose, so the command names the kind context:

```bash
kubectl --context kind-audit-platform label namespace team-a pod-security.kubernetes.io/enforce=privileged --overwrite
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:deployer
```

Expect `8 passed · 6 failed`, exit 1:

```text
FAIL  AT-04.1  Privileged container blocked        a privileged container was accepted in a dry run
...
FAIL  AT-09    Pod Security Admission enforced     pod-security.kubernetes.io/enforce=privileged on namespace team-a; only baseline or restricted blocks dangerous pods
8 passed · 6 failed (AT-04.1, AT-04.2, AT-04.3 and 3 more) · 0 skipped · 0 errors · exit 1
```

Apply the template again, and the gate passes:

```bash
./provision.sh team-a
```

Expect `14 passed`, exit 0. To catch this kind of drift without waiting for the next change, run the audit every night ([ci-and-nightly.md](ci-and-nightly.md)).

## 6. Fence a namespace that already runs an app

The fix lines assume a new namespace. On a namespace with running pods, a fence can stop the next rollout. Do it in this order.

First, ask the API server what the strictest level would refuse, without changing anything:

```bash
kubectl label --dry-run=server --overwrite namespace legacy pod-security.kubernetes.io/enforce=restricted
```

```text
Warning: existing pods in namespace "legacy" violate the new PodSecurity enforce level "restricted:latest"
Warning: web-69c6f74b8b-jkk64 (and 1 other pod): allowPrivilegeEscalation != false, unrestricted capabilities, runAsNonRoot != true, seccompProfile
namespace/legacy labeled (server dry run)
```

The app is not ready for `restricted`. Try `baseline`:

```bash
kubectl label --dry-run=server --overwrite namespace legacy pod-security.kubernetes.io/enforce=baseline
```

Expect only `namespace/legacy labeled (server dry run)`, with no warning. So enforce `baseline` now, and only warn for `restricted`. Add the LimitRange before the quota: with a compute quota and no defaults, the API server refuses every new pod that sets no CPU or memory.

```bash
kubectl label --overwrite namespace legacy pod-security.kubernetes.io/enforce=baseline pod-security.kubernetes.io/warn=restricted
kubectl apply -n legacy -f - <<'EOF'
apiVersion: v1
kind: LimitRange
metadata: {name: container-defaults}
spec:
  limits:
  - type: Container
    default: {cpu: 500m, memory: 512Mi}
    defaultRequest: {cpu: 100m, memory: 128Mi}
---
apiVersion: v1
kind: ResourceQuota
metadata: {name: compute}
spec:
  hard:
    requests.cpu: "2"
    requests.memory: 4Gi
    limits.cpu: "4"
    limits.memory: 8Gi
    pods: "20"
    services.nodeports: "0"
EOF
kubectl rollout restart deployment/web -n legacy
kubectl rollout status deployment/web -n legacy --timeout=120s
```

The rollout finishes; `kubectl` warns that the pods would not meet `restricted`. Audit again:

```bash
kubectl audit-tenant -n legacy --as system:serviceaccount:legacy:deployer
```

Expect `12 passed · 2 failed (AT-04.5, AT-08)`, exit 1. AT-10 confirms that the next rollout fits the new quota:

```text
PASS  AT-10    Quota covers rollout peak           estimate: peak fits compute: requests.cpu 300m of 2, ...
```

The two FAILs left need changes to the app, so plan them with its owners:

- **AT-04.5** needs `restricted`. Set `allowPrivilegeEscalation: false`, `runAsNonRoot: true`, `capabilities: {drop: [ALL]}` and `seccompProfile: {type: RuntimeDefault}` in the app's pod spec (for nginx, use an image that runs as non-root, such as `nginxinc/nginx-unprivileged`). Then repeat the dry run, and enforce `restricted` when it prints no warning.
- **AT-08** needs default-deny policies. They block all traffic that no other policy allows, so first add the allow rules the app needs (its ingress, DNS, its database), then the two default-deny policies.

## 7. Clean up

```bash
kind delete cluster --name audit-platform
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`. Keep `tenant.yaml` and `provision.sh`: they are the start of your own template.

## Use it on your platform

- Keep the template in Git, in whatever makes your namespaces: a Helm chart, a Kustomize base, Terraform, Crossplane, Backstage, or a namespace operator such as [Capsule](capsule.md).
- Run the audit as the last step of provisioning, as `provision.sh` does, and every night against every namespace: [ci-and-nightly.md](ci-and-nightly.md).
- For your existing namespaces, start with [real-cluster.md](real-cluster.md): one namespace, read-only, before any fix.
- If your teams deploy only through Argo CD or Flux and may not create pods, AT-04 shows SKIP: the probes need an identity that may create pods. See [What a PASS does not prove](../README.md#what-a-pass-does-not-prove).

## Tell us how it went

A [playbook report](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new?template=playbook-report.yml&playbook=platform-team) takes a few minutes, and the playbook name is already filled in. A report about problems helps as much as one about success. The answers that help most for this playbook:

- Would a gate like `provision.sh` fit how your team makes namespaces (Helm, Terraform, a portal, GitOps)?
- On a namespace of yours that already runs apps, which fence would be the hardest to add, and why?
- The step that broke or was unclear, if any.
