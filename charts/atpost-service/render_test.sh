#!/usr/bin/env bash
# Helm render gate for charts/atpost-service and every deploy values file.
#
#   ./charts/atpost-service/render_test.sh
#
# What it proves (W4, 3 Oct 2026; the Module 4 worker regressions are kept):
#   1. EVERY deploy/services/*/values-prod.yaml and deploy/web/*/values-prod.yaml
#      renders with a test account id and a test image tag (and every
#      values-staging.yaml still renders);
#   2. the production ApplicationSets (deploy/argocd/applicationset.yaml)
#      carry the account-id token, and with it substituted each one renders
#      every values file its git generator matches, with the parameter it
#      passes; nothing parked under deploy/ matches;
#   3. the chart REFUSES: a missing / fake / templated account id, an empty
#      image tag, an empty worker tag, an unfilled __TF_*__ placeholder;
#   4. the optional templates (worker, migration Job, webhook Ingress,
#      NetworkPolicy) render nothing for a service that does not opt in, and
#      the right objects for the ones that do;
#   5. static wiring: every in-cluster URL in prod values uses the target
#      service's port; the webhook joins the API load balancer with the same
#      certificate and WAF ACL as the API ingress; no `latest` image tag.
#
# Helm is not installed on the dev machine, so it runs through the official
# image (one container for all renders). Same binary CI would use.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="./charts/atpost-service"
HELM_IMAGE="${HELM_IMAGE:-alpine/helm:latest}"
ACCT=111122223333
TAG=0123456789abcdef0123456789abcdef01234567
fail=0

# Docker Desktop on Windows wants a Windows path for the bind mount.
host_path() { (cd "$1" && pwd -W 2>/dev/null) || (cd "$1" && pwd); }
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/out"

ok()  { echo "ok   $*"; }
bad() { echo "FAIL $*"; fail=1; }

# ── job queue: every render is a line "<id>|<expect ok|err>|<helm args>" ──
JOBS="$WORK/jobs.txt"; : > "$JOBS"
job() { printf '%s|%s|%s\n' "$1" "$2" "$3" >> "$JOBS"; }
BASE="--set-string global.awsAccountId=$ACCT --set-string image.tag=$TAG --set-string worker.image.tag=$TAG"
LOCAL="$BASE --set global.allowPlaceholders=true"

cd "$REPO_ROOT"
mapfile -t PROD < <(ls deploy/services/*/values-prod.yaml deploy/web/*/values-prod.yaml)
mapfile -t STAGING < <(ls deploy/services/*/values-staging.yaml deploy/web/*/values-staging.yaml 2>/dev/null || true)
id_of() { local d; d="$(dirname "$1")"; printf '%s_%s' "$(basename "$(dirname "$d")")-$(basename "$d")" "$(basename "$1" .yaml)"; }

for f in "${PROD[@]}"; do job "$(id_of "$f")" ok "-f $f $LOCAL"; done
for f in "${STAGING[@]}"; do job "$(id_of "$f")" ok "-f $f --set-string global.awsAccountId=$ACCT --set global.allowPlaceholders=true"; done

M=deploy/services/media-service/values-prod.yaml
G=deploy/services/graph-service/values-prod.yaml
job neg-no-account      err "-f $M --set-string image.tag=$TAG --set-string worker.image.tag=$TAG --set global.allowPlaceholders=true"
job neg-fake-account    err "-f $M --set-string global.awsAccountId=123 --set-string image.tag=$TAG --set-string worker.image.tag=$TAG --set global.allowPlaceholders=true"
job neg-token-account   err "-f $M --set-string global.awsAccountId=\${aws_account_id} --set-string image.tag=$TAG --set-string worker.image.tag=$TAG --set global.allowPlaceholders=true"
job neg-empty-tag       err "-f $G --set-string global.awsAccountId=$ACCT"
job neg-worker-tag      err "-f $M --set-string global.awsAccountId=$ACCT --set-string image.tag=$TAG --set global.allowPlaceholders=true"
job neg-placeholder     err "-f $M $BASE"
job pos-no-placeholder  ok  "-f $G $BASE"
job commerce-migrate-on ok  "-f deploy/services/commerce-service/values-prod.yaml $LOCAL --set migrationJob.enabled=true"

