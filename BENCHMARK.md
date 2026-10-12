# audit-tenant: benchmarks

All numbers on this page come from runs of this repo's code. Each result lists the date, version, hardware or providers, and the command to repeat it.

| Date | Version | Setup | Metric | Baseline | audit-tenant | Command |
|---|---|---|---|---|---|---|
| 2026-10-11 | phase-1-skeleton (commit c9737c4, one check: AT-01) | kind v1.37.0 (one node) on colima 4 CPU / 8 GiB, macOS 14 arm64; Kyverno 1.19.1 admission controller | wall time of one run (`--as` service account, run 1); API requests per run | - | 0.74 s first run (TLS setup), 0.04 s warm; 7 requests | `make e2e` (test/e2e/run.sh prints both per run) |
