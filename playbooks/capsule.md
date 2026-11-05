# Playbook: check Capsule tenants as their owners

For a team that runs [Capsule](https://projectcapsule.dev/), the multi-tenancy operator, where tenant owners make their own namespaces. A Tenant object says which fences each namespace gets. This playbook checks, as the tenant owner, that the fences are really there and really hold: first on a bare Tenant, then after you add the fences to the Tenant, and between two tenants.

| | |
|---|---|
| Time | about 15 minutes |
| You need | Docker, kind, kubectl, Helm and the plugin ([setup](setup.md)) |
| Changes | one new kind cluster, `audit-capsule`, and two files in the current folder; the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), Capsule chart 0.15.2, 2026-11-05 |

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/capsule.md
Use only the kind cluster that the playbook creates (context kind-audit-capsule). If the current
context is anything else before a kubectl or helm command, stop and ask me. Ask me before you install
a tool. After each audit, compare its summary line with the playbook's "Expect" line and tell me if
they differ. Do not star the repository, open issues or post anything for me. At the end, run the
clean-up step and tell me, in a few lines, what each audit found.
```

## 1. A cluster with Capsule

```bash
kind create cluster --name audit-capsule
kubectl config current-context
helm repo add projectcapsule https://projectcapsule.github.io/charts
helm repo update
helm install capsule projectcapsule/capsule -n capsule-system --create-namespace --version 0.15.2 \
  --set certManager.generateCertificates=false --set tls.create=true --set tls.enableController=true \
  --wait --timeout 5m
```

Expect `kind-audit-capsule`, and `STATUS: deployed`. The three `--set` flags let Capsule make its own webhook certificate, so the cluster needs no cert-manager.

## 2. Two tenants, and one namespace each

Capsule treats members of the group `projectcapsule.dev` as tenant users. `alice` owns the tenant `solar`, and `bob` owns `lunar`. You act as each of them with `--as` and `--as-group`.

```bash
cat > tenants.yaml <<'EOF'
apiVersion: capsule.clastix.io/v1beta2
kind: Tenant
metadata:
  name: solar
spec:
  owners:
  - {kind: User, name: alice}
---
apiVersion: capsule.clastix.io/v1beta2
kind: Tenant
metadata:
  name: lunar
spec:
  owners:
  - {kind: User, name: bob}
EOF
kubectl apply -f tenants.yaml
sleep 3
kubectl create namespace solar-prod --as alice --as-group projectcapsule.dev
kubectl create namespace lunar-prod --as bob --as-group projectcapsule.dev
```

Expect `namespace/solar-prod created` and `namespace/lunar-prod created`. Capsule gave each owner the `admin` role in the new namespace.

## 3. Audit a bare tenant

```bash
for i in $(seq 30); do kubectl auth can-i list pods -n solar-prod --as alice --as-group projectcapsule.dev >/dev/null && break; sleep 1; done
kubectl audit-tenant -n solar-prod --as alice --as-group projectcapsule.dev
```

Expect `2 passed · 12 failed`, exit 1. Capsule keeps alice away from other tenants' objects, but a bare Tenant puts no fences inside her namespaces: privileged pods, node ports, no quota, no network policy.

## 4. Put the fences in the Tenant

Add the fences to both Tenants. Capsule copies them into every namespace of the tenant, now and later:

```bash
cat > fences.yaml <<'EOF'
spec:
  namespaceOptions:
    additionalMetadataList:
    - labels: {pod-security.kubernetes.io/enforce: restricted}  # AT-04, AT-09
  serviceOptions:
    allowedServices: {nodePort: false}                          # AT-05
  resourceQuotas:                                               # AT-06, AT-10
    scope: Namespace
    items:
    - hard: {requests.cpu: "2", requests.memory: 4Gi, limits.cpu: "4", limits.memory: 8Gi, pods: "20"}
  limitRanges:                                                  # AT-07
    items:
    - limits:
      - {type: Container, default: {cpu: 500m, memory: 512Mi}, defaultRequest: {cpu: 100m, memory: 128Mi}}
  networkPolicies:                                              # AT-08
    items:
    - {podSelector: {}, policyTypes: [Ingress]}
    - {podSelector: {}, policyTypes: [Egress]}
