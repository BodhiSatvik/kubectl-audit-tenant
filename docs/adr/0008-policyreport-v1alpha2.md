---
status: accepted
date: 2026-10-10
---
# PolicyReport output uses wgpolicyk8s.io/v1alpha2

The working-group repo also publishes v1beta1, but Kyverno writes and Policy Reporter reads v1alpha2 today (checked 2026-10-10), and kubectl-mtb wrote v1alpha2. We emit v1alpha2 so the report shows up in dashboards people already run. Revisit when Policy Reporter moves. The result field uses the enum pass, fail, skip, error, which maps one-to-one onto our verdicts.
