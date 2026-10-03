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
#   6. QA (the QA AWS account, 3 Oct 2026): every values-qa.yaml renders and
#      has a values-prod.yaml sibling (and the reverse); the QA ApplicationSets
#      (deploy/argocd/applicationset-qa.yaml) pass the same checks as prod on
#      branch `qa`; and the QA guards hold:
#        - no prod host (app./api./ws./admin./media.cleestudio.com, the bare
#          domain, no-reply@cleestudio.com) and no prod name (atpost/prod/,
#          atpost-prod-, the prod VPC CIDR, release/prod) in any QA file;
#        - ENV / APP_ENV / DEPLOY_ENV / NODE_ENV are EXACTLY the prod file's
#          values (QA runs with production semantics), in the files and in
#          every rendered container;
#        - PAYMENTS_ALLOW_STUB, OTP_BYPASS_CODE, CALLS_DEV_ALLOW_STUB_MEDIA
#          absent; NEXT_PUBLIC_ENABLE_STUB_PAYMENTS never "true";
#        - every other env value equals prod's after the QA data mapping
#          (hosts, names, CIDR): launch switches cannot drift;
#        - one QA JWT issuer/audience shared by the gateway, identity-auth and
#          the chat verifiers, different from prod's;
#        - QA size (one replica, HPA max 2, no drain-blocking PDB) and QA-only
#          ingress hosts in the renders.
#
#   QA_GUARDS_ONLY=1 ./charts/atpost-service/render_test.sh
#      runs only the static QA guards (no Docker): a quick check while
#      editing values-qa.yaml. The full run is the gate.
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
mapfile -t QA < <(ls deploy/services/*/values-qa.yaml deploy/web/*/values-qa.yaml 2>/dev/null || true)
id_of() { local d; d="$(dirname "$1")"; printf '%s_%s' "$(basename "$(dirname "$d")")-$(basename "$d")" "$(basename "$1" .yaml)"; }