EOF
for t in solar lunar; do kubectl patch tenant "$t" --type merge --patch-file fences.yaml; done
sleep 5
kubectl audit-tenant -n solar-prod --as alice --as-group projectcapsule.dev -o wide
```

Capsule 0.15 prints warnings that these Tenant fields are deprecated, in favor of its newer `rules` and TenantReplications. They still work in 0.15.2.

Expect `13 passed · 1 failed (AT-01)`, exit 1. The layer column shows who decided each fence: Pod Security Admission for the pod probes, and Capsule's own webhook for NodePort Services:

```text
PASS  AT-05    MTB-PL1-BC-HI-1   admission (webhook: services.validating.projectcapsule.dev)  NodePort services blocked  admission webhook "services.validating.projectcapsule.dev" denied the request: N...
```

The FAIL that stays is by design:

```text
FAIL  AT-01    No cluster-scoped access            RBAC allows create namespaces; granted by ClusterRoleBinding "capsule-namespace-provisioner" of ClusterRole "capsule-na…
```

Capsule lets tenant users create namespaces: that is its main feature. RBAC allows it to the whole `projectcapsule.dev` group, and Capsule's webhook then decides which namespaces a user may make. AT-01 asks RBAC only, so it reports the grant. Read the reason: if the only binding it names is `capsule-namespace-provisioner`, it is the grant you expect. Any other binding is a real finding. v0.1.2 cannot accept this one grant and still test AT-01's other three actions (list nodes, create ClusterRoleBindings, create PersistentVolumes).

Capsule's webhook refuses a user who owns no tenant, though RBAC allows the request:

```bash
kubectl create namespace my-namespace --as carol --as-group projectcapsule.dev
```

Expect `denied the request: You do not have any Tenant assigned`.

## 5. Between tenants, and for new namespaces

Can one tenant's owner read the other's Secrets?

```bash
kubectl audit-tenant -n solar-prod --as alice --as-group projectcapsule.dev --checks AT-02 --other-namespace lunar-prod
kubectl audit-tenant -n lunar-prod --as bob --as-group projectcapsule.dev --checks AT-02 --other-namespace solar-prod
```

Expect a PASS for each: `no general get, list or watch on secrets in lunar-prod`, and the same for `solar-prod`. Inside one tenant, an owner may read the Secrets of all its namespaces; that is Capsule's design, so point `--other-namespace` at another tenant.

A namespace made now gets the fences at once:

```bash
kubectl create namespace solar-dev --as alice --as-group projectcapsule.dev
for i in $(seq 30); do kubectl auth can-i list pods -n solar-dev --as alice --as-group projectcapsule.dev >/dev/null && break; sleep 1; done
kubectl audit-tenant -n solar-dev --as alice --as-group projectcapsule.dev
```

Expect `13 passed · 1 failed (AT-01)` again.

## 6. Clean up

```bash
kind delete cluster --name audit-capsule
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## Use it on your cluster

- Audit one namespace of each tenant as its owner, with the owner's real user and groups (`kubectl auth whoami` with the owner's login prints them). Then check each pair of tenants with `--checks AT-02 --other-namespace`.
- When you move from the deprecated Tenant fields to Capsule's `rules` and TenantReplications, run the same audits before and after. The audit tests the result, not the configuration, so the verdicts must not change.
- To run it every night, see [ci-and-nightly.md](ci-and-nightly.md). The runner needs `impersonate` on each owner and on the group `projectcapsule.dev` only ([docs/runner-role.md](../docs/runner-role.md)).

Did it help, or did something not work? [Tell us](README.md#tell-us).
