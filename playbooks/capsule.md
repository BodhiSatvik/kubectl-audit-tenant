# Playbook: check Capsule tenants as their owners

For a team that runs [Capsule](https://projectcapsule.dev/), the multi-tenancy operator, where tenant owners make their own namespaces. A Tenant object says which fences each namespace gets. This playbook checks, as the tenant owner, that the fences are really there and really hold: first on a bare Tenant, then after you add the fences to the Tenant, and between two tenants.

| | |
|---|---|
| Time | about 15 minutes |
| You need | Docker, kind, kubectl, Helm and the plugin ([setup](setup.md)) |
| Changes | one new kind cluster, `audit-capsule`, and three files in the current folder; the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), Capsule chart 0.15.2, 2026-11-05 |

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/capsule.md
Use only the kind cluster that the playbook creates (context kind-audit-capsule). If the current
context is anything else before a kubectl or helm command, stop and ask me. Ask me before you install
a tool. After each audit, compare its summary line with the playbook's "Expect" line and tell me if
they differ. Do not star the repository, open issues or post anything for me. At the end, run the
clean-up step and tell me, in a few lines, what each audit found. Then give me the report link from the playbook's last section.
```

## 1. A cluster with Capsule

```bash
kind create cluster --name audit-capsule
kubectl config current-context
helm repo add projectcapsule https://projectcapsule.github.io/charts
helm repo update
helm install capsule projectcapsule/capsule --kube-context kind-audit-capsule -n capsule-system --create-namespace --version 0.15.2 \
  --set certManager.generateCertificates=false --set tls.create=true --set tls.enableController=true \
  --wait --timeout 5m
```

Expect `kind-audit-capsule`, and `STATUS: deployed`. The three `--set` flags let Capsule make its own webhook certificate, so the cluster needs no cert-manager. `--kube-context` makes the install fail, instead of going to another cluster, if the kind cluster was not created.

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

## 4. Put the fences in the Tenants

Add the fences to both Tenants. Capsule applies them to every namespace of a tenant, now and later. Capsule 0.15 sets fences with Tenant `rules`, and copies other objects into every tenant namespace with a `GlobalTenantResource`. The rules: a Pod Security label that Capsule manages on each namespace, a deny for NodePort Services, and a quota. The copied objects: a LimitRange, a default-deny NetworkPolicy, and a DNS allow rule.

```bash
cat > rules.yaml <<'EOF'
spec:
  rules:
  - enforce:                      # AT-04, AT-09
      action: allow
      metadata:
      - apiGroups: ["v1"]
        kinds: ["Namespace"]
        labels:
          pod-security.kubernetes.io/enforce:
            required: true
            managed: "restricted"
  - enforce:                      # AT-05
      action: deny
      services:
        types: [NodePort]
  - quota:                        # AT-06, AT-10
    - name: compute
      hard: {requests.cpu: "4", requests.memory: 8Gi, limits.cpu: "8", limits.memory: 16Gi, pods: "50"}
EOF
cat > tenant-fences.yaml <<'EOF'
apiVersion: capsule.clastix.io/v1beta2
kind: GlobalTenantResource
metadata: {name: tenant-fences}
spec:
  resyncPeriod: 60s
  resources:
  - rawItems:
    - apiVersion: v1              # AT-07
      kind: LimitRange
      metadata: {name: defaults}
      spec:
        limits:
        - {type: Container, default: {cpu: 500m, memory: 512Mi}, defaultRequest: {cpu: 100m, memory: 128Mi}}
    - apiVersion: networking.k8s.io/v1   # AT-08
      kind: NetworkPolicy
      metadata: {name: default-deny}
      spec: {podSelector: {}, policyTypes: [Ingress, Egress]}
    - apiVersion: networking.k8s.io/v1
      kind: NetworkPolicy
      metadata: {name: allow-dns}
      spec:
        podSelector: {}
        policyTypes: [Egress]
        egress:
        - to:
          - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: kube-system}}
          ports: [{protocol: UDP, port: 53}, {protocol: TCP, port: 53}]
