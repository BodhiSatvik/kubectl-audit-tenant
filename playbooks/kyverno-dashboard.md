# Playbook: verdicts next to Kyverno in Policy Reporter

For a team that enforces pod security with [Kyverno](https://kyverno.io/) and shows results in [Policy Reporter](https://kyverno.github.io/policy-reporter/). Kyverno's own reports say which objects broke a policy. This playbook adds a second view: what a tenant can actually do, tested as the tenant, on the same dashboard. You install Kyverno with its restricted Pod Security policies, check that its policies really refuse a tenant's dangerous pods, publish the verdicts as a PolicyReport, and watch the dashboard turn red when a policy is removed.

| | |
|---|---|
| Time | about 20 minutes |
| You need | Docker with at least 4 CPUs and 8 GB, kind, kubectl, Helm and the plugin ([setup](setup.md)); delete other kind clusters first, because Kyverno needs room ([if kind fails](setup.md#if-kind-create-cluster-fails)) |
| Changes | one new kind cluster, `audit-kyverno`, and two files in the current folder; the clean-up step deletes the cluster |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), Kyverno chart 3.9.2 (Kyverno v1.19.2), Policy Reporter chart 3.11.2, 2026-11-05 |

**With an AI agent**, give it this prompt:

```text
Follow this playbook step by step, in a new empty folder on this machine:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/kyverno-dashboard.md
Use only the kind cluster that the playbook creates (context kind-audit-kyverno). If the current
context is anything else before a kubectl or helm command, stop and ask me. Ask me before you install
a tool. After each audit, compare its summary line with the playbook's "Expect" line and tell me if
they differ. Do not star the repository, open issues or post anything for me. At the end, run the
clean-up step and tell me, in a few lines, what each audit found.
```

## 1. A cluster with Kyverno

```bash
kind create cluster --name audit-kyverno
kubectl config current-context
helm repo add kyverno https://kyverno.github.io/kyverno/
helm repo add policy-reporter https://kyverno.github.io/policy-reporter
helm repo update
helm install kyverno kyverno/kyverno --kube-context kind-audit-kyverno -n kyverno --create-namespace --version 3.9.2 --wait --timeout 6m
helm install kyverno-policies kyverno/kyverno-policies --kube-context kind-audit-kyverno -n kyverno --version 3.9.2 \
  --set podSecurityStandard=restricted --set validationFailureAction=Enforce --wait
```

Expect `kind-audit-kyverno` as the context, and each `helm install` to end with `STATUS: deployed`. The second chart installs Kyverno's version of the restricted Pod Security Standard, as `ValidatingPolicy` objects that deny. Both installs name the kind context with `--kube-context`: if the cluster was not created, they fail instead of installing on another cluster.

Kyverno's Pod Security policies do not cover NodePort Services. Add one policy for that, and wait until every policy is ready:

```bash
kubectl --context kind-audit-kyverno apply -f - <<'EOF'
apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: restrict-nodeport
spec:
  validationActions: [Deny]
  matchConstraints:
    resourceRules:
    - apiGroups: [""]
      apiVersions: [v1]
      operations: [CREATE, UPDATE]
      resources: [services]
  validations:
  - expression: "!has(object.spec.type) || object.spec.type != 'NodePort'"
    message: Services of type NodePort are not allowed.
EOF
kubectl wait --for=jsonpath='{.status.conditionStatus.ready}'=true validatingpolicies --all --timeout=120s
```

## 2. A team namespace, fenced by Kyverno

The namespace has a quota, defaults and default-deny network policies, but no Pod Security Admission label: here, Kyverno is the pod fence.

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Namespace
metadata: {name: team-a}
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
  hard: {requests.cpu: "2", requests.memory: 4Gi, limits.cpu: "4", limits.memory: 8Gi, pods: "20"}
---
apiVersion: v1
kind: LimitRange
metadata: {name: container-defaults, namespace: team-a}
spec:
  limits:
  - {type: Container, default: {cpu: 500m, memory: 512Mi}, defaultRequest: {cpu: 100m, memory: 128Mi}}
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

## 3. Does Kyverno really stop the tenant?

```bash
for i in $(seq 30); do kubectl auth can-i list pods -n team-a --as system:serviceaccount:team-a:dev >/dev/null && break; sleep 1; done
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:dev -o wide
```

Expect `13 passed · 1 failed (AT-09)`, exit 1. In the layer column, Kyverno's webhook decided every pod and Service probe, and each reason quotes the Kyverno policy that refused it:

```text
PASS  AT-04.1  MTB-PL1-BC-CPI-5  admission (webhook: vpol.validate.kyverno.svc-fail)  Privileged container blocked   admission webhook "vpol.validate.kyverno.svc-fail" denied the request: Policy ...
PASS  AT-05    MTB-PL1-BC-HI-1   admission (webhook: vpol.validate.kyverno.svc-fail)  NodePort services blocked      admission webhook "vpol.validate.kyverno.svc-fail" denied the request: Policy ...
FAIL  AT-09    -                 config                                               Pod Security Admission enforced  namespace team-a has no pod-security.kubernetes.io/enforce label; AT-04 shows ...
```

AT-04 proves that Kyverno blocks the dangerous pods. AT-09 asks for the built-in Pod Security Admission label as a second fence. If your team decided that Kyverno alone is the pod fence, skip AT-09 in the dashboard report (next step). If you want both fences, add the label `pod-security.kubernetes.io/enforce=restricted` to the namespace.

## 4. Publish the verdicts as a PolicyReport

`-o policyreport` prints a `wgpolicyk8s.io/v1alpha2` PolicyReport, the format that Kyverno's own reports use; the Kyverno chart installed its CRD. This script applies the report even when it holds a FAIL, so the dashboard never keeps an old clean report, and then exits with the audit's own code:

```bash
cat > publish.sh <<'EOF'
#!/usr/bin/env bash
# publish.sh: audit team-a and apply the result as a PolicyReport, even when it holds a FAIL.
code=0
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:dev -o policyreport --skip AT-09 > report.yaml || code=$?
if [ -s report.yaml ]; then kubectl apply --server-side -f report.yaml || exit 2; fi
exit $code
EOF
bash publish.sh
kubectl get policyreports -n team-a
```

Expect the warning `--skip ran 13 of 14 results; the others are skip rows in this report`, then `serverside-applied`, and one report with `PASS 13`, `FAIL 0` and `SKIP 1`: the skipped AT-09 stays in the report as a `skip` row. The [README](../README.md#applying-a-policyreport) explains each choice in the script, and who may change a report.

## 5. See it in Policy Reporter

```bash
helm install policy-reporter policy-reporter/policy-reporter --kube-context kind-audit-kyverno -n policy-reporter --create-namespace \
  --version 3.11.2 --set ui.enabled=true --wait --timeout 5m
kubectl port-forward service/policy-reporter-ui 8082:8080 -n policy-reporter
```

Open http://localhost:8082. The dashboard lists **Audit-Tenant** as a source next to **KyvernoValidatingPolicy**. Its page, "Audit-tenant: multi-tenancy", shows `team-a` (kind Namespace, under Cluster Resources) with 13 pass, 0 warn, 0 fail and 0 error. Open the row to see each check's reason, layer and `hint` (the fix line). Leave the port-forward running, and use a second terminal for the next step.

## 6. Watch it turn red

Someone deletes the NodePort policy:

```bash
kubectl --context kind-audit-kyverno delete validatingpolicy restrict-nodeport
bash publish.sh
echo "publish exit: $?"
kubectl get policyreports -n team-a
```

Expect `publish exit: 1`, and the report now `PASS 12`, `FAIL 1`, `SKIP 1`. Reload the dashboard page: `team-a` shows 12 pass and 1 fail. The AT-05 row says `a NodePort Service was accepted in a dry run`, with the fix. Kyverno's own reports did not change, because no object broke a policy: the fence itself was gone.

## 7. Clean up

Stop the port-forward with Ctrl-C, then:

```bash
kind delete cluster --name audit-kyverno
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## Use it on your cluster

- Run `publish.sh` for each team, as a CronJob ([ci-and-nightly.md](ci-and-nightly.md) builds one) or in your pipeline. Use `--as` with each team's identity: a report names the identity, its groups, the context and the server, and every subject that may read PolicyReports in that namespace sees them.
- A tenant bound to `admin` may edit PolicyReports in its own namespace, and so could turn its own FAIL into a PASS. For an audit record, also keep the `-o json` output outside the cluster.
- The layer column names whatever admission step refused a probe, so the audit also reads a Gatekeeper webhook or a ValidatingAdmissionPolicy as the fence. This playbook tested Kyverno only. Policy Reporter needs the PolicyReport CRD, which the Kyverno chart installs; without Kyverno, install the CRD from [wg-policy-prototypes](https://github.com/kubernetes-sigs/wg-policy-prototypes).

Did it help, or did something not work? [Tell us](README.md#tell-us).
