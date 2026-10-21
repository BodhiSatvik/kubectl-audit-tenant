# budgets.sh: the e2e budgets, read from CONSTRAINTS.md's Performance row, which the floor guard
# watches, so run.sh and bench.sh cannot drift from it (D163). Sourced from the repository root.
# Only the row under the Enforced heading counts, and there must be exactly one.
perf=$(awk '/^## /{on = ($0 ~ /^## Enforced with numbers/)} on && /^\| Performance \|/' CONSTRAINTS.md)
REQUEST_BUDGET=$(sed -nE 's/.*more than ([0-9]+) API requests.*/\1/p' <<<"$perf")
MEDIAN_BUDGET=$(sed -nE 's/.* on kind under ([0-9]+) s.*/\1/p' <<<"$perf")
[ "$(grep -c . <<<"$perf")" = 1 ] && [ -n "$REQUEST_BUDGET" ] && [ -n "$MEDIAN_BUDGET" ] || { echo "cannot read the e2e budgets: CONSTRAINTS.md needs exactly one Performance row under ## Enforced with numbers" >&2; exit 1; }
