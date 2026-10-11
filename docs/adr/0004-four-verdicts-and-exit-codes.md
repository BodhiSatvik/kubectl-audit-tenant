---
status: accepted
date: 2026-10-10
---
# Four verdicts and a fixed exit-code contract

Each check ends in exactly one verdict: PASS, FAIL, SKIP, or ERROR. SKIP means the tool chose not to decide (for example a webhook refuses dry-run, or a CRD is absent) and always carries a reason. ERROR means the tool could not complete the check (API timeout, unexpected server error) and also carries a reason. Exit codes: 1 if any FAIL; else 2 if any ERROR, or if the run could not start; else 0. SKIP never changes the exit code. CI pipelines script against these codes, so this contract is hard to change later. Every FAIL, SKIP and ERROR also carries a one-line hint on how to make it pass or how to make the check run.
