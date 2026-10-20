#!/usr/bin/env bash
# run.sh: the kind end-to-end test (make e2e). It creates or reuses a kind cluster, installs
# Kyverno with the restrict-nodeport policy in Enforce mode, applies the tenant-a and tenant-b
# fixtures, builds the plugin and runs it as the four identities of DESIGN.md section 8.
# It asserts the verdict of all fourteen results (test/e2e/expected/run<N>.txt), the deciding
# layer or reason of every check, the exit codes, the header lines, the notes that change a
# reader's trust (D86, D105) and a fix line under every non-PASS.
#
# Environment: E2E_CLUSTER (default audit-tenant-e2e); E2E_DELETE=1 deletes the cluster at the end.
# Outputs land in bin/ (git-ignored). The user's own kubeconfig is never changed.
set -euo pipefail
cd "$(dirname "$0")/../.."
# shellcheck source=test/e2e/versions.env
source test/e2e/versions.env
cluster="${E2E_CLUSTER:-audit-tenant-e2e}"
mkdir -p bin
export KUBECONFIG="$PWD/bin/e2e-kubeconfig"
bin=bin/kubectl-audit_tenant

say() { printf '\n== %s\n' "$*"; }
fail() { echo "e2e: FAIL: $*" >&2; exit 1; }
now() { perl -MTime::HiRes=time -e 'printf "%.3f", time'; }

say "kind cluster $cluster"
if ! kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  kind create cluster --name "$cluster" --config test/e2e/kind.yaml --image "$KIND_NODE_IMAGE" --wait 120s --kubeconfig "$KUBECONFIG"
else
  kind export kubeconfig --name "$cluster" --kubeconfig "$KUBECONFIG"
fi
kubectl version >/dev/null

say "Kyverno chart $KYVERNO_CHART_VERSION (app $KYVERNO_APP_VERSION), admission controller only"
helm upgrade --install kyverno kyverno --repo https://kyverno.github.io/kyverno/ --version "$KYVERNO_CHART_VERSION" \
  --namespace kyverno --create-namespace --wait --timeout 5m \
  --set backgroundController.enabled=false --set cleanupController.enabled=false --set reportsController.enabled=false >/dev/null
kubectl -n kyverno wait deployment -l app.kubernetes.io/component=admission-controller --for=condition=Available --timeout=180s

say "restrict-nodeport policy from kyverno/policies@${KYVERNO_POLICIES_COMMIT:0:7}, switched to Enforce"
curl -sfL "https://raw.githubusercontent.com/kyverno/policies/$KYVERNO_POLICIES_COMMIT/best-practices/restrict-node-port/restrict-node-port.yaml" \
  | sed 's/validationFailureAction: Audit/validationFailureAction: Enforce/' | kubectl apply -f -

say "fixtures"
kubectl apply -f test/e2e/fixtures/tenant-a.yaml
kubectl apply -f test/e2e/fixtures/tenant-b.yaml

say "waiting until the webhook rejects a dry-run NodePort Service"
rejected=""
for _ in $(seq 1 60); do
  if out=$(kubectl create -f test/e2e/fixtures/nodeport-service.yaml -n tenant-a --dry-run=server 2>&1); then sleep 2; continue; fi
  if grep -q 'restrict-nodeport' <<<"$out"; then rejected=1; break; fi # the policy itself, not any webhook error
  sleep 2
done
[ -n "$rejected" ] || fail "Kyverno did not reject the NodePort dry-run: ${out:-no output}"
echo "$out"

say "build"
go build -o "$bin" ./cmd/kubectl-audit_tenant

# run NAME WANT_EXIT ARGS...: runs the plugin with --debug, prints its report, checks the exit code
# and the request budget (CONSTRAINTS.md: no e2e run makes more than 40 API requests).
run() {
  local name=$1 want=$2 code start end requests; shift 2
  say "run $name: kubectl audit-tenant $*"
  start=$(now)
  set +e; "$bin" "$@" --debug >"bin/e2e-$name.out" 2>"bin/e2e-$name.err"; code=$?; set -e
  end=$(now)
  cat "bin/e2e-$name.out"
  requests=$(grep -c '^[A-Z]* /' "bin/e2e-$name.err" || true)
  printf 'exit %d; %s requests; %.2f s wall\n' "$code" "$requests" "$(echo "$end - $start" | bc)"
  [ "$code" = "$want" ] || fail "run $name exited $code, want $want; stderr: $(cat "bin/e2e-$name.err")"
  [ "$requests" -le 40 ] || fail "run $name made $requests API requests, over the budget of 40 (CONSTRAINTS.md)"
}
expect() { grep -qE "$2" "bin/e2e-$1.out" || fail "run $1: expected /$2/ in the output above"; }
absent() { ! grep -qE "$2" "bin/e2e-$1.out" || fail "run $1: /$2/ must not appear in the output above"; }
# count N REGEX WANT: exactly WANT lines of run N match REGEX.
count() { local got; got=$(grep -cE "$2" "bin/e2e-$1.out" || true); [ "$got" = "$3" ] || fail "run $1: $got lines match /$2/, want $3"; }
# verdicts N: the "VERDICT ID" pairs of run N are exactly test/e2e/expected/runN.txt, in order.
verdicts() { awk '/^(PASS|FAIL|SKIP|ERROR) /{print $1, $2}' "bin/e2e-$1.out" | diff -u "test/e2e/expected/run$1.txt" - || fail "run $1: verdicts differ from test/e2e/expected/run$1.txt (diff above)"; }
# Acceptance criterion 6: every FAIL, SKIP and ERROR line is followed by a non-empty fix line.
hints() { awk '/^(FAIL|SKIP|ERROR) /{if (want) bad=1; want=1; next} want{if ($1 != "fix" || NF < 2) bad=1; want=0} END{exit bad || want}' "bin/e2e-$1.out" || fail "run $1: a FAIL, SKIP or ERROR line has no fix line"; }

