---
status: accepted
date: 2026-10-10
---
# Probes state a size, and a harmless control probe runs first

A dry-run create passes through every admission gate, not only the security ones. A ResourceQuota that caps `requests.cpu` rejects any pod with no stated request, and that rejection would look like a PASS for AT-04 and AT-05. So every probe states a tiny request and limit (1m CPU, 1Mi memory), and before the dangerous probes the tool dry-runs one compliant control pod. If the control pod is rejected, AT-04 and AT-05 are SKIP with the cluster's reason, because the test cannot prove anything. The rejection text decides the layer: "violates PodSecurity" is admission (PSA), "admission webhook ... denied" is admission (policy engine), a 403 with no admission text is RBAC, and quota or LimitRange text is inconclusive, so SKIP. The alternative, treating any rejection as PASS (what kubectl-mtb did), produces false passes.
