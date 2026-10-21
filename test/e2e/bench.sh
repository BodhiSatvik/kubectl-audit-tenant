#!/usr/bin/env bash
# bench.sh: the benchmark (make bench, DESIGN.md section 7). On the kind cluster that make e2e leaves,
# it times five consecutive full runs as run 1's identity, counts each run's API requests and writes
# from its --debug trace, and appends the rows to BENCHMARK.md. It fails when the median wall time is
# 10 s or more, a run makes more than 40 API requests, or a run sends a write (CONSTRAINTS.md).
#
# Environment: E2E_CLUSTER (default audit-tenant-e2e); BENCH_VERSION labels the rows (the commit is always added).
# Run outputs land in bin/ (git-ignored).
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=test/e2e/versions.env
source test/e2e/versions.env
cluster="${E2E_CLUSTER:-audit-tenant-e2e}"
mkdir -p bin
export KUBECONFIG="$PWD/bin/e2e-kubeconfig"
bin=bin/kubectl-audit_tenant
fail() { echo "bench: FAIL: $*" >&2; exit 1; }
now() { perl -MTime::HiRes=time -e 'printf "%.3f", time'; }

kind get clusters 2>/dev/null | grep -qx "$cluster" || fail "no kind cluster $cluster; run make e2e first"
kind export kubeconfig --name "$cluster" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1 || fail "cannot export the kubeconfig of $cluster"
go build -o "$bin" ./cmd/kubectl-audit_tenant
args=(-n tenant-a --as system:serviceaccount:tenant-a:tenant-a-dev --debug)

times=() requests=() writes=()
for i in 1 2 3 4 5; do
  start=$(now)
  "$bin" "${args[@]}" >"bin/bench-$i.out" 2>"bin/bench-$i.err" || fail "run $i exited $?, want 0; stderr in bin/bench-$i.err"
  end=$(now)
  t=$(echo "$end - $start" | bc)
  r=$(grep -cE '^[A-Z]+ /' "bin/bench-$i.err" || true)
  # A write is a POST, PUT, PATCH or DELETE without dryRun=All; SelfSubject*Review POSTs persist nothing (section 7).
  w=$(grep -E '^(POST|PUT|PATCH|DELETE) /' "bin/bench-$i.err" | grep -v 'dryRun=All' | grep -vc '/selfsubject' || true)
  times+=("$t") requests+=("$r") writes+=("$w")
  printf 'run %d: %.2f s, %d requests, %d writes\n' "$i" "$t" "$r" "$w"
done
median=$(printf '%s\n' "${times[@]}" | sort -n | sed -n 3p)
max=$(printf '%s\n' "${times[@]}" | sort -n | tail -1)
fewest=$(printf '%s\n' "${requests[@]}" | sort -n | head -1)
most=$(printf '%s\n' "${requests[@]}" | sort -n | tail -1)
wrote=$(printf '%s\n' "${writes[@]}" | sort -n | tail -1)

if [ "${GITHUB_ACTIONS:-}" = true ]; then host="GitHub Actions ${ImageOS:-runner} ${RUNNER_ARCH:-}"; else host="$(uname -sm)"; fi
docker=$(docker info --format '{{.NCPU}} {{.MemTotal}}' | awk '{printf "%d CPU / %.0f GiB", $1, $2 / 2^30}')
node=${KIND_NODE_IMAGE%%@*}
setup="kind ${node##*:} (one node) in Docker ($docker) on $host; Kyverno $KYVERNO_APP_VERSION admission controller"
version="${BENCH_VERSION:+$BENCH_VERSION, }commit $(git describe --tags --always --dirty)"
command="\`make bench\` (test/e2e/bench.sh, after \`make e2e\`): \`kubectl audit-tenant ${args[*]}\`"
spread="$fewest"; [ "$fewest" = "$most" ] || spread="$fewest to $most"
{
  printf '| %s | %s | %s | wall time of a full ten-check run (14 results), 5 consecutive runs | - | median %.2f s, max %.2f s (budget: median under 10 s) | %s |\n' \
    "$(date -u +%F)" "$version" "$setup" "$median" "$max" "$command"
  printf '| %s | %s | %s | API requests per run (--debug trace); writes per run (POST, PUT, PATCH or DELETE without dryRun=All) | - | %s requests per run, %s writes (budget: at most 40 requests, 0 writes) | %s |\n' \
    "$(date -u +%F)" "$version" "$setup" "$spread" "$wrote" "$command"
} >>BENCHMARK.md
printf 'bench: median %.2f s, max %.2f s; %s requests and %s writes per run; rows appended to BENCHMARK.md\n' "$median" "$max" "$spread" "$wrote"

awk -v m="$median" 'BEGIN { exit !(m < 10) }' || fail "median wall time $median s is not under 10 s (CONSTRAINTS.md)"
[ "$fewest" -gt 0 ] || fail "a run has no API requests in its --debug trace, so the budget cannot be checked"
[ "$most" -le 40 ] || fail "a run made $most API requests, over the budget of 40 (CONSTRAINTS.md)"
[ "$wrote" -eq 0 ] || fail "a run sent $wrote writes; the tool must send none (DESIGN.md section 7)"
