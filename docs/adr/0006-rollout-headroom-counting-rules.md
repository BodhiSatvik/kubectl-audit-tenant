---
status: accepted
date: 2026-10-10
---
# Rollout headroom (AT-10) counting rules

AT-10 sums the rollout peak of every workload and compares it with the ResourceQuota `hard` values for `requests.cpu`, `requests.memory`, `limits.cpu`, `limits.memory`, and `pods`. Counted: Deployments, StatefulSets, and Argo Rollouts when the CRD exists. Not counted in v0.1: DaemonSets (their count follows machines, not the tenant), Jobs and CronJobs (their overlap follows the schedule), and bare Pods; the reason shows the quota's `used` values so the reader sees what else consumes it. Peak per workload: Deployment = replicas + maxSurge (absolute or percent rounded up, default 25%); Rollout blue/green = replicas + (previewReplicaCount, else replicas); Rollout canary = replicas + maxSurge; StatefulSet = replicas. Pod size follows the Kubernetes rule: max(sum of regular containers, largest init container); a container with no request uses the LimitRange `defaultRequest` if one exists, else zero with a note. All workloads are assumed to roll out at once (worst case, stated in the reason). No safety multiplier and no overhead constant: only public Kubernetes and Argo semantics, so a reader can reproduce the number by hand.