# ── the production ApplicationSets ─────────────────────────────────────────
APPSET=deploy/argocd/applicationset.yaml
token_count=$(grep -c 'awsAccountId: "\${aws_account_id}"' "$APPSET" || true)
appsets=$(grep -c '^kind: ApplicationSet' "$APPSET" || true)
if [[ "$token_count" -ne "$appsets" || "$appsets" -eq 0 ]]; then
  bad "$APPSET: every ApplicationSet must carry awsAccountId: \"\${aws_account_id}\" ($token_count of $appsets)"
else
  ok "$APPSET: $appsets ApplicationSets carry the account-id token"
fi
# Apart from the tokens, nothing Terraform's templatefile would interpret.
if [[ $(grep -o '\${[^}]*}\|%{' "$APPSET" | grep -vc '^\${aws_account_id}$' || true) -ne 0 ]]; then
  bad "$APPSET: template syntax other than the account-id token (templatefile would choke)"
fi
sed 's/\$[{]aws_account_id[}]/'"$ACCT"'/g' "$APPSET" > "$WORK/appset.yaml"
grep -q '\${' "$WORK/appset.yaml" && bad "$APPSET: substitution left a token"
grep -q '^ *automated:' "$WORK/appset.yaml" && bad "$APPSET: a production ApplicationSet syncs automatically (must be manual)"
grep -q 'atpost-services-staging\|atpost-web-staging' "$WORK/appset.yaml" && bad "$APPSET: staging ApplicationSets must not be in the file Terraform applies to production"
# Each document is valid YAML: helm parses it as a values file.
awk -v d="$WORK" 'BEGIN{n=0; f=d"/appset-0.yaml"} /^---$/{n++; f=d"/appset-" n ".yaml"; next} {print > f}' "$WORK/appset.yaml"
i=0
for doc in "$WORK"/appset-*.yaml; do
  cp "$doc" "$WORK/out/appset-doc-$i.yaml"
  job "appset-doc-$i-parses" ok "-f $G $BASE -f /wkout/appset-doc-$i.yaml"
  i=$((i + 1))
done
# Emulate each generator: list element × git files, rendered with the
# template's own valueFiles and parameter.
mapfile -t patterns < <(grep -oE 'path: "deploy/[^"]+"' "$WORK/appset.yaml" | sed 's/path: "//; s/"$//')
mapfile -t accts < <(grep -oE 'awsAccountId: "[^"]*"' "$WORK/appset.yaml" | sed 's/awsAccountId: "//; s/"$//')
param_ok=$(grep -c "name: global.awsAccountId" "$WORK/appset.yaml" || true)
[[ "$param_ok" -eq "$appsets" ]] || bad "$APPSET: every template must pass global.awsAccountId ($param_ok of $appsets)"
for idx in "${!patterns[@]}"; do
  p="${patterns[$idx]}"; a="${accts[$idx]:-}"
  [[ "$a" =~ ^[0-9]{12}$ ]] || bad "$APPSET generator $idx: account id did not substitute"
  # ArgoCD's default (legacy) git-file globbing lets `*` cross `/`: match
  # with find so a nested file would be caught too.
  dir="${p%%/\**}"; leaf="${p##*/}"
  mapfile -t matched < <(find "$dir" -mindepth 2 -name "$leaf" -type f | sort)
  mapfile -t shallow < <(ls $p 2>/dev/null | sort)
  if [[ "${matched[*]}" != "${shallow[*]}" ]]; then
    bad "$p: files below the intended depth would become Applications: $(comm -23 <(printf '%s\n' "${matched[@]}") <(printf '%s\n' "${shallow[@]}") | tr '\n' ' ')"
  fi
  for f in "${matched[@]}"; do
    job "appset-$idx-$(id_of "$f")" ok "-f $f --set-string global.awsAccountId=$a --set-string image.tag=$TAG --set-string worker.image.tag=$TAG --set global.allowPlaceholders=true"
  done
  ok "$p → ${#matched[@]} Application(s) with account $a"
