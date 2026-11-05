#!/usr/bin/env bash
# Runs the README quick start as a new user would (D218, DESIGN.md acceptance criterion 9): the README's
# krew install line for the release, then the two bash blocks of its "Quick start" section, word for word.
# Needs Docker, kind, kubectl and kubectl-krew on PATH. Run it only on a clean machine (CI runs it on a fresh
# runner): it installs the plugin and makes the audit-demo cluster, and fails if either is already there.
set -euo pipefail
readme=${1:-README.md}
dir=$(mktemp -d)

install=$(grep -m1 '^kubectl krew install --manifest-url ' "$readme")
awk -v dir="$dir" '/^## Quick start/{q=1; next} q && /^## /{exit}
  q && /^```bash$/{n++; b=1; next} q && b && /^```$/{b=0; next} q && b{print > (dir "/block" n ".sh")}' "$readme"
[ -s "$dir/block1.sh" ] && [ -s "$dir/block2.sh" ] || { echo "quickstart: README has no two bash blocks under Quick start" >&2; exit 1; }

# run <name> <command...>: prints the output and records the exit code without stopping the script.
run() { local name=$1; shift; set +e; "$@" 2>&1 | tee "$dir/$name.out"; echo "${PIPESTATUS[0]}" > "$dir/$name.rc"; set -e; }
fail() { echo "quickstart: $*" >&2; exit 1; }
summary() { grep -E '^[0-9]+ passed ' "$dir/$1.out" | tail -1; } # the "N passed · M failed ..." line

start=$(date +%s)
bash -ec "$install"
export PATH="${KREW_ROOT:-$HOME/.krew}/bin:$PATH" # where krew's own install guide puts its plugins
run first bash -e "$dir/block1.sh"
run second bash -e "$dir/block2.sh"
seconds=$(( $(date +%s) - start ))

# What the README promises: every check runs; no fences fails 11 of 14; the restricted label passes AT-04 and AT-09.
[ "$(cat "$dir/first.rc")" = 1 ] || fail "the first run exited $(cat "$dir/first.rc"), the README shows exit 1"
case $(summary first) in "3 passed "*" 11 failed "*) ;; *) fail "the first run's summary is not '3 passed · 11 failed'" ;; esac
[ "$(cat "$dir/second.rc")" = 1 ] || fail "the second run exited $(cat "$dir/second.rc"), want 1 (five FAILs left)"
case $(summary second) in "9 passed "*" 5 failed "*) ;; *) fail "the second run's summary is not '9 passed · 5 failed'" ;; esac
for id in AT-04.1 AT-04.2 AT-04.3 AT-04.4 AT-04.5 AT-09; do
  grep -Eq "^PASS +$id " "$dir/second.out" || fail "$id does not PASS after the label"
done
echo "quickstart: the README quick start took ${seconds}s, from the plugin install to the second run"
[ "$seconds" -le 300 ] || fail "it took more than five minutes"
kind delete cluster --name audit-demo
