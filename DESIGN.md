# kubectl audit-tenant: design

Status: **approved after plan reviews, 2026-10-11** (CEO review in SCOPE REDUCTION mode and engineering review, each with a Codex outside pass; decisions D1 to D28 and corrections F1 to F25 are one commit per batch in this file's history; the review records live in gstack's project folder). Earlier: v0, approved as the spec on 2026-10-10 in the spec session. When this file and an ADR disagree, this file wins and the ADR is updated. ADRs (why): `docs/adr/0001-read-only-by-construction.md`, `0002-checks-run-as-the-tenant.md`, `0003-cite-kubectl-mtb-do-not-fork.md`, `0004-four-verdicts-and-exit-codes.md`, `0005-probes-carry-sizes-and-a-control-probe.md`, `0006-rollout-headroom-counting-rules.md`, `0007-at-09-requires-the-psa-label.md`, `0008-policyreport-v1alpha2.md`, `0009-identity-is-what-the-api-server-says.md`. Terms: `GLOSSARY.md`.

Terms used below. AT: the prefix of every check ID (`AT-01` to `AT-10`); it stands for audit-tenant. SSAR: SelfSubjectAccessReview, an API call that asks the cluster "may this identity do X?" and does nothing. Dry-run: a create request with `dryRun=All`; the server runs every gate and stores nothing. Admission: the gate that inspects a new object before it is saved. PSA: Pod Security Admission, the built-in pod gate switched on by a namespace label. MTB: the archived Kubernetes Multi-Tenancy Benchmarks. PolicyReport: the `wgpolicyk8s.io` report object that policy dashboards read.

## 1. Problem

A platform team runs one Kubernetes cluster for many teams ("tenants"). Security and auditors ask: "prove that team A cannot touch team B". Tenants ask: "is my namespace set up right, and why was my pod rejected?". Today nobody can answer from the tenant's side. Admin-side scanners (Kyverno audit mode, Kubescape, Polaris, Popeye) infer what a tenant could do from rules; they never ask the cluster as the tenant. Permission viewers (rbac-tool, who-can, rakkess) show RBAC only. NamespaceHound (Wiz, 2024) enumerates cross-namespace escape paths, again from the admin side. The community tool that did ask as the tenant, `kubectl-mtb`, is archived (last release December 2021) and predates PSA. `kuota-calc` computes a quota from manifests but never compares it with the live namespace. Checked 2026-10-10: krew lists 411 plugins; none runs as the tenant.

`kubectl audit-tenant` runs ten checks as the tenant identity against one namespace and prints one verdict per check, with the layer that decided it, the matching MTB ID, a reason, and a hint. It never changes the cluster.

## 2. Users and first adopters

**Primary user: the platform team.** Mature platforms hide Kubernetes from application teams; many tenants never hold a kubeconfig, and a rejected pod becomes a message in the platform team's chat channel, not a kubectl error. For those platforms this tool is a test of the platform's own fences. The provisioning pipeline runs it right after it creates a namespace, impersonating the new tenant's identity, and fails the pipeline on a FAIL. Security or an auditor runs it on demand against any namespace and gets one page with a public benchmark ID per line. The team that wrote the guardrails should not be the only proof that they work; this checks their automation from the outside, the way an integration test checks code.

**Secondary users: tenants who do hold a kubeconfig.** Most namespace-per-team clusters on EKS, GKE and AKS, Capsule tenants and Rancher projects still hand out kubectl. Those teams run it against their own namespace with no special permission and read why a pod was rejected instead of asking in a channel.

**Why not build these checks into the platform's deploy code?** Good platforms do. This tool is the independent check on those checks: a standard list that names each fence with a public benchmark ID, runs in ten seconds against any cluster, and does not depend on the platform's own code being right. Teams that have the guardrails use it as the test. Teams that do not have them yet use it as the list.

| User | Runs it how | Wants |
|---|---|---|
| Platform engineer, at tenant onboarding | pipeline step: `kubectl audit-tenant -n team-a --as team-a-admin -o json --fail-on-skip`, exit code gates the step | Proof that the new namespace is fenced before the tenant gets it |
| Security or auditor | `kubectl audit-tenant -n team-a --as team-a-admin` | One page of PASS/FAIL with benchmark IDs, repeatable next quarter |
| Tenant team with kubectl | `kubectl audit-tenant -n team-a` with its own kubeconfig | "Is my namespace governed?" and "why was my pod rejected?" |
| Policy dashboard owner | `-o policyreport \| kubectl apply -f -` | The verdicts next to Kyverno results in Policy Reporter |

First adopters: three platform engineers at other companies (interviewed before the build goes far, see section 10), community meetup members with shared clusters, and Capsule users. The quick start must work on a kind cluster in five minutes.

## 3. Scope (v0.1)

**In.** Ten checks (section 5). Two identity modes: self (current kubeconfig identity) and impersonation (`--as`, `--as-group`). Four verdicts: PASS, FAIL, SKIP, ERROR (ADR 0004). Outputs: checklist (default), `-o wide`, `--plain`, `-o json`, `-o policyreport`. Exit codes for CI. Subcommands `list-checks` and `version`. Binaries for darwin and linux (amd64, arm64) and windows/amd64. krew manifest. Unit, envtest and kind end-to-end tests.

**Out (goes to `TODOS.md` as "later").** Network behavior tests (need running pods). Writing the PolicyReport into the cluster. Capsule `Tenant` discovery. SARIF. Gatekeeper constraint mapping. Helm chart or operator. Many namespaces or clusters in one run. `--baseline` file (v0.2). Argo Rollouts in the headroom math (v0.2; deferred in the CEO review of 2026-10-10, ADR 0006). DaemonSets, Jobs, CronJobs and bare Pods in the headroom math (ADR 0006). Cloud lookups for service principals (ADR 0009). Checking whether the network plugin enforces NetworkPolicy.

