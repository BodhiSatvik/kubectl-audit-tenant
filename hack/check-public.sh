#!/usr/bin/env bash
# check-public.sh: fail when a tracked text file still holds an unfilled
# template placeholder: an upper-case name in double braces, like the template's PAPER_DOI.
# Usage: hack/check-public.sh            scan every git-tracked file
#        hack/check-public.sh FILE...    scan the given files
#        hack/check-public.sh --self-test
set -euo pipefail
pattern='\{\{[A-Z][A-Z0-9_]*\}\}'
if [ "${1:-}" = "--self-test" ]; then
  tmp=$(mktemp); printf 'left {{%s}} over\n' PLANTED > "$tmp"
  if "$0" "$tmp" >/dev/null; then echo "self-test: FAIL, a planted placeholder passed"; rm -f "$tmp"; exit 1; fi
  rm -f "$tmp"; echo "self-test: ok"; exit 0
fi
if [ $# -eq 0 ]; then
  hits=$(git ls-files -z | xargs -0 grep -nIE "$pattern" -- 2>/dev/null || true)
else
  hits=$(grep -nIE "$pattern" -- "$@" || true)
fi
if [ -n "$hits" ]; then
  echo "check-public: unfilled placeholders (fill them before this tree goes public):"
  echo "$hits"
  exit 1
fi
echo "check-public: no unfilled placeholders"
