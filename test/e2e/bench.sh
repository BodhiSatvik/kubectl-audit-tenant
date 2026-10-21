#!/usr/bin/env bash
# bench.sh: the benchmark (make bench, DESIGN.md section 7). On the kind cluster that make e2e leaves,
# it times five consecutive full runs as run 1's identity, checks each run's verdicts against run 1's
# expected verdicts, and counts each run's API requests and writes from its --debug trace. It fails when
# the median wall time reaches the budget, a run makes more API requests than the budget or sends a
# write (CONSTRAINTS.md's Performance row, read by budgets.sh); only when every gate passes does it append the rows to BENCHMARK.md.
#
# Usage: test/e2e/bench.sh [--self-test]
# Environment: E2E_CLUSTER (default audit-tenant-e2e); BENCH_VERSION labels the rows (the commit is always added).
# Run outputs land in bin/ (git-ignored).
set -euo pipefail
cd "$(dirname "$0")/../.."

# requests_in and writes_in read a --debug trace. A write is a POST, PUT, PATCH or DELETE whose query
# lacks dryRun=All; the two SelfSubject*Review POSTs persist nothing (DESIGN.md section 7).
requests_in() { grep -cE '^[A-Z]+ /' "$1" || true; }
writes_in() {
  grep -E '^(POST|PUT|PATCH|DELETE) /' "$1" |
    grep -vE '^[A-Z]+ /[^ ?]*\?([^ ]*&)?dryRun=All(&[^ ]*)? ' |
    grep -vcE '^POST /apis/(authorization\.k8s\.io/v1/selfsubjectaccessreviews|authentication\.k8s\.io/v1/selfsubjectreviews) ' || true
}

if [ "${1:-}" = --self-test ]; then
  f=$(mktemp)
  trap 'rm -f "$f"' EXIT
  printf '%s\n' 'identity: context kind, kubeconfig user u, no impersonation' 'GET /version 200 1ms' \
    'POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews 201 1ms' '  {"kind":"SelfSubjectAccessReview"}' \
    'POST /api/v1/namespaces/tenant-a/pods?dryRun=All 201 1ms' 'POST /api/v1/namespaces/tenant-a/pods?notdryRun=All 201 1ms' \
    'PUT /api/v1/namespaces/tenant-a/pods/x 200 1ms' 'DELETE /api/v1/namespaces/tenant-a/configmaps/selfsubject-cache 200 1ms' \
    'PATCH /api/v1/namespaces/tenant-a/pods/x 200 1ms' 'POST /api/v1/namespaces/tenant-a/pods?dryRun=Allx 201 1ms' \
    'POST /api/v1/namespaces/tenant-a/services?fieldManager=a&dryRun=All 201 1ms' >"$f"
  r=$(requests_in "$f") w=$(writes_in "$f")
  [ "$r" = 9 ] && [ "$w" = 5 ] || { echo "bench self-test: FAIL, $r requests and $w writes, want 9 and 5" >&2; exit 1; }
  echo "bench self-test: ok"
  exit 0
fi

# shellcheck source=test/e2e/versions.env
source test/e2e/versions.env
# shellcheck source=test/e2e/budgets.sh
source test/e2e/budgets.sh
cluster="${E2E_CLUSTER:-audit-tenant-e2e}"
mkdir -p bin
export KUBECONFIG="$PWD/bin/e2e-kubeconfig"
bin=bin/kubectl-audit_tenant
fail() { echo "bench: FAIL: $*" >&2; exit 1; }

kind get clusters 2>/dev/null | grep -qx "$cluster" || fail "no kind cluster $cluster; run make e2e first"
kind export kubeconfig --name "$cluster" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1 || fail "cannot export the kubeconfig of $cluster"
go build -o "$bin" ./cmd/kubectl-audit_tenant
args=(-n tenant-a --as system:serviceaccount:tenant-a:tenant-a-dev --debug)

