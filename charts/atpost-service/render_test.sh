#!/usr/bin/env bash
# Module 4 M4-P0-3 — Helm render regression gate.
#
# Codex accepted the shared-chart worker only with render regression tests. The
# property that matters is a NEGATIVE one: adding an optional workload to a
# chart eight services share must not change what any of those eight render.
# "It looked fine when I checked" does not survive the next values change.
#
# Helm is not installed on the dev machine, so it runs through the official
# image. Same binary CI would use.
#
#   ./charts/atpost-service/render_test.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="./charts/atpost-service"
HELM_IMAGE="${HELM_IMAGE:-alpine/helm:latest}"
fail=0

helm_template() {
  MSYS_NO_PATHCONV=1 docker run --rm -v "${REPO_ROOT}:/wk" -w /wk "${HELM_IMAGE}" \
    template "$1" "${CHART}" -f "$2" --set-string global.awsAccountId=111122223333
}

expect_deployments() {
  local name="$1" values="$2" want="$3"
  local got
  got=$(helm_template "$name" "$values" | grep -c '^kind: Deployment' || true)
  if [[ "$got" != "$want" ]]; then
    echo "FAIL ${values}: rendered ${got} Deployment(s), want ${want}"
    fail=1
  else
    echo "ok   ${values}: ${got} Deployment(s)"
  fi
}

echo "── services that do NOT opt in must render exactly one Deployment ──"
# If the worker block ever renders for these, the optional workload has leaked
# into every service in the platform.
for svc in graph-service suggestion-service api-gateway post-service; do
  for envf in prod staging; do
    values="deploy/services/${svc}/values-${envf}.yaml"
    [[ -f "${REPO_ROOT}/${values}" ]] || continue
    expect_deployments "$svc" "$values" 1
  done
done

echo
echo "── media-service opts in and must render exactly two ──"
for envf in prod staging; do
  expect_deployments media-service "deploy/services/media-service/values-${envf}.yaml" 2
done

# A deployment must supply its real AWS account; checked-in fake accounts are
# forbidden and a missing value must make rendering fail.
if MSYS_NO_PATHCONV=1 docker run --rm -v "${REPO_ROOT}:/wk" -w /wk "${HELM_IMAGE}" \
  template media-service "${CHART}" -f deploy/services/media-service/values-prod.yaml >/dev/null 2>&1; then
  echo "FAIL: media values rendered without global.awsAccountId"; fail=1
else
  echo "ok   media render refuses a missing AWS account id"
fi

echo
echo "── the worker must not collide with the server ──"
out=$(helm_template media-service deploy/services/media-service/values-prod.yaml)

# Distinct names: two Deployments sharing a name is a chart that silently
# deploys only one of them.
if ! grep -q 'name: media-service-worker' <<<"$out"; then
  echo "FAIL: worker Deployment is not uniquely named"; fail=1
else
  echo "ok   worker Deployment has a distinct name"
fi

# Distinct selector. Without the component label in the SELECTOR both
# Deployments match the same pods and fight over replica count.
if [[ $(grep -c 'app.kubernetes.io/component: worker' <<<"$out") -lt 3 ]]; then
  echo "FAIL: worker selector/labels incomplete — the two Deployments may select the same pods"
  fail=1
else
  echo "ok   worker carries a distinct selector"
fi

# A different image from the server. Inheriting the server repository would
# deploy a container with no ffmpeg and no worker binary, which looks healthy
# while doing nothing.
if ! grep -q 'atpost/media-worker' <<<"$out"; then
  echo "FAIL: worker does not use its own image repository"; fail=1
else
  echo "ok   worker uses a separate image repository"
fi

# No Service for the worker: it takes no inbound traffic.
if [[ $(grep -c '^kind: Service$' <<<"$out") -ne 1 ]]; then
  echo "FAIL: expected exactly one Service (the server's); the worker must not publish one"
  fail=1
else
  echo "ok   worker publishes no Service"
fi

# IRSA reuse: the worker runs as the same ServiceAccount, so it gets that
# service's AWS role and nothing broader.
if ! grep -q 'serviceAccountName: media-service' <<<"$out"; then
  echo "FAIL: worker does not reuse the media-service ServiceAccount (IRSA identity)"
  fail=1
else
  echo "ok   worker reuses the service ServiceAccount for IRSA"
fi

echo
if [[ "$fail" -ne 0 ]]; then
  echo "RENDER REGRESSION FAILED"
  exit 1
fi
echo "all render checks passed"
