# audit-tenant

The language of `kubectl audit-tenant`, a read-only kubectl plugin that proves a Kubernetes namespace meets a tenant governance baseline. This file defines terms. It is not a spec.

## Language

### Who and where

**Tenant**:
A team that owns one or more namespaces in a cluster shared with other teams.
_Avoid_: Team (alone), customer, user

**Tenant identity**:
The Kubernetes user, groups and service account that the cluster sees when the tenant acts. Every check runs as this identity.
_Avoid_: Subject, principal, caller

**Identity mode**:
How the tool gets the tenant identity: *self mode* uses the current kubeconfig identity; *impersonation mode* uses `--as` and `--as-group`.
_Avoid_: Auth mode, user mode

**Target namespace**:
The namespace under audit.
_Avoid_: Tenant namespace, the namespace

**Other namespace**:
A namespace the tenant must not reach, used by cross-namespace checks. Default `kube-system`.
_Avoid_: Foreign namespace, victim namespace

### What is checked

**Baseline**:
The set of checks a namespace must pass to count as governed. v0.1 ships one fixed baseline of ten checks.
_Avoid_: Policy, profile, standard, ruleset

**Check**:
One named test with a stable ID that yields exactly one verdict for the target namespace.

**Check ID**:
The stable name of a check, `AT-01` to `AT-10`. AT stands for audit-tenant. Sub-checks add a dot and a number (`AT-04.2`).
_Avoid_: Check number, test ID, rule ID
_Avoid_: Rule, test, benchmark, control

**Sub-check**:
One of several probes inside a single check, with its own verdict and MTB ID, numbered `AT-nn.k`. Selecting the check selects all its sub-checks.
_Avoid_: Variant, case, step

**Behavioral check**:
A check that asks the cluster whether it would let the tenant identity do a forbidden thing, and passes when the cluster says no.
_Avoid_: Dynamic check, live test, attack

**Configuration check**:
A check that reads objects in or about the target namespace and compares them with the baseline.
_Avoid_: Static check, config scan

**Probe**:
A deliberately non-compliant object that a behavioral check submits with server-side dry-run. The API server never stores it; a proxy that drops `dryRun=All` can, and the tool then reports the object and sends no further create (D79, D101).
_Avoid_: Payload, attack pod, test resource

**Control probe**:
A compliant pod (AT-04) or ClusterIP Service (AT-05) that a behavioral check rehearses before its dangerous probes, to prove the cluster can accept that kind of object at all.
_Avoid_: Baseline pod, canary pod, sanity check

**Default-deny rule**:
A NetworkPolicy that selects every pod in the target namespace and opens no doors in a direction (ingress or egress).
_Avoid_: Deny-all, zero-trust policy, lockdown

**Rollout peak**:
The largest number of pods a workload can run at one moment during a rollout, and their summed resource requests.
_Avoid_: Surge, max replicas, burst

**Headroom**:
The difference between a quota's hard limits and the summed rollout peaks of the namespace's workloads. Negative headroom is a shortfall.
_Avoid_: Slack, buffer, margin

### Results

**Verdict**:
The outcome of one check: PASS, FAIL, SKIP or ERROR. SKIP means the tool chose not to decide. ERROR means the tool could not complete the check.
_Avoid_: Status, result, outcome, score

**Hint**:
The one-line advice attached to every FAIL, SKIP or ERROR that tells the reader what to change so the check passes or runs.
_Avoid_: Remediation, fix, suggestion

**Deciding layer**:
The part of the cluster that produced a verdict: RBAC (the permission list), admission (a gatekeeper such as Pod Security Admission or a policy engine), or configuration (an object the tool read).
_Avoid_: Source, enforcer, mechanism

**Reason**:
The one-line text that explains a verdict, kept as close as possible to the cluster's own message.
_Avoid_: Message, detail, explanation

**Report**:
The full output of one run: cluster, target namespace, tenant identity, time, verdicts, summary. Printed as a table, JSON, or a PolicyReport.
_Avoid_: Scan, audit, output

### Lineage

**MTB ID**:
The identifier of the matching benchmark in the archived Kubernetes Multi-Tenancy Benchmarks, such as `MTB-PL1-CC-CPI-1`. A check with no match shows `-`.
_Avoid_: Benchmark number, upstream ID