EOF
for t in solar lunar; do kubectl patch tenant "$t" --type merge --patch-file rules.yaml; done
kubectl apply -f tenant-fences.yaml
for i in $(seq 60); do kubectl get limitrange defaults -n solar-prod >/dev/null 2>&1 && break; sleep 1; done
kubectl audit-tenant -n solar-prod --as alice --as-group projectcapsule.dev -o wide
```

The loop waits until Capsule has copied the LimitRange into the namespace.

Expect `13 passed · 1 failed (AT-01)`, exit 1. The layer column shows who decided each fence: Pod Security Admission for the pod probes, Capsule's own webhook for NodePort Services, and the ResourceQuota that Capsule wrote for the quota rule.

## 5. AT-01: the grant Capsule needs

The FAIL that stays is by design:

```text
FAIL  AT-01    No cluster-scoped access            RBAC allows create namespaces; granted by ClusterRoleBinding "capsule-namespace-provisioner" of ClusterRole "capsule-na…
```

Capsule lets tenant users create namespaces: that is its main feature. RBAC allows it to the whole `projectcapsule.dev` group, and Capsule's webhook then decides which namespaces a user may make. AT-01 asks RBAC only, so it reports the grant. Do not follow its `fix` line: without the binding, tenant owners can no longer create namespaces. Read the reason instead: if the only binding it names is `capsule-namespace-provisioner`, it is the grant you expect, and any other binding is a real finding.

AT-01 also asks about three other actions, but its reason names only the first grant it finds. Ask the three directly:

```bash
kubectl auth can-i list nodes --as alice --as-group projectcapsule.dev
kubectl auth can-i create clusterrolebindings --as alice --as-group projectcapsule.dev
kubectl auth can-i create persistentvolumes --as alice --as-group projectcapsule.dev
```

Expect `no` three times. kubectl may first warn that the resource is not namespace scoped; that warning is harmless.

Capsule's webhook refuses a user who owns no tenant, though RBAC allows the request:

```bash
kubectl create namespace my-namespace --as carol --as-group projectcapsule.dev
```

Expect `denied the request: You do not have any Tenant assigned`.

## 6. The Pod Security label holds

AT-09 notes that a tenant who may change their namespace's labels could remove the Pod Security label. Under the `managed` rule, Capsule puts it back. As alice, try to weaken it (the command names the kind context because it tries to weaken the cluster):

```bash
kubectl --context kind-audit-capsule label namespace solar-prod pod-security.kubernetes.io/enforce=privileged --overwrite --as alice --as-group projectcapsule.dev
kubectl get namespace solar-prod -L pod-security.kubernetes.io/enforce
```

Expect the label command to be accepted, and the label to stay `restricted`.

## 7. Between tenants, and for new namespaces

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

## 8. Clean up

```bash
kind delete cluster --name audit-capsule
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## Use it on your cluster

- Audit one namespace of each tenant as its owner, with the owner's real user and groups (`kubectl auth whoami` with the owner's login prints them). Then check each pair of tenants with `--checks AT-02 --other-namespace`.
- When you change how the fences are set (for example from the Tenant fields that Capsule 0.15 deprecates to `rules` and replications, or to a policy engine), run the same audits before and after. The audit tests the result, not the configuration, so the verdicts must not change.
- To run it every night, see [ci-and-nightly.md](ci-and-nightly.md). The runner needs `impersonate` on each owner and on the group `projectcapsule.dev` only ([docs/runner-role.md](../docs/runner-role.md)).

## Tell us how it went

A [playbook report](https://github.com/BodhiSatvik/kubectl-audit-tenant/issues/new?template=playbook-report.yml&playbook=capsule) takes a few minutes, and the playbook name is already filled in. A report about problems helps as much as one about success. The answers that help most for this playbook:

- How should the tool treat Capsule's namespace grant in AT-01?
- How do you set fences today: Tenant rules, replications, or a policy engine?
- The step that broke or was unclear, if any.