**Fallback.** If v0.1.0 is not tagged by 2026-10-31, tag with whatever passes, minimum AT-01, 02, 03, 04, 05, 06, 09: every behavioral isolation check plus quota and PSA presence. AT-10 is the heaviest and least security-critical check, so it is the first to wait for the next tag (swapped in the eng review, 2026-10-11, D28). The README's check table and `list-checks` show only shipped checks, and the acceptance criteria in section 12 apply to the shipped set. Checks never ship half-done: a check is in or out.

## 4. Architecture

```mermaid
graph LR
  U["kubectl audit-tenant<br/>-n team-a --as team-a-dev"] --> CLI["CLI<br/>flags, check selection,<br/>exit code 0 / 1 / 2"]
  CLI --> RUN["Check runner<br/>10 checks, fixed order"]
  RUN --> BEH["Behavioral checks<br/>AT-01..03: SSAR 'may I?'<br/>AT-04.x, AT-05: dry-run probes"]
  RUN --> CFG["Configuration checks<br/>AT-06..09: read objects<br/>AT-10: rollout headroom"]
  BEH --> SAFE["Read-only transport<br/>GET, LIST, SSAR, dry-run only<br/>blocks writes and Secret bodies"]
  CFG --> SAFE
  SAFE --> API["Kubernetes API server<br/>(the cluster)"]
  API --> RBAC["RBAC<br/>permission list"]
  API --> ADM["Admission<br/>PSA, webhook or policy"]
  API --> STORE["Stored objects<br/>quota, limits, netpol,<br/>labels, workloads"]
  RUN --> REP["Report<br/>PASS / FAIL / SKIP / ERROR<br/>reason + hint per result"]
  REP --> OUT["Printers<br/>checklist (default), wide, plain,<br/>json, policyreport"]
  classDef cluster fill:#E6F1FB,stroke:#185FA5,color:#042C53
  class API,RBAC,ADM,STORE cluster
```

Rendered copies: `docs/diagrams/architecture.svg` and `.png`. Editable scene: `docs/diagrams/architecture.excalidraw` (open at excalidraw.com). Source of truth: `docs/diagrams/architecture.mmd`.

Rules that shape the code:

