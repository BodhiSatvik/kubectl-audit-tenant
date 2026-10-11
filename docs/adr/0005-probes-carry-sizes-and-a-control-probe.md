---
status: accepted
date: 2026-10-10
---
# Probes state a size, and a harmless control probe runs first

A dry-run create passes through every admission gate, not only the security ones. A ResourceQuota that caps `requests.cpu` rejects any pod with no stated request, and that rejection would look like a PASS for AT-04 and AT-05. So every probe states a request and limit the namespace accepts (see the sizing rule below), and before the dangerous probes the tool dry-runs one compliant control pod. If the control pod is rejected, AT-04 and AT-05 are SKIP with the cluster's reason, because the test cannot prove anything. The rejection text decides the layer: "violates PodSecurity" is admission (PSA), "admission webhook ... denied" is admission (policy engine), a 403 with no admission text is RBAC, and quota or LimitRange text is inconclusive, so SKIP. The alternative, treating any rejection as PASS (what kubectl-mtb did), produces false passes.

Sizing rule (CEO review, 2026-10-10): a LimitRange `min` above 1m CPU or 1Mi memory rejects even the control pod, which would turn AT-04 and AT-05 into SKIP on exactly the governed namespaces the tool is for. So when a Container LimitRange with `defaultRequest` and `default` for cpu and memory exists, probes carry no sizes and the server applies the namespace's own defaults; when only a `min` exists, probes use it; otherwise, or when limitranges cannot be listed, 1m and 1Mi.

Controller bypass (CEO review, 2026-10-10): when RBAC denies `create pods` but allows creating a pod controller (Deployment, ReplicaSet, StatefulSet, DaemonSet, Job, CronJob), the controller's pods are not subject to the tenant's RBAC and only admission can stop them. Admission was not exercised, so AT-04 reports SKIP with the allowed kind named, not PASS.

Valid objects (outside review, 2026-10-11): a probe must pass API validation before admission sees it. `privileged: true` with `allowPrivilegeEscalation: false` is rejected at validation, so the AT-04.1 probe also sets `allowPrivilegeEscalation: true`; otherwise a permissive namespace would show ERROR instead of FAIL.

Services too (outside review, 2026-10-11): AT-05 rehearses a compliant ClusterIP Service before the NodePort probe. A policy that rejects Services for an unrelated reason (a required label, a naming rule) would otherwise reject the probe and produce a false PASS; when the control Service is rejected, AT-05 is SKIP with the real reason.
