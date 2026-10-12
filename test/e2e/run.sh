#!/usr/bin/env bash
# run.sh: the kind end-to-end test (make e2e). It creates or reuses a kind cluster, installs
# Kyverno with the restrict-nodeport policy in Enforce mode, applies the tenant-a and tenant-b
# fixtures, builds the plugin and runs it as the four identities of DESIGN.md section 8.
# Phase 1 asserts AT-01 and the exit codes; later phases extend the expectations.
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

# run NAME WANT_EXIT ARGS...: runs the plugin with --debug, prints its report, checks the exit code.
run() {
  local name=$1 want=$2 code start end; shift 2
  say "run $name: kubectl audit-tenant $*"
  start=$(now)
  set +e; "$bin" "$@" --debug >"bin/e2e-$name.out" 2>"bin/e2e-$name.err"; code=$?; set -e
  end=$(now)
  cat "bin/e2e-$name.out"
  printf 'exit %d; %s requests; %.2f s wall\n' "$code" "$(grep -c '^[A-Z]* /' "bin/e2e-$name.err" || true)" "$(echo "$end - $start" | bc)"
  [ "$code" = "$want" ] || fail "run $name exited $code, want $want; stderr: $(cat "bin/e2e-$name.err")"
}
expect() { grep -qE "$2" "bin/e2e-$1.out" || fail "run $1: expected /$2/ in the output above"; }
# Acceptance criterion 6: every FAIL, SKIP and ERROR line is followed by a non-empty fix line.
hints() { awk '/^(FAIL|SKIP|ERROR) /{if (want) bad=1; want=1; next} want{if ($1 != "fix" || NF < 2) bad=1; want=0} END{exit bad || want}' "bin/e2e-$1.out" || fail "run $1: a FAIL, SKIP or ERROR line has no fix line"; }

run 1 0 -n tenant-a --as system:serviceaccount:tenant-a:tenant-a-dev
expect 1 '^PASS  AT-01 '

# run 2: the service account's own token in its own kubeconfig, no --as (self mode)
sa=bin/e2e-sa-kubeconfig
server=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
kubectl config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d >bin/e2e-ca.crt
kubectl --kubeconfig "$sa" config set-cluster kind --server "$server" --certificate-authority bin/e2e-ca.crt --embed-certs >/dev/null
kubectl --kubeconfig "$sa" config set-credentials tenant-a-dev --token "$(kubectl create token tenant-a-dev -n tenant-a --duration 1h)" >/dev/null
kubectl --kubeconfig "$sa" config set-context tenant-a --cluster kind --user tenant-a-dev --namespace tenant-a >/dev/null
kubectl --kubeconfig "$sa" config use-context tenant-a >/dev/null
run 2 0 --kubeconfig "$sa"
expect 2 '^PASS  AT-01 '
expect 2 'groups: .*system:serviceaccounts:tenant-a'

run 3 0 -n tenant-b --as system:serviceaccount:tenant-b:tenant-b-dev
expect 3 '^PASS  AT-01 '

run 4 1 -n tenant-a
expect 4 '^FAIL  AT-01 '
# The admin header line needs AT-03 (B11); B16 adds that expectation.

for n in 1 2 3 4; do hints "$n"; done

say "e2e: all four runs match (AT-01, exit codes and hints)"
if [ "${E2E_DELETE:-}" = 1 ]; then kind delete cluster --name "$cluster"; fi
