---
status: accepted
date: 2026-10-10
---
# Cite the Kubernetes Multi-Tenancy Benchmarks; do not fork kubectl-mtb

`kubectl-mtb` (Apache-2.0, archived 2023, last release December 2021) is the lineage of this tool. We reuse its benchmark IDs (such as `MTB-PL1-CC-CPI-1`) as a public vocabulary and cite it in the README, but we write new code. Reasons: its checks predate Pod Security Admission, its dependencies are four years old, and a fork would carry its archived status and bugs (two benchmarks share the ID `MTB-PL1-BC-HI-1`). A check with no MTB match shows `-`. Checks are mapped to MTB by title, not by the duplicated ID.
