# kubectl audit-tenant

Prove, from the tenant's side, that a Kubernetes namespace is fenced.

> Status: pre-release. v0.1.0 is not tagged yet, so the install commands below work from v0.1.0 on.

## Why

A platform team that shares one cluster between teams must show that team A cannot reach team B, and today the proof usually comes from the admin's side: tools that read roles and policies and infer what a tenant could do. `kubectl audit-tenant` asks the cluster directly, as the tenant, with permission checks and server-side dry runs, and prints one verdict per fence with the layer that decided it, a benchmark ID and a fix. It never changes the cluster.

## Quick start (kind, about five minutes)

You need Docker, [kind](https://kind.sigs.k8s.io/) and kubectl. Install the plugin one of three ways:

```bash
# 1. krew, once the plugin is in the krew index
kubectl krew install audit-tenant

# 2. krew, from the release (works from the first release on)
kubectl krew install --manifest-url https://github.com/BodhiSatvik/kubectl-audit-tenant/releases/download/v0.1.0/audit-tenant.yaml

# 3. Go 1.26 or newer; kubectl finds kubectl-audit_tenant on your PATH
go install github.com/BodhiSatvik/kubectl-audit-tenant/cmd/kubectl-audit_tenant@v0.1.0
```

Make a cluster with one tenant namespace and run the audit as the tenant's service account:

```bash
kind create cluster --name audit-demo
kubectl create namespace team-a
kubectl create serviceaccount dev -n team-a
kubectl create rolebinding dev-edit -n team-a --clusterrole=edit --serviceaccount=team-a:dev
# a new cluster fills the built-in edit role a few seconds after it starts
until kubectl auth can-i list pods -n team-a --as system:serviceaccount:team-a:dev >/dev/null; do sleep 1; done
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:dev
```

The namespace has no fences yet, so most checks fail. An excerpt of a real run on kind v1.37.0:

```text
namespace team-a · as system:serviceaccount:team-a:dev (groups: system:serviceaccounts, system:serviceaccounts:team-a, system:authenticated) · cluster kind-audit-demo

PASS  AT-01    No cluster-scoped access            RBAC denied all 4 actions
...
FAIL  AT-04.1  Privileged container blocked        a privileged container was accepted in a dry run
      fix      set the namespace label pod-security.kubernetes.io/enforce=baseline (or restricted), or add a policy that blocks privileged containers
...
FAIL  AT-09    Pod Security Admission enforced     namespace team-a has no pod-security.kubernetes.io/enforce label
      fix      kubectl label --overwrite namespace team-a pod-security.kubernetes.io/enforce=restricted (baseline also passes, but does not block AT-04.5)
...
3 passed · 11 failed (AT-04.1, AT-04.2, AT-04.3 and 8 more) · 0 skipped · 0 errors · exit 1
```

Add one fence and run again:

```bash
kubectl label namespace team-a pod-security.kubernetes.io/enforce=restricted
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:dev
```

AT-04.1 to AT-04.5 and AT-09 now pass, and each reason quotes the Pod Security Admission message that refused the probe. The five FAILs left (NodePort, quota, LimitRange, NetworkPolicy, rollout headroom) each print a `fix` line. When you are done, run `kind delete cluster --name audit-demo`.

## Who runs it, and how

| Who | Command | Gets |
|---|---|---|
| Platform engineer, when a namespace is created | `kubectl audit-tenant -n team-a --as team-a-admin -o json --fail-on-skip` in the provisioning pipeline | A failed step when the new namespace is not fenced |
| Security or an auditor | `kubectl audit-tenant -n team-a --as team-a-admin` | One page of verdicts with benchmark IDs, repeatable next quarter |
| A tenant team with kubectl | `kubectl audit-tenant -n team-a` with its own kubeconfig | "Is my namespace governed?" and "why was my pod rejected?" |
| A policy dashboard owner | `-o policyreport`, applied with kubectl | The verdicts next to Kyverno's results in Policy Reporter |

Without `--as` the tool runs as the kubeconfig's own identity ("self mode"). With `--as` and `--as-group` it impersonates the tenant: you need `impersonate` rights, and you should pass the tenant's groups from your identity provider, because the API server does not add them for you.

## The checks

Ten checks give fourteen results (AT-04 has five). Behavioral checks ask the cluster: a SelfSubjectAccessReview (SSAR, the API call that asks "may this identity do X?" and changes nothing) or a create with `dryRun=All` (the server runs every gate and stores nothing). Configuration checks read objects in the namespace. `kubectl audit-tenant list-checks` prints the same list.

| ID | PASS when | Method | MTB ID | Severity |
|---|---|---|---|---|
| AT-01 | The tenant may not list nodes or create namespaces, ClusterRoleBindings or PersistentVolumes | SSAR | MTB-PL1-CC-CPI-1 | high |
| AT-02 | The tenant may not get, list or watch Secrets in `--other-namespace` (default `kube-system`) | SSAR | MTB-PL1-CC-TI-2 | high |
| AT-03 | The tenant may not escalate or bind ClusterRoles, impersonate users, groups or service accounts, or do `*` on `*` cluster-wide | SSAR | MTB-PL2-CC-TI-1 | high |
| AT-04.1 | A privileged container is refused | dry-run pod | MTB-PL1-BC-CPI-5 | high |
| AT-04.2 | A `hostPath` volume is refused | dry-run pod | MTB-PL1-BC-HI-1 | high |
| AT-04.3 | `hostPID` is refused | dry-run pod | MTB-PL1-BC-HI-4 | high |
| AT-04.4 | `hostNetwork` is refused | dry-run pod | MTB-PL1-BC-HI-3 | high |
| AT-04.5 | `allowPrivilegeEscalation: true` is refused | dry-run pod | MTB-PL1-BC-CPI-6 | high |
| AT-05 | A `type: NodePort` Service is refused | dry-run Service | MTB-PL1-BC-HI-1 | medium |
| AT-06 | The unscoped ResourceQuotas set `requests.cpu`, `requests.memory`, `limits.cpu`, `limits.memory` and `pods` | read | MTB-PL1-CC-FNS-1 | medium |
| AT-07 | The LimitRanges set `default` and `defaultRequest` for cpu and memory | read | - | low |
| AT-08 | A default-deny NetworkPolicy exists for ingress and for egress | read | - | medium |
| AT-09 | The namespace label `pod-security.kubernetes.io/enforce` is `baseline` or `restricted` | read | - | high |
| AT-10 | The rollout peak of the Deployments and StatefulSets (replicas, or the HPA's `maxReplicas`, plus `maxSurge`) fits every unscoped ResourceQuota | read and compute | - | low |

AT-04.5 needs `restricted` or a policy: Pod Security Admission at `baseline` does not block privilege escalation. The MTB IDs are those of the archived [Kubernetes multi-tenancy benchmarks](https://github.com/kubernetes-retired/multi-tenancy/tree/master/benchmarks); upstream gives hostPath volumes and NodePort Services the same ID.

Before an AT-04 or AT-05 probe, the tool asks whether the tenant may create pods (or Services) at all. If RBAC denies that, the result is an RBAC PASS, but only after a control question (`list pods` in the namespace) proves the identity has some rights there; an identity with no rights at all gets SKIP, because an RBAC PASS would prove nothing. Each probe follows a compliant control probe, so a rejection by a quota or a LimitRange is never read as a security fence.

## Verdicts and exit codes

| Verdict | Means |
|---|---|
| PASS | The fence held. The reason names the layer that decided: RBAC, admission (PSA, webhook, policy or quota) or the API server. |
| FAIL | The cluster allowed it. The `fix` line says what to add. |
| SKIP | The tool could not test it, for example it may not read the object, or a webhook cannot take a dry run. The `fix` line says what to change. |
| ERROR | A request failed, the run timed out, or the tool hit a bug. |

| Exit code | When |
|---|---|
| 0 | No FAIL and no ERROR. SKIPs are allowed. |
| 1 | At least one FAIL; with `--fail-on-skip`, also any SKIP. |
| 2 | An ERROR and no FAIL, or the run could not start (bad flags, kubeconfig, credentials, impersonation, namespace not found). |

Every output format keeps the exit code.

## Output formats

- Checklist (default): one line per result, with a `fix` line under each FAIL, SKIP and ERROR and a `note` line that names what the check did not test.
- `-o wide`: adds the MTB ID and the deciding layer, and never cuts a reason. `--plain` gives ASCII only, with no color.
- `-o json`: one object per run. The schema is [`docs/report.schema.json`](docs/report.schema.json).
- `-o policyreport`: a `wgpolicyk8s.io/v1alpha2` PolicyReport for dashboards such as Policy Reporter. The tool only prints it; you apply it.

### Applying a PolicyReport

```bash
set -o pipefail
kubectl audit-tenant -n team-a --as system:serviceaccount:team-a:dev -o policyreport \
  | kubectl apply --server-side -f -
```

- Use a pipe. The report is applied even when it holds a FAIL, and with `pipefail` the step still exits 1. With `> r.yaml && kubectl apply -f r.yaml`, a FAIL stops the apply, and the dashboard keeps the last clean report.
- Use `--server-side`. Client-side apply copies the whole report into an annotation, which has a 256 KiB limit.
- If you pass `--context` to the audit, pass the same `--context` to `kubectl apply`. The report itself does not name the cluster.
- The report lives in the audited namespace. Any subject that may write PolicyReports there can change it. The Kyverno Helm chart (checked on v1.19.1) gives the built-in `admin` role create, update, patch and delete on PolicyReports, so a tenant bound to `admin` can turn its own FAIL into a PASS. For an audit, keep the `-o json` output outside the cluster.
- A report names the identity and its groups. A self-mode report from an admin shows the admin's username and groups to every subject that may read PolicyReports in that namespace. Use `--as` for reports you apply.
- One report exists per set of run inputs (mode, user, groups, `--other-namespace`, server URL), and a re-run with the same inputs replaces it. `-o policyreport` refuses `--checks` and `--skip`. A run that hits `--timeout` still prints a report, with ERROR for every check that did not finish.

## In a pipeline

```bash
kubectl audit-tenant -n "$NAMESPACE" --as "$TENANT_USER" --as-group "$TENANT_GROUP" \
  -o json --fail-on-skip > audit.json
```

The step fails (exit 1) on any FAIL, and with `--fail-on-skip` also on any SKIP, so a namespace the tool could not prove does not pass. `audit.json` keeps every reason and hint for the record.

## Safety

- **Read-only by construction.** The HTTP transport has an exact allowlist: `GET /version`, the target namespace, lists of quotas, LimitRanges, NetworkPolicies, Deployments, StatefulSets and HPAs in the target namespace, SSAR and SelfSubjectReview creates, and pod and Service creates in the target namespace only with `dryRun=All`. Everything else is refused before it leaves the process: every subresource (such as `pods/exec`), every Secret path, every other namespace, and every PUT, PATCH and DELETE. The tool never reads a Secret.
- **Every create answer is checked.** A dry-run answer must have a generated `audit-tenant-probe-` name and no `resourceVersion`. If a proxy drops `dryRun=All` and the server stores an object, the check reports ERROR with the object's name, and the run sends no further create.
- **What a run leaves in the API audit log.** SSARs, one SelfSubjectReview, and dry-run creates of pods and Services named `audit-tenant-probe-*`, labeled `app.kubernetes.io/managed-by=audit-tenant`, all under the tenant identity. With `--as`, the audit log also records the user who impersonated. Admission webhooks see the dry-run requests too.
- A full run sends at most 40 API requests in the end-to-end tests and finishes in well under a second on kind (see [Benchmarks](#benchmarks)).

## What a PASS does not prove

- **Grants limited to named objects.** SSARs carry no resource name, so a role that grants access to specific Secrets or roles by `resourceNames` is not seen.
- **That the network plugin enforces NetworkPolicy.** AT-08 reads the policy objects only.
- **Pods created by controllers.** If the tenant may not create pods but may create or scale Deployments, Jobs or other pod controllers, AT-04 shows SKIP: those pods bypass the tenant's own RBAC. A PASS also says nothing about pods that a controller's service account creates under a Pod Security Admission exemption that the tenant's own probes do not have.
- **The identity you meant.** The control question needs `list pods`. On a cluster that grants `list pods` to every authenticated user, a misspelled `--as` user passes it. The tool warns when it impersonates a user with no groups, but not for a misspelled service account or a user given with `--as-group`. Check that the header line names the identity you meant.
- **The future.** A PASS is true for the policies, roles and labels at the time of the run.
- Each PASS has a `note` line that names what that check did not test.

## Troubleshooting

Run again with `--debug` and paste the stderr into the issue. `--debug` prints one line per API request, the identity steps and the server's answers; it never prints request headers. Read it before you paste it.

**The run did not start (exit 2)**

- `API server unreachable`: check the server URL, the network, and `--context`.
- `unauthorized: the cluster rejected your credentials`: your token expired; log in again.
- `timed out during startup`: the credential plugin of your kubeconfig (a cloud login) or the API server did not answer within `--timeout`. A hung credential helper can keep running after the tool exits; stop it yourself.
- `<user> may not impersonate <name>`: ask a cluster admin for `impersonate` on users, groups and service accounts, or run without `--as`.
- `namespace <name> not found in context <context>`: check `-n` and `--context`.
- `namespace required`: pass `-n`, or set a namespace in the kubeconfig context.
- `cannot ask the cluster "may I?"`: the identity lacks `create selfsubjectaccessreviews`, which every authenticated user normally has.
- `--as-group and --as-uid need a user to impersonate`: the API needs a user with the groups. To test a group alone, use `--as audit-probe --as-group <group>`.
- `-o policyreport needs every check`: drop `--checks` and `--skip`.
- `-o policyreport needs to know who runs the tool`: no SelfSubjectReview named the identity. Use `--as`, or `-o json`.

**SKIP**

- `this identity may not even list pods in <namespace>`: check `-n`, `--as` and `--as-group`; the identity may be misspelled, miss its identity-provider prefix, or have no role in the namespace. On a cluster that started a few seconds ago, the built-in roles may still be empty; wait and run again.
- `RBAC denies create pods but allows <verb> <kind>` (or the same for Services): run with `--as` an identity that may create pods (or Services), or confirm that AT-09 and your policy engine cover controller-created pods and changes to existing Services.
- `the namespace rejects even a compliant pod` (or `a plain ClusterIP Service`): a gate refuses the compliant control probe, so the dangerous probes would prove nothing. The reason quotes the refusal; fix that first.
- `webhook "<name>" cannot take a dry run`: ask its owner to set `sideEffects: NoneOnDryRun` in the webhook configuration.
- A ResourceQuota or a LimitRange stopped the probe: free quota, or check the LimitRange `min` and `max`, then run again.
- `services.nodeports` quota full but above zero: NodePort Services are allowed. Set that quota to 0, or add a policy that rejects `type: NodePort`.
- A mutating webhook or policy changed the probe (it set `allowPrivilegeEscalation: false` next to `privileged: true`, or added a privileged container on a server with `--allow-privileged=false`): the API server refused the probe before any security gate ran, so it decides nothing. Check which mutation does it.
- `cannot list <resource> in <namespace> (forbidden)` or `cannot read namespace labels (forbidden)`: grant `list` (or `get` on the namespace) to the identity, or run with `--as` an identity that may read them.

**ERROR**

- A 429 or 5xx answer: run again; if it repeats, run with `--debug` and report the server message.
- `the server stored the <kind> <name>`: something between you and the API server drops `dryRun=All`, such as a proxy or gateway. Delete that object, and run against the API server directly.
- `not run: an earlier create answer could not be verified as a dry run`: see the earlier ERROR, delete any object it names, fix what it reports, then run again.
- The answer did not come from a Kubernetes API server: check the server URL and any proxy in your kubeconfig.
- The authorizer or a ValidatingAdmissionPolicy reported an error: check the authorization webhook, or the policy's expressions, parameters and binding.
- The cluster rejected the probe for a reason the tool does not know: read the reason, or run with `--debug` and report it.
- `not run: timeout after 30s`: raise `--timeout`, or run fewer checks with `--checks` (not with `-o policyreport`).
- `interrupted`: run again. If the hint lists leftovers, run `kubectl get pods,services -n <namespace> -l app.kubernetes.io/managed-by=audit-tenant`.
- `a bug in audit-tenant`: please report it with the `--debug` output.

## How it differs

Admin-side scanners describe what exists. [Popeye](https://github.com/derailed/popeye) and [Kubescape](https://github.com/kubescape/kubescape) read the cluster's objects with your rights and report misconfigurations against best practices or frameworks such as NSA-CISA, MITRE ATT&CK and CIS. [Kyverno](https://kyverno.io/docs/policy-types/cluster-policy/validate/) policies in `Audit` mode record violations of new and existing resources in PolicyReports. [NamespaceHound](https://github.com/wiz-sec-public/namespacehound) finds possible paths between namespaces from RBAC objects and pod host mounts, with read access to every resource type. None of them, by its own docs, asks the cluster as a tenant what that tenant may do. A `kubectl auth can-i --as <user>` script does, but it answers RBAC questions only; AT-04 and AT-05 also send dry-run creates through admission (PSA, webhooks and ValidatingAdmissionPolicy). [kubectl-mtb](https://github.com/kubernetes-retired/multi-tenancy/tree/master/benchmarks/kubectl-mtb) ran behavioral checks with `--as`; its repository was archived in June 2023, and its module no longer installs with `go install` ([BENCHMARK.md](BENCHMARK.md)). This tool uses its benchmark IDs. [kuota-calc](https://github.com/postfinance/kuota-calc) computes the quota that a set of manifests needs; AT-10 reads the live workloads and HPAs and compares their rollout peak with the namespace's real ResourceQuota.

## How it works

The check runner runs the ten checks in order, one at a time. Every request goes through the read-only transport to the API server, where RBAC, admission or the stored objects answer. The report holds one verdict, reason and hint per result, and the printers turn it into the checklist, wide, JSON or PolicyReport output. Diagram: [`docs/diagrams/architecture.svg`](docs/diagrams/architecture.svg). Design and decisions: [`DESIGN.md`](DESIGN.md) and [`docs/adr/`](docs/adr/).

The tool is a runnable form of the audit step of the Governed Namespace Lifecycle Framework (GNLF) in the paper below: after the request, policy validation, template, RBAC binding and quota steps produce a namespace, it checks the result from the tenant's side.

## Benchmarks

On kind v1.37.0 with Kyverno, a full run of every check took 0.10 s at the median and 0.68 s at most over five runs, with 40 API requests and 0 writes. All numbers in [BENCHMARK.md](BENCHMARK.md) come from runs of this code, with the command to repeat them. They are not copied from the paper.

## Development

`make check` runs vet, lint, a secret scan, the unit tests, the safety replay and the floor guard. `make e2e` builds a kind cluster with Kyverno and asserts every verdict for four identities; `make bench` then measures. Rules for changes are in [CONSTRAINTS.md](CONSTRAINTS.md).

## Using it? Tell us

If you use audit-tenant, please open an ["I use this" issue](../../issues/new?template=i-use-this.yml). It takes two minutes and helps the project. See [ADOPTION.md](ADOPTION.md).

## Prior art and citation

- [kubectl-mtb](https://github.com/kubernetes-retired/multi-tenancy/tree/master/benchmarks/kubectl-mtb) and the multi-tenancy benchmarks (Apache-2.0): the benchmark IDs.
- [NamespaceHound](https://github.com/wiz-sec-public/namespacehound) (Apache-2.0) and [kuota-calc](https://github.com/postfinance/kuota-calc) (MIT): the nearest tools.
- [Kyverno policies](https://github.com/kyverno/policies) (`restrict-nodeport`, Apache-2.0): the end-to-end test policy.
- The [PolicyReport CRD](https://github.com/kubernetes-sigs/wg-policy-prototypes) of the Kubernetes Policy Working Group.

This project implements ideas from:

> Bhasin, S. (2026). GNLF for Large-Scale Legacy Migration on a GitOps-Driven Kubernetes Platform. https://doi.org/10.56975/ijcrt.v14i4.305793

See [CITATION.cff](CITATION.cff).

## License

Apache-2.0
