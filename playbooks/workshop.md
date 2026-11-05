# Playbook: a 60-minute hands-on workshop

For a meetup host, a teacher, or a study group. Each person gets a shared cluster that two teams set up in a hurry. Six fences are missing or wrong. Attendees find them with `kubectl audit-tenant`, fix them with kubectl, and prove the fix. They need no Kubernetes security background: each FAIL says what to change.

| | |
|---|---|
| Time | 60 minutes, plus a setup check before the session |
| Each attendee needs | a laptop with Docker, kind, kubectl and the plugin ([setup](setup.md)); bash or zsh (on Windows, WSL2) |
| Changes | one new kind cluster per laptop, `audit-lab`; the last step deletes it |
| Tested | v0.1.2 on kind v0.33.0 (Kubernetes v1.37.0), 2026-11-05 |

## Before the session (facilitator)

- Send attendees [setup.md](setup.md) a few days ahead. Ask each one to run this once, on good Wi-Fi: the kind node image is about 1.3 GB.

  ```bash
  kind create cluster --name warmup && kind delete cluster --name warmup
  kubectl audit-tenant version
  ```

- Plan for 2 to 3 people who could not install. Pair them with someone whose setup works.
- Ask attendees to delete other kind clusters before the session (`kind get clusters`): each one uses memory and Linux inotify slots, and a full machine makes `kind create cluster` fail ([setup.md](setup.md#if-kind-create-cluster-fails)).
- Do the lab yourself once, start to finish (about 20 minutes).

| Time | Part |
|---|---|
| 0:00 | Why: one cluster, many teams. How do you prove that team A cannot reach team B? (5 min) |
| 0:05 | Steps 1 and 2: the cluster and the first audits (10 min) |
| 0:15 | Read the output together: PASS, FAIL, SKIP, the layer, the `fix` and `note` lines (10 min) |
| 0:25 | Step 3: fix the six problems, in pairs (20 min) |
| 0:45 | Step 4: prove the fixes; then discuss what a PASS does not prove (10 min) |
| 0:55 | Step 5: clean up, and questions (5 min) |

**With an AI agent** (for self-study), give it this prompt:

```text
Guide me through this workshop on this machine, in a new empty folder:
https://github.com/BodhiSatvik/kubectl-audit-tenant/blob/main/playbooks/workshop.md
Use only the kind cluster that the workshop creates (context kind-audit-lab). Run steps 1 and 2, then
stop. Do not show me the answers or the lab file. When I propose a fix, run it, re-run the audit and
tell me what changed. Do not star the repository, open issues or post anything for me.
At the end, run the clean-up step.
```

## Step 1. Your cluster

```bash
kind create cluster --name audit-lab
kubectl config current-context
kubectl --context kind-audit-lab apply -f https://raw.githubusercontent.com/BodhiSatvik/kubectl-audit-tenant/main/playbooks/workshop-lab.yaml
```

Expect `kind-audit-lab`, and a list of created objects. The lab file adds weak RBAC on purpose, so the command names the kind context: if the cluster was not created, it fails instead of touching another cluster. Do not read the lab file yet: the audit should tell you what is wrong.

The cluster has two teams. The members of `team-a-devs` (such as `alice`) work in the namespace `team-a`; the members of `team-b-devs` (such as `bob`) work in `team-b`. You are the cluster admin. You test each team as that team with `--as` and `--as-group`.

## Step 2. Audit both teams

```bash
for i in $(seq 30); do kubectl auth can-i list pods -n team-b --as bob --as-group team-b-devs >/dev/null && break; sleep 1; done
kubectl audit-tenant -n team-a --as alice --as-group team-a-devs
kubectl audit-tenant -n team-b --as bob --as-group team-b-devs
kubectl audit-tenant -n team-a --as alice --as-group team-a-devs --checks AT-02 --other-namespace team-b
kubectl audit-tenant -n team-b --as bob --as-group team-b-devs --checks AT-02 --other-namespace team-a
```

Expect:

- team-a: `7 passed · 7 failed`
- team-b: `11 passed · 3 failed (AT-01, AT-07, AT-08)`
- team-a reading team-b's Secrets: one FAIL
- team-b reading team-a's Secrets: one PASS

How to read a line:

```text
FAIL  AT-01    No cluster-scoped access            RBAC allows create persistentvolumes; granted by ClusterRoleBinding "team-b-storage-helper" of ClusterRole "storage-hel…
      fix      remove or change ClusterRoleBinding "team-b-storage-helper" of ClusterRole "storage-helper" to Group "team-b-devs" ...
```

- **PASS**: the fence held. The reason names the layer that decided: RBAC, admission (such as Pod Security), or config (an object the tool read).
- **FAIL**: the cluster accepted a dangerous request in a dry run, or granted the access, or a required object is missing. The `fix` line says what to change.
- **SKIP**: the tool could not test it, and says why. A SKIP is never a PASS.
- **note**: what that check did not test.

Every pod or Service in a FAIL was a server-side dry run: the API server ran all of its checks and stored nothing.

## Step 3. Fix the six problems

Work in pairs. Use the `fix` lines, and re-run the audit after each change. The problems:

1. team-a may run privileged pods, and pods that use the node's disk, processes and network. (Five AT-04 results, and AT-09.)
2. team-a may open a port on every node. (AT-05)
3. team-b may create PersistentVolumes, which can point at a node's disk. (AT-01)
4. team-b's containers get no default CPU and memory. (AT-07)
5. team-b's pods may connect anywhere. (AT-08)
6. team-a may read team-b's Secrets. (AT-02 with `--other-namespace team-b`)

Questions to discuss while you work:

- Why does a missing Pod Security label fail five different checks?
- Problem 6 is a RoleBinding inside team-b. Who could have made it, and why might it have looked harmless?
- Which fixes would be risky on a namespace that already runs apps? (Hint: read the AT-09 fix line.)

<details>
<summary>Answers (open after you try)</summary>

```bash
# 1. Pod Security: first ask what "restricted" would refuse among running pods, then set it
kubectl label --dry-run=server --overwrite namespace team-a pod-security.kubernetes.io/enforce=restricted
kubectl label --overwrite namespace team-a pod-security.kubernetes.io/enforce=restricted
# 2. NodePort Services: a quota of zero node ports, no policy engine needed
kubectl patch resourcequota compute -n team-a --type merge -p '{"spec":{"hard":{"services.nodeports":"0"}}}'
# 3. The cluster-wide grant to team-b
kubectl --context kind-audit-lab delete clusterrolebinding team-b-storage-helper
# 4 and 5. Defaults for containers, and a default deny for egress
kubectl apply -n team-b -f - <<'EOF'
apiVersion: v1
kind: LimitRange
metadata: {name: container-defaults}
spec:
  limits:
  - {type: Container, default: {cpu: 500m, memory: 512Mi}, defaultRequest: {cpu: 100m, memory: 128Mi}}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-egress}
spec: {podSelector: {}, policyTypes: [Egress]}
EOF
# 6. The cross-team grant
kubectl --context kind-audit-lab delete rolebinding team-a-migration-help -n team-b
```

</details>

## Step 4. Prove the fixes

Run the four audits from step 2 again. Expect `14 passed · 0 failed` for each team, and a PASS for each cross-team check.

Then discuss what a PASS does not prove. Each `note` line names a limit; the README lists them all in [What a PASS does not prove](../README.md#what-a-pass-does-not-prove). Two good examples: AT-08 reads the NetworkPolicy objects but cannot tell whether the network plugin enforces them, and a grant to one named Secret is not visible to an access review.

## Step 5. Clean up

```bash
kind delete cluster --name audit-lab
```

kind removes the cluster's context, so kubectl now has no current context. Go back to your own cluster with `kubectl config use-context <your context>`.

## After the workshop

- To try it on a real cluster safely, read [real-cluster.md](real-cluster.md).
- To see the tool prove from the API server's audit log that it wrote nothing, do part 6 of [security-review.md](security-review.md).
- Something confusing, wrong or missing? Please open an issue from your own GitHub account, with what you ran and what you saw: [Tell us](README.md#tell-us).