# ── QA static guards (no Docker) ───────────────────────────────────────────
QA_ARGO=(deploy/argocd/applicationset-qa.yaml deploy/argocd/repo-credentials-qa.yaml)
QA_ALL=("${QA[@]}" "${QA_ARGO[@]}")
# (Every helper below reads ALL files in one process: process spawns are
# slow on Windows, and one per file per check made this section minutes.)
SQ="'"
QUOTE="[\"$SQ]"   # an ERE class matching either quote character
# Top-level `env:` of each file as "<file>\t<KEY>=<value>" lines (quotes and
# trailing comments stripped; commented-out keys ignored).
env_all() {
  awk -v quote="$QUOTE" 'FNR == 1 { p = 0 } /^env:/ { p = 1; next } /^[^ #]/ { p = 0 }
       p && /^  [A-Z0-9_]+:/ { line = $0; sub(/^  /, "", line); k = line; sub(/:.*/, "", k)
         v = line; sub(/^[^:]*:[ ]*/, "", v); sub(/[ ]+#.*$/, "", v)
         gsub("^" quote "|" quote "$", "", v)
         print FILENAME "\t" k "=" v }' "$@"
}
# One value from env_all output on stdin: env_get <file> <KEY>
env_get() { awk -F'\t' -v f="$1" -v k="$2=" '$1 == f && index($2, k) == 1 { print substr($2, length(k) + 1) }'; }
# ENV / APP_ENV / DEPLOY_ENV / NODE_ENV lines (any indent: server, worker,
# migration job) of each file, normalised, joined per file:
# "<file>\t<ENV=prod,APP_ENV=production,...>".
env_sem_all() {
  awk -v sq="$SQ" 'FNR == 1 { if (f != "") print f "\t" acc; f = FILENAME; acc = "" }
       /^[ \t]+(ENV|APP_ENV|DEPLOY_ENV|NODE_ENV)[ \t]*:/ { l = $0; sub(/[ \t]+#.*$/, "", l)
         gsub("[\"" sq " \t]", "", l); acc = acc l "," }
       END { if (f != "") print f "\t" acc }' "$@"
}
# The QA data mapping (the only differences allowed in a value): hosts,
# environment names, the VPC CIDR.
qa_map() {
  sed -E 's#atpost/prod/#atpost/qa/#g; s#atpost-prod-#atpost-qa-#g;
    s#https://app\.cleestudio\.com,https://cleestudio\.com,https://admin\.cleestudio\.com#https://qa.cleestudio.com,https://admin-qa.cleestudio.com#g;
    s#https://app\.cleestudio\.com,https://cleestudio\.com#https://qa.cleestudio.com#g;
    s#(^|[^a-z0-9.-])app\.cleestudio\.com#\1qa.cleestudio.com#g;
    s#(^|[^a-z0-9.-])(api|ws|admin|media)\.cleestudio\.com#\1\2-qa.cleestudio.com#g;
    s#no-reply@cleestudio\.com#no-reply@qa.cleestudio.com#g; s#10\.30\.0\.0/16#10.40.0.0/16#g'
}
# Keys whose QA value is deliberately not the mapped prod value, and keys QA
# adds. Everything else (every launch switch) must match prod. The *_USER_IDS
# here are per-account user ids (each account has its own users), filled
# after registration; product pilot gates that open a closed product to
# outsiders (DATING_PILOT_USER_IDS, COMMUNITIES_ALLOWED_*) are NOT listed and
# stay prod's.
QA_DATA_KEYS=" JWT_ISSUER JWT_AUDIENCE ADMIN_IMAGE_ORIGINS SUPERADMIN_USER_IDS ADMIN_USER_IDS MODERATOR_USER_IDS LIVE_PILOT_USER_IDS PAGES_ADMIN_USER_IDS "
QA_ONLY_KEYS=" commerce-service:COURIER_PROVIDER "
qa_static_guards() {
  echo
  echo "── QA guards (${#QA[@]} values-qa files) ──"
  local f hits
  [[ ${#QA[@]} -eq ${#PROD[@]} ]] || bad "QA: ${#QA[@]} values-qa files for ${#PROD[@]} values-prod files"
  for f in "${PROD[@]}"; do [[ -f "${f%values-prod.yaml}values-qa.yaml" ]] || bad "QA: $f has no values-qa.yaml"; done
  for f in "${QA[@]}"; do [[ -f "${f%values-qa.yaml}values-prod.yaml" ]] || bad "QA: $f has no values-prod.yaml"; done
  for f in "${QA_ARGO[@]}"; do [[ -f "$f" ]] || bad "QA: $f missing"; done

  # Prod hosts: app./api./ws./admin./media.cleestudio.com, the bare domain
  # (any cleestudio.com not preceded by a label), comments included.
  hits=$(grep -nE '(^|[^a-z0-9.-])((app|api|ws|admin|media)\.)?cleestudio\.com' "${QA_ALL[@]}" || true)
  [[ -z "$hits" ]] && ok "QA: no prod or bare-domain host in any QA file" || bad "QA: prod host in a QA file:"$'\n'"$hits"
  hits=$(grep -nE 'atpost/prod/|atpost-prod-|10\.30\.0\.0/16|release/prod' "${QA_ALL[@]}" || true)
  [[ -z "$hits" ]] && ok "QA: no atpost/prod/, atpost-prod-, prod CIDR or release/prod in any QA file" || bad "QA: prod name in a QA file:"$'\n'"$hits"

  # Environment semantics: exactly prod's values, line for line (top-level
  # env, worker env and migration-job env alike).
  local semfail=0 c
  hits=$(awk -F'\t' 'FNR == NR { f = $1; sub(/values-prod\.yaml$/, "values-qa.yaml", f); prod[f] = $2; next }
                     prod[$1] != $2 { print $1 ": [" $2 "] but prod [" prod[$1] "]" }' \
    <(env_sem_all "${PROD[@]}") <(env_sem_all "${QA[@]}"))
  [[ -z "$hits" ]] || { bad "QA: ENV/APP_ENV/DEPLOY_ENV/NODE_ENV differ from prod:"$'\n'"$hits"; semfail=1; }
  hits=$(grep -nE "^\\s+(ENV|APP_ENV|DEPLOY_ENV)\\s*:\\s*$QUOTE?(qa|staging|stage|dev|development|local|test|ci)$QUOTE?\\s*(#.*)?\$" "${QA[@]}" || true)
  [[ -z "$hits" ]] || { bad "QA: a non-production environment name:"$'\n'"$hits"; semfail=1; }
  c=$(grep -lE '^\s+ENV\s*:' "${QA[@]}" | wc -l)
  [[ "$c" -eq $(( ${#QA[@]} - 2 )) ]] || { bad "QA: $c of $(( ${#QA[@]} - 2 )) service files set ENV"; semfail=1; }
  [[ $semfail -eq 0 ]] && ok "QA: ENV/APP_ENV/DEPLOY_ENV/NODE_ENV are prod's values in every file"

  # Stubs and bypasses: never set (comments explaining their absence are fine).
  hits=$(grep -nE "^\\s*(PAYMENTS_ALLOW_STUB|OTP_BYPASS_CODE|CALLS_DEV_ALLOW_STUB_MEDIA)\\s*:|^\\s*-.*secretKey:\\s*(PAYMENTS_ALLOW_STUB|OTP_BYPASS_CODE)\\b|^\\s*NEXT_PUBLIC_ENABLE_STUB_PAYMENTS\\s*:\\s*$QUOTE?true" "${QA[@]}" || true)
  [[ -z "$hits" ]] && ok "QA: PAYMENTS_ALLOW_STUB, OTP_BYPASS_CODE, stub media/payments absent" || bad "QA: stub or bypass set:"$'\n'"$hits"

  # Launch switches: every prod env key exists in QA with the mapped value.
  local envs_prod envs_qa
  envs_prod="$(env_all "${PROD[@]}")"
  envs_qa="$(env_all "${QA[@]}")"
  hits=$(awk -F'\t' -v data="$QA_DATA_KEYS" -v only="$QA_ONLY_KEYS" '
      { i = index($2, "="); k = substr($2, 1, i - 1); v = substr($2, i + 1) }
      FNR == NR { f = $1; sub(/values-prod\.yaml$/, "values-qa.yaml", f); prod[f SUBSEP k] = v; next }
      { svc = $1; sub(/\/[^\/]*$/, "", svc); sub(/.*\//, "", svc); key = $1 SUBSEP k }
      index(data, " " k " ") { delete prod[key]; next }
      !(key in prod) { if (!index(only, " " svc ":" k " ")) print $1 ": adds " k " (not in prod)"; next }
      prod[key] != v { print $1 ": " k "=" v " but prod (QA-mapped) " k "=" prod[key] }
      { delete prod[key] }
      END { for (key in prod) { split(key, a, SUBSEP); if (!index(data, " " a[2] " ")) print a[1] ": drops " a[2] " (prod: " prod[key] ")" } }
    ' <(qa_map <<<"$envs_prod") <(printf '%s\n' "$envs_qa"))
  [[ -z "$hits" ]] && ok "QA: every env value (launch switches included) is prod's, after the QA data mapping" \
    || bad "QA: env drifts from prod:"$'\n'"$hits"

  # One QA token identity across the minting and verifying services.
  local iss aud svc gw=deploy/services/api-gateway
  iss="$(env_get $gw/values-qa.yaml JWT_ISSUER <<<"$envs_qa")"
  aud="$(env_get $gw/values-qa.yaml JWT_AUDIENCE <<<"$envs_qa")"
  [[ -n "$iss" && -n "$aud" ]] || bad "QA: gateway JWT_ISSUER/JWT_AUDIENCE unset"
  [[ "$iss" != "$(env_get $gw/values-prod.yaml JWT_ISSUER <<<"$envs_prod")" ]] || bad "QA: JWT_ISSUER equals prod's"
  [[ "$aud" != "$(env_get $gw/values-prod.yaml JWT_AUDIENCE <<<"$envs_prod")" ]] || bad "QA: JWT_AUDIENCE equals prod's"
  hits=$(awk -F'\t' -v iss="JWT_ISSUER=$iss" -v aud="JWT_AUDIENCE=$aud" '
      $1 ~ /\/(identity-auth-service|chat-call-service|chat-message-service|chat-ws-gateway)\/values-qa\.yaml$/ {
        if ($2 ~ /^JWT_ISSUER=/) { seen[$1 "i"] = 1; if ($2 != iss) print $1 ": " $2 }
        if ($2 ~ /^JWT_AUDIENCE=/) { seen[$1 "a"] = 1; if ($2 != aud) print $1 ": " $2 } }
      END { if (length(seen) != 8) print "expected JWT_ISSUER and JWT_AUDIENCE in 4 files, found " length(seen) }' <<<"$envs_qa")
  [[ -z "$hits" ]] && ok "QA: one JWT identity ($iss, $aud) on gateway, identity-auth and chat; not prod's" \
    || bad "QA: JWT identity differs from the gateway's:"$'\n'"$hits"

  # Size.
  local sizefail=0
  hits=$(grep -LE '^replicaCount: 1$' "${QA[@]}" || true)
  [[ -z "$hits" ]] || { bad "QA: replicaCount is not 1 in: $hits"; sizefail=1; }
  hits=$(grep -nE '^  maxReplicas: ([3-9]|[1-9][0-9]+)$|^  replicaCount: ([2-9]|[1-9][0-9]+)$' "${QA[@]}" || true)
  [[ -z "$hits" ]] || { bad "QA: HPA max or worker replicas above the QA size:"$'\n'"$hits"; sizefail=1; }
  [[ $sizefail -eq 0 ]] && ok "QA: one replica, HPA max 2, one worker"
  return 0
}
qa_static_guards
if [[ "${QA_GUARDS_ONLY:-}" == 1 ]]; then
  echo
  [[ "$fail" -ne 0 ]] && { echo "QA GUARDS FAILED"; exit 1; }
  echo "QA guards passed (static only; run without QA_GUARDS_ONLY for the renders)"
  exit 0
fi

for f in "${PROD[@]}"; do job "$(id_of "$f")" ok "-f $f $LOCAL"; done
for f in "${STAGING[@]}"; do job "$(id_of "$f")" ok "-f $f --set-string global.awsAccountId=$ACCT --set global.allowPlaceholders=true"; done
for f in "${QA[@]}"; do job "$(id_of "$f")" ok "-f $f $LOCAL"; done


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

# ── the ApplicationSets (production, then QA) ──────────────────────────────
# check_appset <file> <tag> <revision> <values file name> <env label>
# The same checks for both: the account-id token in every ApplicationSet and
# no other template syntax; manual sync; every document parses; the branch
# and the values file name are the environment's own; each generator renders
# every file it matches with the parameter it passes.
check_appset() {
  local APPSET="$1" tag="$2" rev="$3" vname="$4" envl="$5"
  local token_count appsets i doc idx p a dir leaf param_ok
  local -a patterns accts matched shallow
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
  mkdir -p "$WORK/$tag"
  sed 's/\$[{]aws_account_id[}]/'"$ACCT"'/g' "$APPSET" > "$WORK/$tag.yaml"
  grep -q '\${' "$WORK/$tag.yaml" && bad "$APPSET: substitution left a token"
  grep -q '^ *automated:' "$WORK/$tag.yaml" && bad "$APPSET: an ApplicationSet syncs automatically (must be manual)"
  # The environment's own branch and values file, nothing else.
  [[ "$(grep -oE '(revision|targetRevision): *[^ ]+' "$WORK/$tag.yaml" | sed -E 's/.*: *//' | sort -u)" == "$rev" ]] \
    || bad "$APPSET: tracks a branch other than $rev: $(grep -oE '(revision|targetRevision): *[^ ]+' "$WORK/$tag.yaml" | sort -u | tr '\n' ' ')"
  [[ -z "$(grep -oE 'values-[a-z-]+\.yaml' "$WORK/$tag.yaml" | grep -v "^$vname\$" || true)" ]] \
    || bad "$APPSET: references a values file other than $vname"
  [[ $(grep -c "atpost.io/env: $envl\$" "$WORK/$tag.yaml" || true) -eq "$appsets" ]] || bad "$APPSET: every template must label atpost.io/env: $envl"
  # Each document is valid YAML: helm parses it as a values file.
  awk -v d="$WORK/$tag" 'BEGIN{n=0; f=d"/doc-0.yaml"} /^---$/{n++; f=d"/doc-" n ".yaml"; next} {print > f}' "$WORK/$tag.yaml"
  i=0
  for doc in "$WORK/$tag"/doc-*.yaml; do
    cp "$doc" "$WORK/out/$tag-doc-$i.yaml"
    job "$tag-doc-$i-parses" ok "-f $G $BASE -f /wkout/$tag-doc-$i.yaml"
    i=$((i + 1))
  done
  # Emulate each generator: list element × git files, rendered with the
  # template's own valueFiles and parameter.
  mapfile -t patterns < <(grep -oE 'path: "deploy/[^"]+"' "$WORK/$tag.yaml" | sed 's/path: "//; s/"$//')
  mapfile -t accts < <(grep -oE 'awsAccountId: "[^"]*"' "$WORK/$tag.yaml" | sed 's/awsAccountId: "//; s/"$//')
  param_ok=$(grep -c "name: global.awsAccountId" "$WORK/$tag.yaml" || true)
  [[ "$param_ok" -eq "$appsets" ]] || bad "$APPSET: every template must pass global.awsAccountId ($param_ok of $appsets)"
  for idx in "${!patterns[@]}"; do
    p="${patterns[$idx]}"; a="${accts[$idx]:-}"
    [[ "$a" =~ ^[0-9]{12}$ ]] || bad "$APPSET generator $idx: account id did not substitute"
    [[ "$p" == */"$vname" ]] || bad "$APPSET generator $idx: $p does not match $vname"
    # ArgoCD's default (legacy) git-file globbing lets `*` cross `/`: match
    # with find so a nested file would be caught too.
    dir="${p%%/\**}"; leaf="${p##*/}"
    mapfile -t matched < <(find "$dir" -mindepth 2 -name "$leaf" -type f | sort)
    mapfile -t shallow < <(ls $p 2>/dev/null | sort)
    if [[ "${matched[*]}" != "${shallow[*]}" ]]; then
      bad "$p: files below the intended depth would become Applications: $(comm -23 <(printf '%s\n' "${matched[@]}") <(printf '%s\n' "${shallow[@]}") | tr '\n' ' ')"
    fi
    for f in "${matched[@]}"; do
      job "$tag-$idx-$(id_of "$f")" ok "-f $f --set-string global.awsAccountId=$a --set-string image.tag=$TAG --set-string worker.image.tag=$TAG --set global.allowPlaceholders=true"
    done
    ok "$p → ${#matched[@]} Application(s) with account $a"
  done
}
check_appset deploy/argocd/applicationset.yaml appset release/prod values-prod.yaml prod
grep -q 'atpost-services-staging\|atpost-web-staging' "$WORK/appset.yaml" && bad "deploy/argocd/applicationset.yaml: staging ApplicationSets must not be in the file Terraform applies to production"
check_appset deploy/argocd/applicationset-qa.yaml appset-qa qa values-qa.yaml qa
grep -q 'atpost-services-prod\|atpost-web-prod\|atpost-services-staging\|atpost-web-staging' "$WORK/appset-qa.yaml" && bad "deploy/argocd/applicationset-qa.yaml: carries another environment's ApplicationSet"
grep -oE "name: '[a-z-]+\{\{" "$WORK/appset-qa.yaml" | grep -vqE "name: '(qa|web-qa)-" && bad "deploy/argocd/applicationset-qa.yaml: Application names must start qa- / web-qa-"
[[ $(find deploy/web -name values-prod.yaml | wc -l) -eq 2 ]] || bad "deploy/web must hold exactly the app and admin-console prod values"
[[ $(find deploy/web -name values-qa.yaml | wc -l) -eq 2 ]] || bad "deploy/web must hold exactly the app and admin-console QA values"
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
echo "── ${#PROD[@]} prod + ${#QA[@]} QA + ${#STAGING[@]} staging values files rendered ($(grep -c '' "$JOBS") renders in total) ──"

grep -q 'awsAccountId is required' "$WORK/out/neg-no-account.err" && ok "missing account id refused" || bad "missing account id: wrong refusal"
grep -q 'awsAccountId is required' "$WORK/out/neg-fake-account.err" && ok "short account id refused" || bad "short account id: wrong refusal"
grep -q 'awsAccountId is required' "$WORK/out/neg-token-account.err" && ok "unfilled ApplicationSet token refused" || bad "token account id: wrong refusal"
grep -q 'image.tag is required' "$WORK/out/neg-empty-tag.err" && ok "empty image tag refused" || bad "empty tag: wrong refusal"
grep -q 'worker.image.tag is required' "$WORK/out/neg-worker-tag.err" && ok "empty worker tag refused" || bad "empty worker tag: wrong refusal"
grep -q 'unfilled Terraform placeholder' "$WORK/out/neg-placeholder.err" && ok "unfilled __TF_*__ placeholder refused" || bad "placeholder: wrong refusal"

echo
echo "── per-file properties of the prod and QA renders ──"
for f in "${PROD[@]}" "${QA[@]}"; do
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
done < <(grep -hoE 'http://[a-z0-9-]+\.atpost\.svc\.cluster\.local:[0-9]+' "${PROD[@]}" "${QA[@]}" | sort -u)
[[ $url_bad -eq 0 ]] && ok "every in-cluster URL in prod and QA values uses its service's port"
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
echo "── QA renders ──"
QA_OUTS=()
for f in "${QA[@]}"; do QA_OUTS+=("$WORK/out/$(id_of "$f").out"); done
# Every rendered container (server, worker, init, migration job): ENV=prod,
# APP_ENV/DEPLOY_ENV=production — whatever path set them (commonEnv, env,
# worker.env, migrationJob.env).
hits=$(awk '$1 == "-" && $2 == "name:" && ($3 == "ENV" || $3 == "APP_ENV" || $3 == "DEPLOY_ENV") {
              n = $3; getline; v = $2; gsub(/"/, "", v)
              if (!((n == "ENV" && v == "prod") || (n != "ENV" && v == "production"))) print FILENAME ": " n "=" v; seen++ }
            END { if (seen < 30) print "only " seen + 0 " ENV/APP_ENV/DEPLOY_ENV entries rendered; the parser is probably broken" }' "${QA_OUTS[@]}")
[[ -z "$hits" ]] && ok "QA renders: every container runs ENV=prod, APP_ENV/DEPLOY_ENV=production" || bad "QA renders: non-production environment:"$'\n'"$hits"
hits=$(grep -nE '(name|secretKey): "?(PAYMENTS_ALLOW_STUB|OTP_BYPASS_CODE|CALLS_DEV_ALLOW_STUB_MEDIA)"?$' "${QA_OUTS[@]}" || true)
[[ -z "$hits" ]] && ok "QA renders: no PAYMENTS_ALLOW_STUB / OTP_BYPASS_CODE / stub media" || bad "QA renders: stub or bypass rendered:"$'\n'"$hits"
# Only the four QA hosts are served.
hits=$(grep -hoE '^ *- host: "?[^" ]+' "${QA_OUTS[@]}" | sed -E 's/.*host: "?//' | sort -u | grep -vxE '(qa|api-qa|ws-qa|admin-qa)\.cleestudio\.com' || true)
[[ -z "$hits" ]] && ok "QA renders: ingress hosts are qa., api-qa., ws-qa., admin-qa. only" || bad "QA renders: unexpected ingress host(s): $hits"
hits=$(grep -l '^kind: PodDisruptionBudget' "${QA_OUTS[@]}" || true)
[[ -z "$hits" ]] && ok "QA renders: no PodDisruptionBudget (one replica)" || bad "QA renders: PDB with one replica: $hits"
qpay="$(R "$(id_of deploy/services/payments-service/values-qa.yaml)")"
grep -q 'cidr: "10.40.0.0/16"' <<<"$qpay" || bad "QA payments: the ALB (QA VPC CIDR) is not allowed to reach the webhook"
grep -q 'host: "api-qa.cleestudio.com"' <<<"$qpay" || bad "QA payments: webhook is not on api-qa."
qgw=deploy/services/api-gateway/values-qa.yaml; qpay_f=deploy/services/payments-service/values-qa.yaml
{ [[ "$(ann $qgw 'alb.ingress.kubernetes.io/wafv2-acl-arn')" == "$(ann $qpay_f wafAclArn)" ]] &&
  [[ "$(ann $qgw 'alb.ingress.kubernetes.io/certificate-arn')" == "$(ann $qpay_f certificateArn)" ]] &&
  [[ "$(ann $qgw 'alb.ingress.kubernetes.io/group.name')" == "$(ann $qpay_f groupName)" ]] &&
  [[ "$(ann $qpay_f groupName)" == atpost-qa-api ]]; } \
  && ok "QA webhook joins atpost-qa-api with the API's certificate and WAF ACL" || bad "QA webhook / API ALB group wiring differs"
qweb="$(R "$(id_of deploy/web/app/values-qa.yaml)")"
{ grep -q 'host: "qa.cleestudio.com"' <<<"$qweb" && ! grep -q 'redirect-to-app' <<<"$qweb" && grep -q "atpost/web:$TAG" <<<"$qweb"; } \
  && ok "QA web: qa.cleestudio.com, no bare-domain redirect, atpost/web" || bad "QA web: host rules incomplete"
qadm="$(R "$(id_of deploy/web/admin-console/values-qa.yaml)")"
grep -q 'host: "admin-qa.cleestudio.com"' <<<"$qadm" && ok "QA admin console: admin-qa.cleestudio.com" || bad "QA admin console host missing"
for f in deploy/web/app/values-qa.yaml deploy/web/admin-console/values-qa.yaml deploy/services/chat-ws-gateway/values-qa.yaml; do
  [[ "$(ann $f 'alb.ingress.kubernetes.io/wafv2-acl-arn')" == "$(ann $qgw 'alb.ingress.kubernetes.io/wafv2-acl-arn')" ]] || bad "$f: QA ingress names a different WAF ACL (QA has one web ACL)"
done

echo
if [[ "$fail" -ne 0 ]]; then
  echo "RENDER CHECKS FAILED"
  exit 1
fi
echo "all render checks passed"
