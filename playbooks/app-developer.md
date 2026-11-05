# Playbook: check your own namespace

For an application developer who has a kubeconfig for one namespace and nothing more. You check, with your own login and no flags, whether your namespace is fenced, and whether your next rollout fits the quota. The playbook plays the platform admin first, to make the namespace and your login.

| | |
|---|---|
| Time | about 10 minutes |
| You need | Docker, kind, kubectl and the plugin ([setup](setup.md)) |
| Changes | one new kind cluster, `audit-dev`, and two files in the current folder (`dev.kubeconfig`, `api.yaml`); the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), 2026-11-05, in zsh |

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/app-developer.md
Use only the kind cluster that the playbook creates: context kind-audit-dev in my kubeconfig, and
context dev in the dev.kubeconfig file that the playbook writes. If a kubectl command would use any
other context, stop and ask me.
Ask me before you install a tool. After each audit, compare its summary line with the playbook's
"Expect" line and tell me if they differ. Do not star the repository, open issues or post anything
for me. At the end, run the clean-up step and tell me, in a few lines, what each audit found.
```

## 1. The platform admin makes your namespace

```bash
kind create cluster --name audit-dev
kubectl config current-context
```

Expect `kind-audit-dev`. Now the admin's part: a namespace `team-a` with the usual fences, and a service account `dev` that stands in for you.

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
  labels: {pod-security.kubernetes.io/enforce: restricted}
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: dev, namespace: team-a}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: dev-edit, namespace: team-a}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: edit}
subjects:
- {kind: ServiceAccount, name: dev, namespace: team-a}
---
apiVersion: v1
kind: ResourceQuota
metadata: {name: compute, namespace: team-a}
spec:
  hard: {requests.cpu: "2", requests.memory: 2Gi, limits.cpu: "4", limits.memory: 4Gi, pods: "10", services.nodeports: "0"}
---
apiVersion: v1
kind: LimitRange
metadata: {name: container-defaults, namespace: team-a}
spec:
  limits:
  - {type: Container, default: {cpu: 500m, memory: 256Mi}, defaultRequest: {cpu: 100m, memory: 128Mi}}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-ingress, namespace: team-a}
spec: {podSelector: {}, policyTypes: [Ingress]}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-egress, namespace: team-a}
spec: {podSelector: {}, policyTypes: [Egress]}
EOF
```

## 2. Your own kubeconfig

On a real cluster, your company gives you this file, usually with a single sign-on login. Here, the admin makes one with a token for `dev`, and removes the admin's own credentials from it:

```bash
kind get kubeconfig --name audit-dev > dev.kubeconfig
kubectl --kubeconfig dev.kubeconfig config set-credentials dev --token="$(kubectl create token dev -n team-a --duration=2h)"
kubectl --kubeconfig dev.kubeconfig config set-context dev --cluster=kind-audit-dev --user=dev --namespace=team-a
kubectl --kubeconfig dev.kubeconfig config use-context dev
kubectl --kubeconfig dev.kubeconfig config delete-context kind-audit-dev
kubectl --kubeconfig dev.kubeconfig config delete-user kind-audit-dev
KUBECONFIG=dev.kubeconfig kubectl auth whoami
```

Expect `Username system:serviceaccount:team-a:dev`. From now on, every command that starts with `KUBECONFIG=dev.kubeconfig` runs as you, the developer.

## 3. Check your namespace, with no flags

```bash
KUBECONFIG=dev.kubeconfig kubectl audit-tenant
```

Expect `14 passed · 0 failed · 0 skipped · 0 errors · exit 0`. The header says `(self)`: the tool ran as your own login, and took the namespace from your context:

```text
namespace team-a · as system:serviceaccount:team-a:dev (self) (groups: system:serviceaccounts, system:serviceaccounts:team-a, system:authenticated) · cluster dev
```

You needed no extra rights. The checks ask the cluster "may I?" questions and send dry runs, which every user may do.