run 1 0 -n tenant-a --as system:serviceaccount:tenant-a:tenant-a-dev

# run 2: the service account's own token in its own kubeconfig, no --as (self mode)
sa=bin/e2e-sa-kubeconfig
server=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d >bin/e2e-ca.crt
kubectl --kubeconfig "$sa" config set-cluster kind --server "$server" --certificate-authority bin/e2e-ca.crt --embed-certs >/dev/null
kubectl --kubeconfig "$sa" config set-credentials tenant-a-dev --token "$(kubectl create token tenant-a-dev -n tenant-a --duration 1h)" >/dev/null
kubectl --kubeconfig "$sa" config set-context tenant-a --cluster kind --user tenant-a-dev --namespace tenant-a >/dev/null
kubectl --kubeconfig "$sa" config use-context tenant-a >/dev/null
run 2 0 --kubeconfig "$sa"
expect 2 'groups: .*system:serviceaccounts:tenant-a'
expect 2 '\(groups: system:serviceaccounts, system:serviceaccounts:tenant-a, system:authenticated\)' # both groups the API server adds

run 3 1 -n tenant-b --as system:serviceaccount:tenant-b:tenant-b-dev
count 3 '^FAIL +AT-04\.[1-5] +.* was accepted in a dry run$' 5 # no PSA label, and admin may create pods

run 4 1 -n tenant-a
expect 4 '^this identity looks like a cluster admin; add --as to test a tenant$'
absent 2 'looks like a cluster admin' # self mode as a tenant: no hint

for n in 1 2 4; do count "$n" '^PASS +AT-04\.[1-5] .* violates PodSecurity "restricted:latest"' 5; done # PSA applies to admins too
for n in 1 2 3 4; do
  verdicts "$n"
  count "$n" '^PASS +AT-05 .* admission webhook "validate\.kyverno\.svc-fail" denied' 1
  hints "$n"
done

# AT-01 to AT-03: decided by RBAC; the tenants hold no cluster-scoped rights, cluster-admin holds them all.
for n in 1 2 3; do
  count "$n" '^PASS +AT-01 .* RBAC denied all 4 actions$' 1
  count "$n" '^PASS +AT-02 .* no general get, list or watch on secrets in kube-system$' 1
  count "$n" '^PASS +AT-03 .* RBAC denied all 6 actions$' 1
done
count 4 '^FAIL +AT-0[1-3] .* RBAC allows ' 3

# AT-06 to AT-10 on tenant-a (runs 1, 2 and 4): the fixture's quota, limits, policies, label and headroom.
for n in 1 2 4; do
  count "$n" '^PASS +AT-06 .* ResourceQuota tenant-a-quota has no scopes and sets ' 1
  count "$n" '^PASS +AT-07 .* LimitRange tenant-a-limits gives containers default and defaultRequest ' 1
  count "$n" '^PASS +AT-08 .* default deny for ingress by default-deny-ingress and egress by default-deny-egress' 1
  count "$n" "^ +note +not tested: whether the cluster's network plugin enforces NetworkPolicy$" 1
  count "$n" '^PASS +AT-09 .* pod-security\.kubernetes\.io/enforce=restricted on namespace tenant-a$' 1
  count "$n" '^PASS +AT-10 .* estimate: peak fits tenant-a-quota: requests.cpu 500m of 4' 1
  count "$n" '^ +note +the peak counts active pods during a rollout; terminating pods can add to usage; not counted: DaemonSets, Jobs, CronJobs, bare Pods and Argo Rollouts$' 1
done
# D105: only cluster-admin may change the PSA label; the note says so, the verdict stays PASS.
expect 4 '^ +note +this identity may update namespace tenant-a, so it can change or remove this label$'
for n in 1 2; do absent "$n" 'may (update|patch) namespace'; done

# AT-06 to AT-10 on tenant-b (run 3): an ungoverned namespace fails each with its own reason.
count 3 '^FAIL +AT-06 .* no ResourceQuota in tenant-b$' 1
count 3 '^FAIL +AT-07 .* no LimitRange in tenant-b$' 1
count 3 '^FAIL +AT-08 .* no NetworkPolicy in tenant-b$' 1
count 3 '^FAIL +AT-09 .* namespace tenant-b has no pod-security\.kubernetes\.io/enforce label$' 1 # no AT-04 admission note: AT-04 failed
count 3 '^FAIL +AT-10 .* estimate: no ResourceQuota to compare against$' 1
count 3 '^ +fix +add a ResourceQuota with no scopes .* \(see AT-06\)$' 1 # AT-10: no quota to compare against

say "e2e: all four runs match (verdicts AT-01 to AT-10, layers and reasons, notes, exit codes, headers and hints)"
if [ "${E2E_DELETE:-}" = 1 ]; then kind delete cluster --name "$cluster"; fi
