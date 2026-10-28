---
status: accepted
date: 2026-10-10
---
# Four verdicts and a fixed exit-code contract

Each check ends in exactly one verdict: PASS, FAIL, SKIP, or ERROR. SKIP means the tool chose not to decide (for example a webhook refuses dry-run, or a CRD is absent) and always carries a reason. ERROR means the tool could not complete the check (API timeout, unexpected server error) and also carries a reason. Exit codes: 1 if any FAIL; else 2 if any ERROR, or if the run could not start; else 0. SKIP never changes the exit code by default; the opt-in `--fail-on-skip` flag (CEO review, 2026-10-11) makes any SKIP exit 1, for pipelines that must not accept an unproven namespace. CI pipelines script against these codes, so this contract is hard to change later. Every FAIL, SKIP and ERROR also carries a one-line hint on how to make it pass or how to make the check run.

Amended 2026-10-28 (D197, before the krew-index listing): a run where every result is SKIP exits 2, not 0, because it proved nothing. A misspelled `-n`, `--as` or `--as-group` gives exactly that run, and exit 0 passed it in a pipeline. `--fail-on-skip` still makes it exit 1, which wins over 2 as before. A run with at least one PASS keeps the old rule: its SKIPs do not change the exit code. The rule also covers a `--checks` or `--skip` subset whose results are all SKIP (D201): that run proved nothing either, so stderr points at the SKIP reasons, not only at a typo.
