#!/usr/bin/env bash
# coverage-gate.sh: per-package statement-coverage floors from CONSTRAINTS.md.
# Usage: go test ./... -coverprofile=coverage.out -covermode=atomic 2>&1 | tee test.log; hack/coverage-gate.sh test.log
# Floors: 80% for every package under internal/, 90% for internal/safety and internal/probe. cmd/ and test/ are wiring and excluded.
set -euo pipefail
log="${1:?usage: coverage-gate.sh <go test output file>}"
fail=0
while IFS= read -r line; do
  pkg=$(awk '{print $2}' <<<"$line")
  case "$pkg" in */internal/*) ;; *) continue ;; esac
  if grep -q '\[no test files\]' <<<"$line"; then pct=0
  else pct=$(sed -nE 's/.*coverage: ([0-9]+(\.[0-9]+)?)% of statements.*/\1/p' <<<"$line"); fi
  [ -z "${pct:-}" ] && continue
  floor=80
  case "$pkg" in */internal/safety*|*/internal/probe*) floor=90 ;; esac
  if awk -v p="$pct" -v f="$floor" 'BEGIN{exit !(p+0 < f)}'; then
    echo "coverage-gate: $pkg has ${pct}% statement coverage; floor is ${floor}% (CONSTRAINTS.md)"; fail=1
  fi
done < <(grep -E '^(ok|\?|FAIL)[[:space:]]' "$log" || true)
if [ "$fail" -ne 0 ]; then echo "coverage-gate: FAIL. Add tests for the branches the report shows uncovered; do not lower the floor."; exit 1; fi
echo "coverage-gate: every internal/ package meets its floor"