done
[[ $(find deploy/web -name values-prod.yaml | wc -l) -eq 2 ]] || bad "deploy/web must hold exactly the app and admin-console prod values"
ST=deploy/argocd/applicationset-staging.yaml
[[ -f "$ST" ]] && { grep -q 'aws_account_id' "$ST" && bad "$ST must not carry the production token" || ok "$ST is separate and untokened"; }

# ── run every render in one container ──────────────────────────────────────
cat > "$WORK/out/run.sh" <<'EOS'
#!/bin/sh
while IFS='|' read -r id expect args; do
  [ -z "$id" ] && continue
  # shellcheck disable=SC2086
  if helm template rel ./charts/atpost-service $args > "/wkout/$id.out" 2> "/wkout/$id.err"; then
    echo 0 > "/wkout/$id.rc"
  else
    echo 1 > "/wkout/$id.rc"
  fi
done < /wkout/jobs.txt
EOS
cp "$JOBS" "$WORK/out/jobs.txt"
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "$(host_path "$REPO_ROOT"):/wk" -v "$(host_path "$WORK/out"):/wkout" -w /wk \
  --entrypoint sh "$HELM_IMAGE" /wkout/run.sh

R() { cat "$WORK/out/$1.out"; }
while IFS='|' read -r id expect _; do
  rc=$(cat "$WORK/out/$id.rc" 2>/dev/null || echo missing)
  if [[ "$expect" == ok && "$rc" != 0 ]]; then
    bad "$id did not render: $(tail -1 "$WORK/out/$id.err")"
  elif [[ "$expect" == err && "$rc" != 1 ]]; then
    bad "$id rendered but must be refused"
  fi
done < "$JOBS"
echo
echo "── ${#PROD[@]} prod + ${#STAGING[@]} staging values files rendered ($(grep -c '' "$JOBS") renders in total) ──"

grep -q 'awsAccountId is required' "$WORK/out/neg-no-account.err" && ok "missing account id refused" || bad "missing account id: wrong refusal"
grep -q 'awsAccountId is required' "$WORK/out/neg-fake-account.err" && ok "short account id refused" || bad "short account id: wrong refusal"
grep -q 'awsAccountId is required' "$WORK/out/neg-token-account.err" && ok "unfilled ApplicationSet token refused" || bad "token account id: wrong refusal"
grep -q 'image.tag is required' "$WORK/out/neg-empty-tag.err" && ok "empty image tag refused" || bad "empty tag: wrong refusal"
grep -q 'worker.image.tag is required' "$WORK/out/neg-worker-tag.err" && ok "empty worker tag refused" || bad "empty worker tag: wrong refusal"
grep -q 'unfilled Terraform placeholder' "$WORK/out/neg-placeholder.err" && ok "unfilled __TF_*__ placeholder refused" || bad "placeholder: wrong refusal"

echo
echo "── per-file properties of the prod renders ──"
for f in "${PROD[@]}"; do
  id="$(id_of "$f")"; [[ -f "$WORK/out/$id.out" ]] || continue
  out="$(R "$id")"
  # Never `latest`, never an empty tag.
  if grep -E '^\s+image: ' <<<"$out" | grep -qE ':latest"?$|:"?$'; then bad "$f: renders a latest/empty image tag"; fi
  # A PDB that requires every replica blocks node drains.
  if grep -q '^kind: PodDisruptionBudget' <<<"$out"; then
    min=$(awk '/^kind: PodDisruptionBudget/{p=1} p&&/minAvailable:/{print $2; exit}' <<<"$out")
    if grep -q '^kind: HorizontalPodAutoscaler' <<<"$out"; then
      floor=$(awk '/^kind: HorizontalPodAutoscaler/{p=1} p&&/minReplicas:/{print $2; exit}' <<<"$out")
    else
      floor=$(awk '/^kind: Deployment/{p=1} p&&/replicas:/{print $2; exit}' <<<"$out")
    fi
    if [[ -n "$min" && -n "$floor" && "$min" =~ ^[0-9]+$ && "$min" -ge "$floor" ]]; then
      bad "$f: PDB minAvailable $min >= replicas $floor blocks every node drain"
    fi
  fi
