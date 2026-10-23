---
status: accepted
date: 2026-10-10
---
# PolicyReport output uses wgpolicyk8s.io/v1alpha2

The working-group repo also publishes v1beta1, but Kyverno writes and Policy Reporter reads v1alpha2 today (checked 2026-10-10), and kubectl-mtb wrote v1alpha2. We emit v1alpha2 so the report shows up in dashboards people already run. Revisit when Policy Reporter moves. The result field uses the enum pass, fail, skip, error, which maps one-to-one onto our verdicts.

## Checked again on 2026-10-23 (D172)

The OpenReports API (`openreports.io/v1alpha1`, kinds `Report` and `ClusterReport`) is meant to replace `wgpolicyk8s.io`. Kyverno 1.15 to 1.19.1 still writes `wgpolicyk8s.io/v1alpha2` by default; OpenReports is an opt-in alpha there (Helm value `openreports.enabled=true`). SUSE's admission controller writes OpenReports by default since its 1.33. v0.1.0 keeps `wgpolicyk8s.io/v1alpha2`. When Kyverno makes OpenReports its default, add it as a new `-o` value: a new format breaks no user of this one.