TIMEFORMAT=%3R # the time keyword prints wall seconds only; no extra process inside the timed span
runs=5
results=$(wc -l <test/e2e/expected/run1.txt | tr -d ' ')
times=() requests=() writes=()
for i in $(seq "$runs"); do
  t=$( { time "$bin" "${args[@]}" >"bin/bench-$i.out" 2>"bin/bench-$i.err"; } 2>&1) || fail "run $i exited non-zero, want 0; stderr in bin/bench-$i.err"
  # A run that skipped checks would be faster and send fewer requests; only run 1's expected verdicts count as a full run.
  awk '/^(PASS|FAIL|SKIP|ERROR) /{print $1, $2}' "bin/bench-$i.out" | diff -q test/e2e/expected/run1.txt - >/dev/null ||
    fail "run $i: verdicts differ from test/e2e/expected/run1.txt (output in bin/bench-$i.out)"
  r=$(requests_in "bin/bench-$i.err") w=$(writes_in "bin/bench-$i.err")
  times+=("$t") requests+=("$r") writes+=("$w")
  printf 'run %d: %s s, %d requests, %d writes\n' "$i" "$t" "$r" "$w"
done
median=$(printf '%s\n' "${times[@]}" | sort -n | sed -n "$(((runs + 1) / 2))p")
max=$(printf '%s\n' "${times[@]}" | sort -n | tail -1)
fewest=$(printf '%s\n' "${requests[@]}" | sort -n | head -1)
most=$(printf '%s\n' "${requests[@]}" | sort -n | tail -1)
wrote=$(printf '%s\n' "${writes[@]}" | sort -n | tail -1)

awk -v m="$median" -v b="$MEDIAN_BUDGET" 'BEGIN { exit !(m < b) }' || fail "median wall time $median s is not under $MEDIAN_BUDGET s (CONSTRAINTS.md)"
[ "$fewest" -gt 0 ] || fail "a run has no API requests in its --debug trace, so the budget cannot be checked"
[ "$most" -le "$REQUEST_BUDGET" ] || fail "a run made $most API requests, over the budget of $REQUEST_BUDGET (CONSTRAINTS.md)"
[ "$wrote" -eq 0 ] || fail "a run sent $wrote writes; the tool must send none (DESIGN.md section 7)"

if [ "${GITHUB_ACTIONS:-}" = true ]; then host="GitHub Actions ${ImageOS:-runner} ${RUNNER_ARCH:-}"; else host="$(uname -sm)"; fi
docker=$(docker info --format '{{.NCPU}} {{.MemTotal}}' | awk '{printf "%d CPU / %.0f GiB", $1, $2 / 2^30}')
# What the cluster runs, not what versions.env asks for: run.sh reuses an existing cluster as it is.
server=$(kubectl version -o json | jq -r .serverVersion.gitVersion) || fail "cannot read the cluster's version"
nodes=$(kubectl get nodes --no-headers | wc -l | tr -d ' ')
kyverno=$(kubectl -n kyverno get deployment -l app.kubernetes.io/component=admission-controller -o jsonpath='{.items[0].spec.template.spec.containers[0].image}') || fail "cannot read the Kyverno admission controller's image"
setup="kind $server ($nodes node(s)) in Docker ($docker) on $host; Kyverno admission controller ${kyverno##*:}"
version="${BENCH_VERSION:+$BENCH_VERSION, }commit $(git describe --tags --always)"
[ -z "$(git status --porcelain -- . ':!BENCHMARK.md')" ] || version+="-dirty" # untracked source counts (go build compiles it); rows appended earlier do not
command="\`make bench\` (test/e2e/bench.sh, after \`make e2e\`): \`kubectl audit-tenant ${args[*]}\`"
spread="$fewest"
[ "$fewest" = "$most" ] || spread="$fewest to $most"
{
  printf '| %s | %s | %s | wall time of a full run of every check (%s results), %s consecutive runs | - | median %.2f s, max %.2f s (budget: median under %s s) | %s |\n' \
    "$(date -u +%F)" "$version" "$setup" "$results" "$runs" "$median" "$max" "$MEDIAN_BUDGET" "$command"
  printf '| %s | %s | %s | API requests per run (--debug trace); writes per run (POST, PUT, PATCH or DELETE without dryRun=All) | - | %s requests per run, %s writes (budget: at most %s requests, 0 writes) | %s |\n' \
    "$(date -u +%F)" "$version" "$setup" "$spread" "$wrote" "$REQUEST_BUDGET" "$command"
} >>BENCHMARK.md
printf 'bench: median %.2f s, max %.2f s; %s requests and %s writes per run; rows appended to BENCHMARK.md\n' "$median" "$max" "$spread" "$wrote"