## 4. Will your next rollout fit?

Deploy an app with 8 replicas. Each pod gets the LimitRange's default limit of 500m CPU, so 8 pods use the whole 4 CPU of the quota:

```bash
cat > api.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  replicas: 8
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      securityContext: {runAsNonRoot: true, runAsUser: 65532, seccompProfile: {type: RuntimeDefault}}
      containers:
      - name: api
        image: registry.k8s.io/pause:3.10
        securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
EOF
KUBECONFIG=dev.kubeconfig kubectl apply -f api.yaml
KUBECONFIG=dev.kubeconfig kubectl rollout status deployment/api --timeout=90s
KUBECONFIG=dev.kubeconfig kubectl audit-tenant
```

All 8 pods run. The audit still fails. Expect `8 passed · 1 failed (AT-10) · 5 skipped`, exit 1:

```text
FAIL  AT-10    Quota covers rollout peak           estimate: compute limits.cpu short by 1: peak needs 5, hard is 4 (deployments: api 8->10); used in compute: requests.cp…
      fix      raise the quota's hard values by the shortfall, or lower replicas, HPA maxReplicas or maxSurge; the estimate assumes every workload rolls out at once
SKIP  AT-04.1  Privileged container blocked        the compliant control pod was rejected, so the probes prove nothing: pods "audit-tenant-probe-drfft" is forbidden: exce…
      fix      a ResourceQuota stopped the probe (quota full, or a resource it requires is missing from the probe); free quota, then re-run
```

Two things to read here:

- **AT-10**: a rolling update starts new pods before it stops old ones. With the default `maxSurge` of 25%, the next rollout needs 10 pods, or 5 CPU, and the quota allows 4. Today everything runs; the next deploy will hit `exceeded quota` errors. `-o wide` prints the whole estimate.
- **AT-04 SKIP**: the quota is full, so even a harmless test pod does not fit, and the dangerous pod probes would prove nothing. The tool says so, and does not guess. AT-05 still passes: its probe is a Service, which needs no CPU.

Fix it yourself: scale to 6 replicas. You could also ask the platform team for more quota, or set a smaller `maxSurge`.

```bash
KUBECONFIG=dev.kubeconfig kubectl scale deployment/api --replicas=6
sleep 10
KUBECONFIG=dev.kubeconfig kubectl audit-tenant
```

Expect `14 passed`, exit 0. If you still see SKIPs, the two old pods are still stopping and still count against the quota: wait a few seconds and run it again.

## 5. What you may not do

Audit a namespace that is not yours:

```bash
KUBECONFIG=dev.kubeconfig kubectl audit-tenant -n default
```

Expect `0 passed · 0 failed · 14 skipped`, exit 2, and `error: nothing was proven: every check was SKIP`. You have no rights in `default`, so there is nothing to test, and the tool does not report a false PASS.

Act as another team:

```bash
KUBECONFIG=dev.kubeconfig kubectl audit-tenant --as system:serviceaccount:team-b:dev
```

Expect exit 2: `error: system:serviceaccount:team-a:dev may not impersonate system:serviceaccount:team-b:dev: ...`.

## 6. Clean up

```bash
kind delete cluster --name audit-dev
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## Use it on your cluster

- Run `kubectl audit-tenant` with your usual kubeconfig, in the namespace you deploy to. It needs no rights beyond your own, and it changes nothing ([Safety](../README.md#safety)).
- Before you raise `replicas` or the HPA's `maxReplicas`, run `kubectl audit-tenant --checks AT-10 -o wide`.
- Some FAILs only the platform team can fix, such as an RBAC grant outside your namespace. Send them the output: each FAIL has a `fix` line, and the last line names the tool version, the time and the server.
- If your namespace has no fences at all, show your platform team the [platform-team playbook](platform-team.md).

Did it help, or did something not work? [Tell us](README.md#tell-us).