done
ok "no latest/empty image tags; no drain-blocking PDB"

# In-cluster URLs use the target service's port.
declare -A PORT
for f in deploy/services/*/values-prod.yaml deploy/web/*/values-prod.yaml; do
  n=$(awk '/^service:/{p=1} p&&/^  name:/{print $2; exit}' "$f")
  PORT[$n]=$(awk '/^service:/{p=1} p&&/^  port:/{print $2; exit}' "$f")
done
url_bad=0
while read -r u; do
  h="${u#http://}"; s="${h%%.*}"; p="${u##*:}"
  if [[ "${PORT[$s]:-}" != "$p" ]]; then bad "URL $u: $s listens on ${PORT[$s]:-nothing}"; url_bad=1; fi
done < <(grep -hoE 'http://[a-z0-9-]+\.atpost\.svc\.cluster\.local:[0-9]+' "${PROD[@]}" | sort -u)
[[ $url_bad -eq 0 ]] && ok "every in-cluster URL in prod values uses its service's port"
[[ "${PORT[identity-user-service]}" == 8110 ]] || bad "identity-user-service must listen on 8110"
[[ "${PORT[ai-service]}" != "${PORT[identity-profile-service]}" ]] || bad "ai-service shares identity-profile's port"

echo
echo "── optional templates ──"
for svc in graph-service suggestion-service api-gateway post-service; do
  out="$(R "$(id_of deploy/services/$svc/values-prod.yaml)")"
  [[ $(grep -c '^kind: Deployment' <<<"$out") -eq 1 ]] || bad "$svc: must render exactly one Deployment"
  grep -q '^kind: Job' <<<"$out" && bad "$svc: renders a Job it never asked for"
  grep -q '^kind: NetworkPolicy' <<<"$out" && bad "$svc: renders a NetworkPolicy it never asked for"
  grep -q 'name: .*-webhook$' <<<"$out" && bad "$svc: renders a webhook Ingress it never asked for"
done
ok "services that do not opt in render one Deployment and no Job/NetworkPolicy/webhook"

media="$(R "$(id_of $M)")"
[[ $(grep -c '^kind: Deployment' <<<"$media") -eq 2 ]] && ok "media-service renders server + worker" || bad "media-service: want 2 Deployments"
grep -q 'name: media-service-worker' <<<"$media" || bad "worker Deployment is not uniquely named"
[[ $(grep -c 'app.kubernetes.io/component: worker' <<<"$media") -ge 3 ]] || bad "worker selector/labels incomplete — the two Deployments may select the same pods"
grep -q "atpost/media-worker:$TAG" <<<"$media" || bad "worker does not use its own image repository at the CI tag"
[[ $(grep -c '^kind: Service$' <<<"$media") -eq 1 ]] || bad "worker must not publish a Service"
grep -q 'serviceAccountName: media-service' <<<"$media" || bad "worker does not reuse the media-service ServiceAccount (IRSA identity)"
grep -q '^ *command:$' <<<"$media" && bad "media worker renders a command it never set"
ok "worker: distinct name/selector/image, no Service, same IRSA identity, image CMD kept"

dating="$(R "$(id_of deploy/services/dating-service/values-prod.yaml)")"
{ grep -q 'name: dating-service-worker' <<<"$dating" && grep -q -- '- /data-exporter' <<<"$dating"; } && ok "dating worker runs /data-exporter" || bad "dating worker is not the data exporter"

commerce="$(R "$(id_of deploy/services/commerce-service/values-prod.yaml)")"
grep -q '^kind: Job' <<<"$commerce" && bad "commerce: migration Job renders while disabled (the image has no migrator yet)"
mig="$(R commerce-migrate-on)"
if grep -q 'argocd.argoproj.io/hook: PreSync' <<<"$mig" && grep -q 'name: commerce-service-migrate' <<<"$mig"; then
  ok "migration Job: PreSync hook when enabled"