- **Read-only by construction (ADR 0001).** `internal/safety.Transport` wraps the REST transport with an exact allowlist, not a verb rule, because GET is not read-only in Kubernetes (`pods/exec`, `attach`, `portforward` and `proxy` are GET upgrades). Allowed: GET `/version`; GET `/api/v1/namespaces/<target>`; GET (list) of `resourcequotas`, `limitranges`, `networkpolicies`, `deployments`, `statefulsets` and `horizontalpodautoscalers` in the target namespace; POST to `selfsubjectaccessreviews` and `selfsubjectreviews`; POST to `pods` or `services` in the target namespace only when the query has `dryRun=All`. Everything else is refused before it leaves the process: every subresource, any `secrets` path, any other namespace, and any PUT, PATCH or DELETE; a refusal is a bug in the tool. The test (section 8) drives a real clientset through the transport against an `httptest` server, replays every check, asserts that only allowlisted requests passed, and asserts that a deny-list of crafted requests (a pod `exec` GET, secrets GET and LIST, PATCH, DELETE, POST without `dryRun`, a request to another namespace) is refused.
- **Run as the tenant (ADR 0002).** Behavioral checks ask the cluster. The tool never reads Roles or policies to guess.
- **Fixed order, sequential.** Checks run one after another in ID order. About 35 requests per run. No concurrency in v0.1; a full run on kind must finish in under 10 seconds (section 7). The client uses QPS 50 and burst 300 (kubectl's values); client-go's defaults (5 and 10) would throttle the run.
- **One LIST per kind per run.** `resourcequotas` and `limitranges` are listed once and cached for the run; AT-06, AT-07, AT-10 and the probe sizing read the cache. The namespace object is read once at startup (section 6) and AT-09 reads the cached result.
- **One `--timeout` (default 30s)** bounds the whole run through a context deadline. `--request-timeout` (cli-runtime) bounds each single request; when both are set, the shorter one wins for that request.
- **No Argo Rollouts in v0.1.** AT-10 counts only built-in workload kinds, so the tool needs no dynamic client and no discovery call. Rollouts return in v0.2 (`TODOS.md`), read as unstructured objects without an Argo Go module.
- **Go style.** Code follows the JetBrains modern Go guidelines (agent skill `/modern-go-guidelines:use-modern-go`, loaded before any Go is written) and `golangci-lint` enforces what it can.
- **Stdlib first.** `text/tabwriter` for columns, `golang.org/x/term` (already a client-go dependency) to detect a terminal, hand-written ANSI for the four colors. No table or color library.

Go module: `github.com/BodhiSatvik/kubectl-audit-tenant`. Go 1.26. `k8s.io/client-go`, `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/cli-runtime` v0.37.x. `github.com/spf13/cobra`. Supported servers: Kubernetes 1.28 and newer (SelfSubjectReview is GA from 1.28). Import `_ "k8s.io/client-go/plugin/pkg/client/auth"` so cloud logins work (krew best practice).

### Package layout

| Path | Holds |
|---|---|
| `cmd/kubectl-audit_tenant/main.go` | Entry point. Binary name follows the krew rule (dash becomes underscore). |
| `internal/cli/` | cobra commands `audit-tenant` (run), `list-checks`, `version`; flag validation; exit codes |
| `internal/cluster/` | Client construction, identity lookup, server version |
| `internal/safety/` | The read-only transport and its tests |
| `internal/checks/` | `Spec` (static description plus its `Run` function), registry, selection, runner, `at01.go` to `at10.go`, MTB mapping table, severities |
| `internal/probe/` | Probe builders (control pod, 5 dangerous pods, NodePort service), rejection classifier |
| `internal/headroom/` | Pure functions for AT-10: peak per workload, pod request math, quota comparison |
| `internal/report/` | `Report` and `Result` types; the five printers |
| `test/e2e/` | kind config, `versions.env`, fixtures, Kyverno policy, expected verdicts, run script |
| `hack/check-public.sh` | Fails on unfilled `{{`...`}}` placeholders; runs in CI |
| `.github/workflows/` | `ci.yml` (vet, lint, unit, envtest), `e2e.yml` (kind), `release.yml` (goreleaser) |
| `.goreleaser.yaml` | Release; its `krews` block renders the krew manifest (`dist/krew/audit-tenant.yaml`) |

## 5. The checks

Severity is static per check and appears in `-o json`, `-o policyreport`, and `list-checks`. Every non-PASS result carries a reason (the cluster's own text where possible) and a hint.

| ID | Name | Kind | MTB | Severity | Method | PASS when |
|---|---|---|---|---|---|---|
| AT-01 | No cluster-scoped access | behavioral | MTB-PL1-CC-CPI-1 | high | SSAR with no namespace (these resources are cluster-scoped; a namespaced SSAR draws an API warning): `list nodes`, `create namespaces`, `create clusterrolebindings`, `create persistentvolumes`; first a positive control, `create selfsubjectaccessreviews`, which any API server allows (a bare no is ERROR: something other than the API server answered, D55; a no with `denied: true` came from an authorizer such as an access proxy with its own roles, and the questions go ahead, D57) | all four denied |
| AT-02 | No cross-namespace secret access | behavioral | MTB-PL1-CC-TI-2 | high | SSAR: `get secrets` and `list secrets` in `--other-namespace` (default `kube-system`) | both denied. SSARs carry no resource name, so a grant limited by `resourceNames` to specific secrets is not detected; the PASS reason says "no general get or list on secrets in kube-system (named-secret grants are not tested)" |
| AT-03 | No privilege escalation | behavioral | MTB-PL2-CC-TI-1 | high | SSAR: `escalate` and `bind` on `roles` scoped to the target namespace and on `clusterroles` with no namespace; `impersonate` on `users`, `groups`, `serviceaccounts` with no namespace; `*` verb on `*` resource in `*` API group with no namespace | all denied. The in-namespace wildcard is **not** tested: a namespace-scoped `*/*` role is a common, legitimate tenant-admin pattern. In-namespace `impersonate` on `serviceaccounts` is **not** tested either: the built-in `edit` and `admin` roles grant it, so it is a normal tenant permission; the reason says so. Grants limited by `resourceNames` (one named user to impersonate, one named role to bind) are not detected by unnamed SSARs; the PASS reason says so |
| AT-04.1 | Privileged container blocked | behavioral | MTB-PL1-BC-CPI-5 | high | dry-run pod with `privileged: true` | rejected by admission or RBAC |
| AT-04.2 | hostPath volume blocked | behavioral | MTB-PL1-BC-HI-1 | high | dry-run pod with a `hostPath` volume | rejected |
| AT-04.3 | hostPID blocked | behavioral | MTB-PL1-BC-HI-4 | high | dry-run pod with `hostPID: true` | rejected |
| AT-04.4 | hostNetwork blocked | behavioral | MTB-PL1-BC-HI-3 | high | dry-run pod with `hostNetwork: true` | rejected |
| AT-04.5 | Privilege escalation to root blocked | behavioral | MTB-PL1-BC-CPI-6 | high | dry-run pod with `allowPrivilegeEscalation: true`, `runAsUser: 0`, `runAsNonRoot: false` | rejected. PSA `baseline` does not block this probe; the FAIL hint names both fixes: set `pod-security.kubernetes.io/enforce=restricted`, or add a policy that forbids `runAsUser: 0` and `allowPrivilegeEscalation` (Kyverno `require-run-as-nonroot` + `disallow-privilege-escalation`, or a ValidatingAdmissionPolicy) |
| AT-05 | NodePort services blocked | behavioral | MTB-PL1-BC-HI-1 (shared upstream ID; mapped by title) | medium | dry-run `Service` with `type: NodePort` | rejected by admission or RBAC |
| AT-06 | ResourceQuota present and complete | config | MTB-PL1-CC-FNS-1 | medium | LIST resourcequotas | at least one quota with no `scopes`/`scopeSelector` whose `hard` has `requests.cpu` (or `cpu`), `requests.memory` (or `memory`), `limits.cpu`, `limits.memory`, `pods` |
| AT-07 | LimitRange with defaults | config | - | low | LIST limitranges | an item of type `Container` with `default` and `defaultRequest` for both cpu and memory |
| AT-08 | Default-deny NetworkPolicy | config | - | medium | LIST networkpolicies | a policy with empty `podSelector`, `Ingress` in `policyTypes`, no `ingress` rules; AND a policy (same or other) with empty `podSelector`, `Egress` in `policyTypes`, no `egress` rules. Other policies do not affect the verdict. Reason notes the tool cannot see whether the network plugin enforces policies; the FAIL hint shows the two-policy pattern (one empty-selector deny per direction; allow rules, such as DNS egress, in separate policies) |
| AT-09 | Pod Security Admission enforced | config | - | high | GET namespace labels | `pod-security.kubernetes.io/enforce` is `baseline` or `restricted` (ADR 0007). Forbidden to read: SKIP with hint |
| AT-10 | Quota covers rollout peak | computed | - | low | LIST deployments, statefulsets, horizontalpodautoscalers, resourcequotas, limitranges | the summed rollout peak fits in every unscoped quota's `hard` for `requests.cpu`, `requests.memory`, `limits.cpu`, `limits.memory`, `pods` (ADR 0006) |

`--checks AT-04` selects all five sub-checks. `list-checks` prints ID, kind, MTB, severity, name, and the method in one line each.

### Probes (AT-04, AT-05), ADR 0005

- Image `registry.k8s.io/pause:3.10` (never pulled: dry-run stores nothing). Container `probe`. Sizes follow the namespace's LimitRange (ADR 0005): if a `Container` LimitRange with `defaultRequest` and `default` for both cpu and memory exists, the probe carries no requests or limits and the server fills them in; otherwise requests and limits are `1m` CPU and `1Mi` memory, raised to the LimitRange `min` when one exists; if limitranges cannot be listed (403), `1m`/`1Mi`. The control pod uses the same rule. `generateName: audit-tenant-probe-`, label `app.kubernetes.io/managed-by: audit-tenant`, annotation `audit-tenant.dev/probe: "true"`.
- The **control pod** is PSA-restricted compliant: `runAsNonRoot: true`, `runAsUser: 65532`, `allowPrivilegeEscalation: false`, `capabilities.drop: ["ALL"]`, `seccompProfile: RuntimeDefault`. Each dangerous probe is the control pod plus exactly one dangerous capability. Two probes need extra fields to be valid objects: AT-04.1 sets `privileged: true` together with `allowPrivilegeEscalation: true`, because the API server rejects `privileged: true` with `allowPrivilegeEscalation: false` at validation (the fixture would otherwise produce ERROR, not FAIL); AT-04.5 needs three fields to express its one capability (run as root with escalation allowed): `allowPrivilegeEscalation: true`, `runAsUser: 0`, `runAsNonRoot: false`.
- Order, AT-04: (1) SSAR `create pods` in the target namespace. Denied → SSAR `create`, `update` and `patch` on `deployments`, `replicasets`, `statefulsets`, `daemonsets` (group `apps`), `jobs` and `cronjobs` (group `batch`) in the target namespace (18 SSARs, only on this branch; changing an existing controller's pod template also yields new pods). All denied → AT-04.1 to AT-04.5 PASS, layer RBAC, reason "RBAC denies create pods and any create, update or patch of a pod controller; admission not exercised". Any allowed → AT-04.1 to AT-04.5 SKIP, reason "RBAC denies create pods but allows <verb> <kind>; pods created by a controller bypass the tenant's RBAC, so admission must be the fence and it was not exercised", hint "run with --as <an identity that may create pods>, or confirm AT-09 and your policy engine cover controller-created pods". No probe is sent on either branch. (2) Allowed → dry-run the control pod. Rejected → AT-04.1 to AT-04.5 SKIP with the control's rejection text and the hint "the namespace rejects even a compliant pod; fix that first". (3) Control accepted → dry-run the five probes, one result each. The control pod gates only AT-04; it is sent only when at least one AT-04 sub-check is selected.
- Order, AT-05: (1) SSAR `create services`. Denied → SSAR `update` and `patch` on `services`; both denied → PASS, layer RBAC, reason "RBAC denies create, update and patch of services; admission not exercised"; either allowed → SKIP, reason "RBAC denies create services but allows <verb> services; an existing Service can be changed to type NodePort, so admission must be the fence and it was not exercised", hint "run with --as <an identity that may create services>, or confirm your policy engine rejects type: NodePort on update". (2) `create services` allowed → dry-run a compliant ClusterIP control Service (same generateName and labels, one port 80/TCP, no selector). Rejected → AT-05 SKIP with the control's rejection text and the hint "the namespace rejects even a plain ClusterIP Service; fix that first". (3) Control accepted → dry-run the NodePort service. Rejected by admission → PASS with the layer; accepted → FAIL.
- Because RBAC is settled by SSAR before any probe, a probe rejection is never attributed to RBAC.
- **Classifier** on a probe rejection, rules applied in this order, first match wins: (1) message contains `violates PodSecurity` → PASS, layer admission (PSA), the violated controls kept in the reason; (2) message contains `admission webhook` and `denied` → PASS, layer admission (webhook), webhook name kept; (2b) message contains `ValidatingAdmissionPolicy` and `denied request` → PASS, layer admission (policy), policy and binding names kept in the reason; (3) message contains `does not support dry run` or `sideEffects` → SKIP, hint names the webhook and the `sideEffects: NoneOnDryRun` fix; (4) message contains `exceeded quota`, `must specify`, `forbidden: maximum`, or `forbidden: minimum` → SKIP (inconclusive; quota or LimitRange gate), hint says which; (5) HTTP 400 or 422 → ERROR (the probe itself is malformed; a bug to report); (6) any other rejection → ERROR with the full server text, never a guess; when the message is empty the reason is "HTTP <code> <reason>". A probe that is accepted (HTTP 200 or 201) → FAIL.
- AT-05 service: `type: NodePort`, one port 80/TCP, no selector, same generateName and labels.

### Headroom math (AT-10), ADR 0006

- Counted: Deployments, StatefulSets. Not counted in v0.1: Argo Rollouts (v0.2), DaemonSets, Jobs, CronJobs, bare Pods. The reason names the counted and not-counted kinds and shows each quota's `used` values so the reader sees what else consumes it.
- Replicas: `spec.replicas` (default 1). If a `HorizontalPodAutoscaler` (autoscaling/v2) targets the workload (`scaleTargetRef` matched by API group, kind and name), use its `maxReplicas` instead.
- Peak pods: Deployment RollingUpdate = replicas + maxSurge (absolute, or percent of replicas rounded up; default 25%); Deployment Recreate = replicas; StatefulSet = replicas.
- Pod request and limit: if the pod template sets pod-level `spec.resources` (beta, on by default since Kubernetes 1.34), those requests and limits are the pod's values and the container sum is ignored. Otherwise the Kubernetes rule: a container with a limit and no request counts its limit as the request (the API server defaults it so). Sidecars are init containers with `restartPolicy: Always`. Pod value = max(sum of app containers + sum of sidecars, max over ordered init containers of that container + the sidecars declared before it). Pod overhead from a RuntimeClass is not counted in v0.1 and the reason says so when any workload names a RuntimeClass. A container with no request or limit takes the LimitRange `defaultRequest` or `default` for type Container if one exists, else 0, and the reason says "N containers had no request; counted as 0".
- Compare: for every unscoped ResourceQuota and each of the five keys it defines (`cpu`, `memory` and `count/pods` count as `requests.cpu`, `requests.memory` and `pods`), peak sum ≤ hard. FAIL reason: `peak needs 6 cpu, hard is 4 cpu, short by 2 (deployments: api 3→4, worker 2→3)`. Assumes all workloads roll out at once; the reason says so.
- Edge cases, checked in this order: no unscoped ResourceQuota → FAIL, reason "no ResourceQuota to compare against" (scoped quotas named if any), hint points at AT-06; then, no counted workloads → PASS, reason "no Deployments or StatefulSets; nothing to compare"; several LimitRanges → the first by name that has Container defaults is used and named in the reason; a quota that defines only some of the five keys → only those keys are compared; cannot LIST deployments, statefulsets, resourcequotas or horizontalpodautoscalers → SKIP with the missing permission in the hint; cannot LIST limitranges while a counted container has no explicit request or limit → SKIP likewise; in both SKIP cases, if the partial sum already exceeds a hard value the verdict is FAIL, because the truth can only be worse. Every AT-10 reason starts with "estimate: active pods during a rollout; terminating pods and uncounted kinds can add to usage; see used".

## 6. Interfaces

### Command surface

```
kubectl audit-tenant -n NAMESPACE [--as USER] [--as-group GROUP ...]
                     [-o checklist|wide|json|policyreport] [--plain] [--no-color] [--debug]
                     [--fail-on-skip]
                     [--checks ID,...] [--skip ID,...]
                     [--other-namespace kube-system] [--timeout 30s]
kubectl audit-tenant list-checks [-o table|json]
kubectl audit-tenant version
```

Also honored: `--kubeconfig`, `--context`, `--cluster`, `--user`, `--request-timeout` (from cli-runtime), `KUBECONFIG`, `NO_COLOR`. `-h`/`--help` usage text starts with `kubectl audit-tenant`.

`--debug` writes to stderr one line per API request (method, path, HTTP status, duration), the identity resolution steps, and the full server body of every SSAR and probe response, plus the SelfSubjectReview answer without `status.userInfo.extra` (identity-provider claims; an answer that is not JSON is not shown; D51). It never prints request headers. stdout and the exit code are unchanged, so `-o json | jq` still works. The safety transport's request recorder (section 4) is its source.

Selection rules: `--checks` keeps only the listed IDs (families like `AT-04` or sub-checks like `AT-04.2`), then `--skip` removes IDs from that set; an unknown ID in either flag exits 2; an empty final set exits 2 with "no checks selected". `-n` omitted → the namespace of the current kubeconfig context; none set → exit 2 "namespace required". The run proceeds even when the namespace cannot be read by the identity (behavioral checks need no read); AT-09 then reports SKIP.

Startup: the tool first calls the version endpoint (unreachable → exit 2 "API server unreachable: <error>"; HTTP 401 → exit 2 "unauthorized: the cluster rejected your credentials (expired token?); log in again and retry"), then POSTs a SelfSubjectReview (HTTP 403 or 404 → identity falls back to the kubeconfig and `--as` values, marked `unverified`), then GETs the target namespace once (HTTP 404 → exit 2 "namespace <n> not found in context <ctx>; check -n or --context"; HTTP 403 → the run proceeds and AT-09 reports SKIP from this cached answer), then sends one of AT-01's SSARs (`list nodes`, whose answer AT-01 reuses); if that SSAR itself is refused with 403, the identity cannot ask "may I?" at all and the run exits 2 with the hint "this identity lacks `create selfsubjectaccessreviews`, which every authenticated user normally has". Startup runs under the same deadline and signal handling as the checks: client construction and the first requests execute on a goroutine the run waits on with the context, so a hung exec credential plugin (a cloud login) or a server that never answers ends with exit 2 "timed out during startup: the credential plugin or the API server did not answer within <timeout>" instead of a hang. Before the SSAR hint applies, any startup 403 whose message contains `cannot impersonate` exits 2 with "<user> may not impersonate <--as value>: ask a cluster admin for `impersonate` on users, groups and serviceaccounts, or run without --as".

Failure rules: a read that the API server refuses with HTTP 403 is a fact about the identity, so the check reports SKIP with the missing permission in the hint (AT-06 to AT-10 for their own objects; AT-10 is SKIP when it cannot list HPAs, or LimitRanges while a counted container has no explicit sizes, unless the partial sum already exceeds the quota, which is FAIL). Any other API error makes the check ERROR. The whole run has one deadline (`--timeout`); a check that has not finished when it expires, and every check after it, gets ERROR "not run: timeout after 30s". SIGINT or SIGTERM cancels the run the same way: checks that have not finished get ERROR "interrupted" with the hint "re-run; the cluster was not changed", the report prints with the results so far, and the exit code is 2; a second signal exits at once. An API error inside one check makes that check ERROR and the run continues. An SSAR answer is read as the API defines it: `allowed: true` means allowed; `allowed: false` means denied whether or not `denied` is set (RBAC never sets `denied`; an explicit `denied: true` from a webhook authorizer adds "explicit deny" to the reason); a non-empty `evaluationError` makes the check ERROR with the server text unless the answer is `allowed: true`, which stands (D52); an answer that does not echo the question is ERROR, never a denial (D52); a proven grant stays FAIL even when a later question fails or the deadline hits, and a failed request ends the check's questions (client-go retries each one). Every selected check always appears in the output, whatever happened.

### Exit codes (ADR 0004)

| Code | When |
|---|---|
| 0 | No FAIL and no ERROR. SKIPs allowed (default). |
| 1 | At least one FAIL; or, with `--fail-on-skip`, at least one SKIP. `--fail-on-skip` is the opt-in gate for pipelines that must not accept an unproven namespace; the summary line then says "N skipped, counted as failures (--fail-on-skip)" and `-o json` reports the resulting `exitCode`. |
| 2 | At least one ERROR and no FAIL; or the run could not start: bad flags (`--as-group` with no user to impersonate, from `--as` or the kubeconfig user's `as:`; unknown check ID, unknown `-o`), kubeconfig cannot load, API server unreachable, credentials rejected (401), impersonation refused, namespace not found, or the identity may not create SelfSubjectAccessReviews. Each with a one-line reason and hint on stderr. |

### Checklist output (default, chosen in the spec session)

```
namespace team-a · as team-a-dev (groups: team-a, system:authenticated) · cluster kind-dev

PASS  AT-01  No cluster-scoped access            RBAC denied all 4 actions
FAIL  AT-05  NodePort services blocked           a NodePort Service was accepted in a dry run
      fix    add a policy that rejects type: NodePort, or remove `create services` from the tenant role
SKIP  AT-09  Pod Security Admission enforced     cannot read namespace labels (forbidden)
      fix    grant `get` on namespace team-a to this identity, or run with --as <admin>

12 passed · 1 failed (AT-05) · 1 skipped (AT-09) · 0 errors · exit 1
MTB IDs and layers: -o wide · machine output: -o json, -o policyreport
```

Rules: counts are per result, not per check family (ten checks produce fourteen results, because AT-04 has five); one line per result in ID order; AT-04 prints its five sub-results; a `fix` line under every FAIL, SKIP and ERROR; header line names namespace, identity (with `(unverified)` when SelfSubjectReview failed, `(self)` without `--as`) and cluster context; the summary line lists up to three failed IDs and up to three skipped IDs, then "and N more"; the line after it names the next commands. Color (green, red, yellow, magenta for ERROR) only when stdout is a terminal, `NO_COLOR` is unset and `--no-color` is absent; `-o json` and `-o policyreport` never carry color. On Windows the tool enables virtual-terminal processing on the console before printing color; if that fails, color is off. `--plain` removes color and the `·` separators (ASCII only). Server text in the terminal printers (checklist, wide, plain) has ASCII control characters and ANSI escapes stripped and newlines folded to `; `. The checklist caps a reason at the terminal width minus the fixed columns (minimum 60 characters; 120 when stdout is not a terminal) with `…` and adds the footer line `full reasons: -o wide or -o json` when any reason was cut; `-o wide`, `-o json` and `-o policyreport` keep the full text. In self mode, when AT-03's cluster-wide wildcard is allowed, the header adds: `this identity looks like a cluster admin; add --as to test a tenant`.

`-o wide` adds two columns after the ID: MTB ID and deciding layer (`RBAC`, `admission (PSA)`, `admission (webhook: kyverno-...)`, `admission (policy: <name>)`, `config`, `-`).

### JSON (`-o json`)

```json
{
  "apiVersion": "audit-tenant.dev/v1alpha1",
  "tool": {"name": "audit-tenant", "version": "0.1.0"},
  "cluster": {"context": "kind-dev", "server": "https://127.0.0.1:6443", "version": "v1.37.0"},
  "namespace": "team-a",
  "otherNamespace": "kube-system",
  "identity": {"mode": "impersonation", "username": "team-a-dev", "groups": ["team-a", "system:authenticated"], "verified": true},
  "timestamp": "2026-10-20T15:04:05Z",
  "results": [
    {"id": "AT-05", "name": "NodePort services blocked", "kind": "behavioral", "mtb": "MTB-PL1-BC-HI-1",
     "severity": "medium", "verdict": "FAIL", "layer": "-",
     "reason": "a NodePort Service was accepted in a dry run",
     "hint": "add a policy that rejects type: NodePort, or remove `create services` from the tenant role"}
  ],
  "summary": {"pass": 12, "fail": 1, "skip": 1, "error": 0, "exitCode": 1, "results": 14, "checks": 10}
}
```

`verdict` is one of `PASS`, `FAIL`, `SKIP`, `ERROR`. `mtb` is `"-"` when none. The `audit-tenant.dev` domain in `apiVersion` and in the probe annotation is owned by the project: Satvik registered it on 2026-10-11 (CEO review, D20). The key is one-way after release. A JSON Schema at `docs/report.schema.json` is checked in and the e2e validates against it.

### PolicyReport (`-o policyreport`), ADR 0008

`apiVersion: wgpolicyk8s.io/v1alpha2`, `kind: PolicyReport`, `metadata.name: audit-tenant-<identity slug>` (the identity lowercased with every character outside `a-z0-9-` replaced by `-`, truncated to 54 characters plus an 8-character hash of the full identity when truncated), `metadata.namespace: <target>`, labels `app.kubernetes.io/managed-by: audit-tenant`, annotations `audit-tenant.dev/identity`, `audit-tenant.dev/groups`, `audit-tenant.dev/verified` and `audit-tenant.dev/tool-version`. A re-run for the same identity replaces its own report; reports for different identities coexist in the namespace (eng review, D27). `scope` references the Namespace. One `results[]` entry per result: `policy` = check ID (for example `AT-04.2`), `rule` = MTB ID (omitted when `-`), `result` = `pass|fail|skip|error`, `severity` = `high|medium|low`, `category: multi-tenancy`, `source: audit-tenant`, `message` = reason, `timestamp`, `properties: {layer, hint, name, identity}`. `summary` holds the four counts. Printed to stdout; the user applies it. The tool never writes it.

## 7. Benchmark plan

Numbers in `BENCHMARK.md` come only from runs of this code.

- **What we measure.** Wall time of a full ten-check run on kind: median and maximum of 5 consecutive runs, target median under 10 s. API requests per run: every HTTP request that passes the safety transport, including discovery, the identity lookup, pagination pages and any client retry (client-go retries are left at their defaults and counted as requests); target at most 40. Write requests per run: a write is any POST, PUT, PATCH or DELETE whose query lacks `dryRun=All`; SSAR and SelfSubjectReview POSTs persist nothing and count as reads; the number must be 0 and the safety test fails otherwise. Binary size per platform.
- **Baselines.** `kubectl-mtb` run on the same kind cluster where it still builds; otherwise record "does not build against 1.37" as the finding. Popeye and Kubescape are not like-for-like; we list them as "different scope" rather than compare numbers.
- **Data and environment.** kind with `kindest/node:v1.37.x`, the fixtures in `test/e2e/`, GitHub Actions `ubuntu-latest`. Commands and versions recorded with each row.
- **Repeat it.** `make e2e` runs the same job locally; the row's `Command` column holds the exact invocation.

## 8. Tests

| Layer | What | Tool |
|---|---|---|
| Unit | Every check against scripted cluster answers: startup answers (401, a 403 containing `cannot impersonate`, namespace 404); SSAR answers (allowed, RBAC-denied, explicit deny, evaluationError); the AT-04 controller branch; probe sizing (LimitRange defaults, min only, none, 403); PSA, webhook, ValidatingAdmissionPolicy, RBAC, quota, dry-run-refused and empty-message rejections with real error strings; AT-06 to AT-09 object shapes (two-policy default deny, scoped quotas, missing keys); headroom math table tests (percent surge, HPA, init containers, LimitRange defaults); every printer against golden files, including long, multi-line and ANSI-bearing reasons, `NO_COLOR` and `--plain`; a `--debug` stderr golden. Transport, deadline, SIGINT and startup-timeout tests use a real clientset (`kubernetes.NewForConfig`) against an `httptest.Server` with canned responses, because the fake clientset bypasses HTTP and its reactors take no context: the safety allowlist replay and deny-list, a server that never answers, a sleeping credential helper, and interruption after a FAIL and midway through AT-04 | `client-go/kubernetes/fake` for check logic, `net/http/httptest` for transport and cancellation, golden files |
| Integration | AT-01 to AT-03 against a real API server with real RBAC: a Role and RoleBinding fixture per expected verdict; SSAR under impersonation; `--as-group` with no user to impersonate rejected | `envtest` (`setup-envtest use 1.37.x` in CI) |
| End-to-end | kind + Kyverno + fixtures (below); the job waits for the Kyverno admission controller to be Available and for a dry-run NodePort Service to be rejected before run 1; runs as cluster-admin with `--as`, as the service account's own token (self mode), and as cluster-admin without `--as`; asserts every verdict, the exit code, the admin hint, `-o json` against the schema, and `-o policyreport` with `kubectl apply --dry-run=server` after installing the PolicyReport CRD | GitHub Actions, kind, Helm |

End-to-end fixtures (`test/e2e/fixtures/`), all values exact so the expected verdicts are reproducible:

- `tenant-a`: Namespace with labels `pod-security.kubernetes.io/enforce=restricted`, `enforce-version=latest`. ResourceQuota `tenant-a-quota` with `hard`: `requests.cpu: "4"`, `requests.memory: 8Gi`, `limits.cpu: "8"`, `limits.memory: 16Gi`, `pods: "20"`. LimitRange `tenant-a-limits`, type Container, `default: cpu 500m, memory 512Mi`, `defaultRequest: cpu 100m, memory 128Mi`. NetworkPolicies `default-deny-ingress` (empty podSelector, policyTypes [Ingress]) and `default-deny-egress` (empty podSelector, policyTypes [Egress]). ServiceAccount `tenant-a-dev` with RoleBinding to ClusterRole `edit` and a Role `namespace-reader` granting `get` on `namespaces` with `resourceNames: [tenant-a]` (RBAC evaluates a request for the namespace object as a request inside that namespace, so a Role can grant it; `edit` and `admin` already include namespace reads, and the e2e proves both paths). Deployment `api`: 3 replicas, container requests 100m/128Mi, limits 500m/512Mi, RollingUpdate maxSurge 25%. HPA `api` targeting it with maxReplicas 4. Expected AT-10 peak: 5 pods, 0.5 cpu / 640Mi requests, 2.5 cpu / 2.5Gi limits; fits.
- `tenant-b`: Namespace with no labels and no quota, LimitRange, NetworkPolicy or workloads. ServiceAccount `tenant-b-dev` with RoleBinding to ClusterRole `admin`.
- Cluster-wide: Kyverno from its Helm chart with the `restrict-nodeport` ClusterPolicy from `kyverno/policies`, `validationFailureAction: Enforce`. The PolicyReport CRD (`wgpolicyk8s.io/v1alpha2`) installed for the apply check. Exact versions live in `test/e2e/versions.env` (kind node image tag, Kyverno chart version, `kyverno/policies` commit, CRD commit); CI reads only that file, and the first commit of the file pins the newest 1.37 node image and Kyverno 1.x release available at that time.

Expected verdicts (every row is asserted by the e2e script, with exit code):

| Run | Identity | AT-01..03 | AT-04.1..5 | AT-05 | AT-06..09 | AT-10 | Exit |
|---|---|---|---|---|---|---|---|
| 1 | cluster-admin `--as system:serviceaccount:tenant-a:tenant-a-dev`, `-n tenant-a` | PASS (RBAC) | PASS (admission, PSA) | PASS (admission, webhook) | PASS | PASS | 0 |
| 2 | the `tenant-a-dev` token as its own kubeconfig, no `--as`, `-n tenant-a` | same as run 1 | same | same | same | same | 0 |
| 3 | cluster-admin `--as system:serviceaccount:tenant-b:tenant-b-dev`, `-n tenant-b` | PASS (RBAC) | FAIL (no PSA label, `admin` may create pods) | PASS (admission, webhook) | FAIL | FAIL (no quota) | 1 |
| 4 | cluster-admin, no `--as`, `-n tenant-a` | FAIL (cluster rights) with the admin hint in the header | PASS (PSA applies to admins too) | PASS (webhook) | PASS | PASS | 1 |

Run 1 also validates `-o json` against `docs/report.schema.json` and `-o policyreport` with `kubectl apply --dry-run=server`. Run 2 asserts the header shows groups `system:serviceaccounts` and `system:serviceaccounts:tenant-a`, which the API server adds to an impersonated or token-authenticated service account.

## 9. Release

- `goreleaser` on tag `v*`: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64; `.tar.gz` (`.zip` on Windows) with `LICENSE` and `README.md` inside; `checksums.txt` (sha256). Version, commit and date injected with `-ldflags`.
- goreleaser's `krews` block renders the krew manifest (`dist/krew/audit-tenant.yaml`, not uploaded to krew-index) and `release.extra_files` uploads it as the release asset `audit-tenant.yaml`; the release job tests, each in its own clean `KREW_ROOT` so the second install cannot be skipped as already installed, `kubectl krew install --manifest=audit-tenant.yaml --archive=<tar>` and then `kubectl krew install --manifest-url=<the published asset URL>` on `ubuntu-latest` and `macos-latest`, and runs `kubectl audit-tenant version` from each install. First krew-index PR is manual; later versions may use krew-release-bot.
- README: problem in two sentences; five-line quick start on kind with three install paths in order (krew by name once the index PR has merged; `kubectl krew install --manifest-url <release asset>` from the first release on; `go install github.com/BodhiSatvik/kubectl-audit-tenant/cmd/kubectl-audit_tenant@<tag>`); the check table (with a note that AT-04.5 needs `restricted` or a policy, because `baseline` alone does not block it); a "what a PASS does not prove" paragraph (grants limited by `resourceNames`, network-plugin enforcement, PSA exemptions, controller-created pods when AT-04 shows SKIP); a troubleshooting section with one entry per SKIP and ERROR hint and the line "run again with --debug and paste the stderr into the issue"; "how this differs" paragraph with links (popeye, kubescape, kyverno audit mode, kubectl-mtb, NamespaceHound, kuota-calc, and the `kubectl auth can-i --as` script pattern); safety section, including what a run leaves in the API audit log (SSARs and dry-run creates under the tenant identity, with the impersonating user recorded); prior-art citations; `CITATION.cff`.
- `ADOPTERS.md` and the "I use this" issue template already exist from the scaffold.

## 10. Risks

| Risk | Effect | Handling |
|---|---|---|
| Premise: platforms that hide Kubernetes have no tenant users | Fewer users than hoped | The platform team is the primary user (pipeline step, audits). Before the build goes far, three platform engineers at other companies are asked how they last proved tenant isolation; if all three say an admin-side policy engine in audit mode already answers their auditors, the project stops |
| False PASS from a non-security gate | Tool says safe when it is not | Control probe and classifier (ADR 0005); unit tests with real error strings; e2e |
| PSA exemptions for the impersonated user | Dry-run passes for an exempt identity | Reason prints the identity; README explains exemptions |
| Webhooks that refuse dry-run | AT-04/05 SKIP on some clusters | SKIP with the webhook name and the `sideEffects` hint |
| Tenant cannot read its own namespace or quota objects | AT-06 to AT-10 SKIP in self mode | Hint names the exact permission; impersonation mode for admins |
| Service-account impersonation groups | The API server adds `system:serviceaccounts*` groups automatically; if it does not on some version, AT results differ | e2e asserts the groups in the header; README states the behavior |
| HPA or Rollout math wrong in an edge case | False AT-10 verdict | Table tests; reason prints the per-workload numbers so a reader can check by hand |
| Time: three weeks for v0.1 | Late tag | Six-check fallback on 2026-10-31 |

## 11. Questions settled before the plan reviews

Satvik settled these on 2026-10-10, before the engineering plan review. Each took the recommended answer.

1. AT-01 tests four cluster-scoped actions in v0.1: `list nodes`, `create namespaces`, `create clusterrolebindings`, `create persistentvolumes`. More actions arrive with the `--baseline` file in v0.2.
2. `--other-namespace` keeps `kube-system` as its default, because every cluster has it.
3. AT-10 uses the HPA's `maxReplicas` as the replica count when an HPA targets the workload. That is the real peak the quota must hold.
4. The summary line lists up to three failed IDs and up to three skipped IDs, then "and N more" for each group. This keeps the last line short.
5. CI runs kind on one server version (1.37) in v0.1. A weekly run on the oldest supported server (1.28) stays in `TODOS.md` "Later".

## 12. Acceptance criteria (v0.1.0)

1. `go test ./...` passes; `golangci-lint run` reports zero issues.
2. The safety test records every request each check makes and fails on any request outside the allowlist in section 4, and on any GET or LIST of `secrets`; the `--debug` output never contains request headers.
3. envtest: AT-01, AT-02, AT-03 return PASS for a namespace-only Role and FAIL for a cluster-admin binding; `--as-group` with no user to impersonate exits 2 with the documented message.
4. kind e2e produces exactly the verdicts and exit codes in the section 8 table for all four runs.
5. `-o json` validates against `docs/report.schema.json`; `-o policyreport` passes `kubectl apply --dry-run=server` with the v1alpha2 CRD installed.
6. Every FAIL, SKIP and ERROR in the e2e output has a non-empty `hint`.
7. The median of 5 full runs on kind is under 10 s, and no e2e run makes more than 40 API requests (the AT-04 denied branch, which no fixture exercises, may send up to 18 SSARs in place of 7 probe requests); median, maximum and request count are in `BENCHMARK.md` with the command.
8. `kubectl krew install --manifest --archive` installs the release artifact on macOS and Linux in the release job, and `kubectl krew install --manifest-url` installs it from the published release; `kubectl audit-tenant version` prints the tag.
9. README quick start works on a clean machine with Docker, kind and kubectl in five minutes.
10. `hack/check-public.sh` (in this repo; fails only on unfilled `{{`...`}}` placeholders in tracked files) passes in CI. The maintainer's private pre-release check also passes; it is a maintainer step, not a CI step.
11. Tag `v0.1.0` exists; release has five archives and `checksums.txt`.

## 13. Related

- ADRs 0001 to 0009 in `docs/adr/`.
- Prior art: kubectl-mtb (`sigs.k8s.io/multi-tenancy/benchmarks/kubectl-mtb`, Apache-2.0), NamespaceHound (`wiz-sec-public/namespacehound`), kuota-calc (MIT), Kyverno policies (`restrict-nodeport`, Apache-2.0), PolicyReport CRD (`kubernetes-sigs/wg-policy-prototypes`).