else
  bad "migration Job missing its PreSync hook"
fi
# The Job's POD must not carry the selector labels (Service/PDB/HPA would count it).
pod_labels=$(awk '/^kind: Job/{j=1} j&&/^  template:/{t=1} t&&/labels:/{l=1;next} l&&/^ {8}[a-z]/{print} l&&!/^ {8}/{exit}' <<<"$mig")
grep -q 'app.kubernetes.io/instance' <<<"$pod_labels" && bad "migration pod carries the selector labels" || ok "migration pod is not selected by the Service/PDB/HPA"

pay="$(R "$(id_of deploy/services/payments-service/values-prod.yaml)")"
grep -q 'name: payments-service-webhook' <<<"$pay" || bad "payments: webhook Ingress missing"
grep -q '^kind: NetworkPolicy' <<<"$pay" || bad "payments: NetworkPolicy missing"
grep -q 'cidr: "10.30.0.0/16"' <<<"$pay" || bad "payments: the ALB (VPC CIDR) is not allowed to reach the webhook"
gw="deploy/services/api-gateway/values-prod.yaml"
ann() { grep -E "^\s+$2:" "$1" | head -1 | sed -E 's/^[^:]+: *//; s/ *#.*$//; s/^"//; s/"$//'; }
pay_f=deploy/services/payments-service/values-prod.yaml
[[ "$(ann $gw 'alb.ingress.kubernetes.io/wafv2-acl-arn')" == "$(ann $pay_f wafAclArn)" ]] \
  && ok "webhook and API ingress name the same WAF ACL (one ACL per ALB group)" \
  || bad "webhook WAF ACL differs from the API ingress's: the load-balancer controller refuses the whole group"
[[ "$(ann $gw 'alb.ingress.kubernetes.io/certificate-arn')" == "$(ann $pay_f certificateArn)" ]] || bad "webhook certificate differs from the API's"
[[ "$(ann $gw 'alb.ingress.kubernetes.io/group.name')" == "$(ann $pay_f groupName)" ]] && ok "webhook joins the API ALB group" || bad "webhook is not in the API ALB group"
grep -q 'alb.ingress.kubernetes.io/wafv2-acl-arn' <<<"$pay" || bad "webhook Ingress has no WAF annotation"

web="$(R "$(id_of deploy/web/app/values-prod.yaml)")"
{ grep -q 'host: "app.cleestudio.com"' <<<"$web" && grep -q 'host: "cleestudio.com"' <<<"$web" && grep -q 'actions.redirect-to-app' <<<"$web" && grep -q 'name: use-annotation' <<<"$web"; } \
  && ok "web: app. served, bare domain redirects to app." || bad "web: host rules / redirect incomplete"
grep -q 'containerPort: 3000' <<<"$web" || bad "web: not on port 3000"
grep -q 'mountPath: "/app/.next/cache"' <<<"$web" || bad "web: Next cache is not writable"
grep -q "atpost/web:$TAG" <<<"$web" || bad "web: image is not atpost/web"
adm="$(R "$(id_of deploy/web/admin-console/values-prod.yaml)")"
{ grep -q 'host: "admin.cleestudio.com"' <<<"$adm" && grep -q "atpost/admin-console:$TAG" <<<"$adm"; } && ok "admin console: admin.cleestudio.com, atpost/admin-console" || bad "admin console render incomplete"
for f in deploy/web/app/values-prod.yaml deploy/web/admin-console/values-prod.yaml; do
  [[ "$(ann $f 'alb.ingress.kubernetes.io/wafv2-acl-arn')" == "$(ann deploy/web/app/values-prod.yaml 'alb.ingress.kubernetes.io/wafv2-acl-arn')" ]] || bad "$f: web ALB group members name different WAF ACLs"
done

echo
if [[ "$fail" -ne 0 ]]; then
  echo "RENDER CHECKS FAILED"
  exit 1
fi
echo "all render checks passed"
