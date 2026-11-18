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
#   7. Alerts and probes (feed-10, 16 Nov 2026):
#        static (no Docker):
#        - the chart's probe defaults are /livez and /readyz, no service
#          values file overrides them, and each service's Go code serves
#          both (the services that do not are listed in KNOWN_PROBE_GAPS and
#          printed on every run; a new gap, or a fixed one still listed,
#          fails);
#        - the default HTTP alert rules are on exactly for the services that
#          record atpost_<service>_http_* (metrics.NewHTTPMetrics("<name>"))
#          and the switch is the same in every values file of a service;
#        - the feed rules are the same list in values-prod.yaml and
#          values-qa.yaml and carry the stall, lag and dead-letter alerts;
#        - the same for the notification-service fan-out rules and the
#          post-service schedule, pending-video, outbox and dead-letter rules
#          (lv-21), and every metric they name is registered in that
#          service's Go code;
#        rendered:
#        - every prod and QA render has a PrometheusRule with the pod and
#          readiness rules, and the 5xx / p99 rules against the service's own
#          metric names when they are on, absent when they are off;
#        - every rendered probe path of a Go service is /livez or /readyz;
#        - defaultAlerts.enabled=false renders no PrometheusRule; thresholds
#          follow the values.
#
#   QA_GUARDS_ONLY=1 ./charts/atpost-service/render_test.sh
#      runs only the static guards (the QA guards and the static half of 7;
#      no Docker): a quick check while editing values files. The full run is
#      the gate.
#
# Helm: with HELM_BIN set (or `helm` on PATH and no Docker requested) the
# renders run with that binary; CI (.github/workflows/chart-render.yml) installs
# a pinned, checksum-verified helm and uses this path. Otherwise they run
# through the official image (one container for all renders).
#   HELM_BIN=/path/to/helm ./charts/atpost-service/render_test.sh
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
  # One exemption (lv-67): post-service's ACTION_LINK_OWN_DOMAINS is the
  # registrable domain whose host AND SUBDOMAINS count as in-app links
  # (actionlinks.ParseOwnDomains / IsOwnDomain). It names no prod host: QA's
  # qa./api-qa. hosts are subdomains of it, so QA needs the same value, and
  # the launch-switch check below still holds it equal to prod's. Only that
  # key, with exactly that value, is exempt.
  hits=$(grep -nE '(^|[^a-z0-9.-])((app|api|ws|admin|media)\.)?cleestudio\.com' "${QA_ALL[@]}" \
    | grep -vE "^deploy/services/post-service/values-qa\\.yaml:[0-9]+:  ACTION_LINK_OWN_DOMAINS: $QUOTE?cleestudio\\.com$QUOTE?\$" || true)
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

# ── alert and probe static guards (no Docker) ──────────────────────────────
# deploy service name → the directory its Go code lives in.
src_of() {
  case "$1" in
    identity-auth-service)    echo identity-platform/services/auth-service ;;
    identity-profile-service) echo identity-platform/services/profile-service ;;
    identity-user-service)    echo identity-platform/services/user-service ;;
    chat-call-service)        echo chat-service/services/call-service ;;
    chat-message-service)     echo chat-service/services/message-service ;;
    chat-ws-gateway)          echo chat-service/services/ws-gateway ;;
    *)                        echo "Architecture/services/$1" ;;
  esac
}
# serves <source dir> <path>: non-test Go code registers the path itself, or
# calls RegisterRoutes of shared/health (which serves /livez, /healthz and
# /readyz: Architecture/shared/health/checker.go), or uses the health
# package of its own tree (identity-platform/shared/health,
# chat-service/shared/health), which serves /livez and /readyz.
serves() {
  local lit shared own
  lit=$(grep -rlF --include='*.go' "\"$2\"" "$1" 2>/dev/null | grep -v '_test\.go$' | head -1 || true)
  [[ -n "$lit" ]] && return 0
  shared=$(grep -rlE --include='*.go' '^[^/]*checker\.RegisterRoutes\(' "$1" 2>/dev/null | grep -v '_test\.go$' \
    | xargs -r grep -lF 'github.com/atpost/shared/health"' 2>/dev/null | head -1 || true)
  [[ -n "$shared" ]] && return 0
  own=$(grep -rlE --include='*.go' '"github.com/atpost/(identity|chat)-shared/health"' "$1" 2>/dev/null | grep -v '_test\.go$' | head -1 || true)
  [[ -n "$own" ]] || return 1
  case "$1" in
    identity-platform/*) grep -qF "\"$2\"" identity-platform/shared/health/health.go ;;
    chat-service/*)      grep -qF "\"$2\"" chat-service/shared/health/health.go ;;
    *)                   return 1 ;;
  esac
}
# "<off|on>" for the default HTTP alert rules of one values file: off when
# the file sets defaultAlerts.http.enabled: false.
http_alerts_of() {
  tr -d '\r' < "$1" | awk '/^defaultAlerts:/ { d = 1; next } /^[^ #]/ { d = 0 }
       d && /^  [a-z]+:/ { h = ($1 == "http:") }
       d && h && /^    enabled: false/ { off = 1 }
       END { print (off ? "off" : "on") }'
}
# Probe paths a service's code does not serve today. The kubelet restarts a
# pod whose liveness path answers 404 and never routes to one whose readiness
# path does, so each entry is a service that cannot run on this chart until
# its code registers the path (or its values override the probe). Listed so
# the gap is printed on every run instead of failing a gate nobody can turn
# green from this folder; remove an entry when the service is fixed (a stale
# entry fails).
KNOWN_PROBE_GAPS=" "
alert_probe_static_guards() {
  echo
  echo "── alert and probe guards (static) ──"
  local f d svc src p key hits gaps="" probefail=0 httpfail=0 state want
  local values=charts/atpost-service/values.yaml

  # Chart defaults.
  [[ "$(tr -d '\r' < $values | awk '/^livenessProbe:/{p=1;next} p&&/^  path:/{print $2; exit}')" == /livez ]] || { bad "chart: livenessProbe.path default is not /livez"; probefail=1; }
  [[ "$(tr -d '\r' < $values | awk '/^readinessProbe:/{p=1;next} p&&/^  path:/{print $2; exit}')" == /readyz ]] || { bad "chart: readinessProbe.path default is not /readyz"; probefail=1; }
  # No Go service overrides a probe; the two web apps name their own page.
  hits=$(grep -nE '^(livenessProbe|readinessProbe):' deploy/services/*/values-*.yaml || true)
  [[ -z "$hits" ]] || { bad "a service values file overrides a probe (services use the chart's /livez and /readyz):"$'\n'"$hits"; probefail=1; }
  for f in deploy/web/app/values-prod.yaml deploy/web/app/values-qa.yaml; do
    [[ "$(tr -d '\r' < "$f" | awk '/^(livenessProbe|readinessProbe):/{p=1;next} p&&/^  path:/{print $2; p=0}' | sort -u)" == /login ]] || { bad "$f: probes must both be /login"; probefail=1; }
  done
  for f in deploy/web/admin-console/values-prod.yaml deploy/web/admin-console/values-qa.yaml; do
    [[ "$(tr -d '\r' < "$f" | awk '/^(livenessProbe|readinessProbe):/{p=1;next} p&&/^  path:/{print $2; p=0}' | sort -u)" == /api/health ]] || { bad "$f: probes must both be /api/health"; probefail=1; }
  done

  for d in deploy/services/*/; do
    svc="$(basename "$d")"; src="$(src_of "$svc")"
    [[ -d "$src" ]] || { bad "$svc: source directory $src not found (src_of in this script)"; probefail=1; continue; }
    for p in /livez /readyz; do
      key="$svc:$p"
      if serves "$src" "$p"; then
        [[ "$KNOWN_PROBE_GAPS" == *" $key "* ]] && { bad "$svc now serves $p: remove $key from KNOWN_PROBE_GAPS"; probefail=1; }
      elif [[ "$KNOWN_PROBE_GAPS" == *" $key "* ]]; then
        gaps+=" $key"
      else
        bad "$svc: the chart probes $p but $src does not serve it"; probefail=1
      fi
    done

    # Default HTTP alert rules: on exactly when the service records the
    # shared HTTP metrics under its own deploy name.
    want=off
    grep -rqF --include='*.go' "NewHTTPMetrics(\"$svc\")" "$src" 2>/dev/null && want=on
    # The identity and chat trees have their own metrics package, which
    # records the same series under the name the service passes it.
    if grep -rqE --include='*.go' '"github.com/atpost/(identity-shared/o11y|chat-shared)/metrics"' "$src" 2>/dev/null \
       && grep -rqE --include='*.go' "ServiceName = \"$svc\"" "$src" 2>/dev/null; then
      want=on
    fi
    for f in "$d"values-*.yaml; do
      state="$(http_alerts_of "$f")"
      if [[ "$state" != "$want" ]]; then
        if [[ "$want" == off ]]; then
          bad "$f: default HTTP alerts are on but $src never calls metrics.NewHTTPMetrics(\"$svc\"): set defaultAlerts.http.enabled: false"
        else
          bad "$f: default HTTP alerts are off but $src records atpost_${svc//-/_}_http_*: remove the switch"
        fi
        httpfail=1
      fi
    done
  done
  [[ $probefail -eq 0 ]] && ok "probes: chart defaults /livez + /readyz, no service override, every service outside the known gaps serves both"
  if [[ -n "$gaps" ]]; then
    echo "KNOWN GAP (not a failure; these pods cannot pass their probes until fixed):"
    for key in $gaps; do echo "       ${key%%:*} does not serve ${key##*:}"; done
  fi
  [[ $httpfail -eq 0 ]] && ok "default HTTP alerts: on exactly where metrics.NewHTTPMetrics(\"<service>\") is called, same switch in every values file"

  # Feed rules: one list, in prod and in QA.
  local fp=deploy/services/feed-service/values-prod.yaml fq=deploy/services/feed-service/values-qa.yaml a
  if [[ "$(tr -d '\r' < $fp | sed -n '/^prometheusRule:/,$p')" == "$(tr -d '\r' < $fq | sed -n '/^prometheusRule:/,$p')" ]]; then
    ok "feed rules: values-qa.yaml carries the same list as values-prod.yaml"
  else
    bad "feed rules differ between $fp and $fq (keep the two lists identical)"
  fi
  for a in FeedConsumerStalled FeedConsumerLagHigh FeedConsumerLagCritical FeedEventsDeadLettered; do
    grep -q "^    - alert: $a" "$fp" || bad "$fp: alert $a is missing"
  done
  # The lag gauge and the two counters the stall rule reads must be the names
  # the shared consumer exports.
  for a in kafka_consumer_lag kafka_messages_errors_total kafka_messages_processed_total kafka_dlq_messages_total; do
    grep -q "Name: \"$a\"" Architecture/shared/o11y/metrics/kafka.go || bad "feed rules read atpost_feed_service_$a but shared/o11y/metrics/kafka.go no longer exports $a"
    grep -q "atpost_feed_service_$a" "$fp" || bad "$fp: no rule reads atpost_feed_service_$a"
  done

  # lv-21 (18 Nov 2026): the upload fan-out (notification-service) and the
  # schedule publisher, pending-video, outbox and dead-letter rules
  # (post-service): one list in prod and QA, the named alerts present, and
  # every metric an expression or annotation names registered by the
  # service's Go code (or the shared Kafka consumer metrics it records).
  local spec s list m short src hit
  for spec in \
    "notification-service:NotificationFanoutJobStuck NotificationFanoutJobsFailing NotificationFanoutTriggersLost NotificationEventsDeadLettered" \
    "post-service:PostScheduledPublishOverdue PostVideoPendingTooLong PostOutboxRelayStuck PostEventsDeadLettered"; do
    s="${spec%%:*}"; list="${spec#*:}"
    fp=deploy/services/$s/values-prod.yaml; fq=deploy/services/$s/values-qa.yaml
    if [[ "$(tr -d '\r' < $fp | sed -n '/^prometheusRule:/,$p')" == "$(tr -d '\r' < $fq | sed -n '/^prometheusRule:/,$p')" ]]; then
      ok "$s rules: values-qa.yaml carries the same list as values-prod.yaml"
    else
      bad "$s rules differ between $fp and $fq (keep the two lists identical)"
    fi
    for a in $list; do
      grep -q "^    - alert: $a\$" "$fp" || bad "$fp: alert $a is missing"
    done
    src="$(src_of "$s")"
    for m in $(tr -d '\r' < "$fp" | sed -n '/^prometheusRule:/,$p' \
               | grep -oE "atpost_${s//-/_}_[a-z0-9_]+|post_video_gate_[a-z0-9_]+" | sort -u); do
      short="${m#atpost_${s//-/_}_}"; short="${short%_bucket}"; short="${short%_count}"; short="${short%_sum}"
      hit=$(grep -rlE --include='*.go' "Name:[[:space:]]*\"$short\"" "$src" Architecture/shared/o11y/metrics/kafka.go 2>/dev/null \
            | grep -v '_test\.go$' | head -1 || true)
      [[ -n "$hit" ]] || bad "$fp: a rule reads $m but $src registers no metric named $short"
    done
  done
  return 0
}
alert_probe_static_guards

if [[ "${QA_GUARDS_ONLY:-}" == 1 ]]; then
  echo
  [[ "$fail" -ne 0 ]] && { echo "STATIC GUARDS FAILED"; exit 1; }
  echo "static guards passed (QA + alerts/probes; run without QA_GUARDS_ONLY for the renders)"
  exit 0
fi

for f in "${PROD[@]}"; do job "$(id_of "$f")" ok "-f $f $LOCAL"; done
for f in "${STAGING[@]}"; do job "$(id_of "$f")" ok "-f $f --set-string global.awsAccountId=$ACCT --set global.allowPlaceholders=true"; done
for f in "${QA[@]}"; do job "$(id_of "$f")" ok "-f $f $LOCAL"; done
# The Azure media overlays (REG-1 / REG-2): they must at least render, and the
# worker grace checks below read them.
AZURE_MEDIA=(deploy/services/media-service/values-azure-staging.yaml deploy/services/media-service/values-azure-prod.yaml)
for f in "${AZURE_MEDIA[@]}"; do job "$(id_of "$f")" ok "-f $f $LOCAL"; done


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
job alerts-off          ok  "-f $G $BASE --set defaultAlerts.enabled=false"
job alerts-thresholds   ok  "-f $G $BASE --set defaultAlerts.http.errorRatio=0.2 --set defaultAlerts.http.p99Seconds=2.5 --set defaultAlerts.pods.restartsPer30m=7"

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
if [[ -z "${HELM_BIN:-}" ]] && ! command -v docker >/dev/null 2>&1 && command -v helm >/dev/null 2>&1; then
  HELM_BIN="$(command -v helm)"
fi
if [[ -n "${HELM_BIN:-}" ]]; then
  # Local helm: same jobs, with /wkout pointing at the work directory.
  echo "rendering with $("$HELM_BIN" version --short 2>/dev/null || echo "$HELM_BIN")"
  while IFS='|' read -r id expect args; do
    [[ -z "$id" ]] && continue
    args="${args//\/wkout\//$WORK/out/}"
    # shellcheck disable=SC2086
    if "$HELM_BIN" template rel ./charts/atpost-service $args > "$WORK/out/$id.out" 2> "$WORK/out/$id.err"; then
      echo 0 > "$WORK/out/$id.rc"
    else
      echo 1 > "$WORK/out/$id.rc"
    fi
  done < "$JOBS"
else
  MSYS_NO_PATHCONV=1 docker run --rm \
    -v "$(host_path "$REPO_ROOT"):/wk" -v "$(host_path "$WORK/out"):/wkout" -w /wk \
    --entrypoint sh "$HELM_IMAGE" /wkout/run.sh
fi

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
[[ $(grep -c '^kind: Deployment' <<<"$media") -eq 3 ]] && ok "media-service renders server + short and long workers" || bad "media-service: want 3 Deployments"
grep -q 'name: media-service-worker-short' <<<"$media" && grep -q 'name: media-service-worker-long' <<<"$media" || bad "media: short/long worker Deployments missing"
grep -q 'karpenter.sh/do-not-disrupt: "true"' <<<"$media" || bad "media worker is not protected from Karpenter disruption"
grep -q '^kind: PodMonitor' <<<"$media" || bad "media worker metrics are not scraped"
grep -q 'alert: WorkerPodCrashLooping' <<<"$media" || bad "media worker has no alerts"
[[ $(grep -c 'atpost.io/worker-class' <<<"$media") -ge 6 ]] || bad "worker class selectors incomplete"
[[ $(grep -c 'app.kubernetes.io/component: worker' <<<"$media") -ge 3 ]] || bad "worker selector/labels incomplete — the two Deployments may select the same pods"
grep -q "atpost/media-worker:$TAG" <<<"$media" || bad "worker does not use its own image repository at the CI tag"
[[ $(grep -c '^kind: Service$' <<<"$media") -eq 1 ]] || bad "worker must not publish a Service"
grep -q 'serviceAccountName: media-service' <<<"$media" || bad "worker does not reuse the media-service ServiceAccount (IRSA identity)"
grep -q '^ *command:$' <<<"$media" && bad "media worker renders a command it never set"
ok "worker: distinct name/selector/image, no Service, same IRSA identity, image CMD kept"

# reels-4: a worker class never has more replicas than its topic has
# partitions (the extra consumers sit idle in the group). The topics Job
# (infra/terraform/modules/msk/topics-job) creates every topic, media.events
# and media.events.long included, with ONE count, var.partitions, which each
# environment passes as msk_topic_partitions (envs/<env>/variables.tf
# default; a committed *.tfvars assignment would win).
# deploy_replicas <render> → "<deployment name> <replicas>" lines.
deploy_replicas() { awk '/^---/{k=""; n=""} /^kind: /{k=$2} k=="Deployment" && /^  name: /{n=$2} k=="Deployment" && /^  replicas: /{print n, $2}' <<<"$1"; }
tf_partitions() {
  local env="$1" v
  v=$(cat infra/terraform/envs/"$env"/*.tfvars 2>/dev/null | awk '/^[ \t]*msk_topic_partitions[ \t]*=/{gsub(/[^0-9]/, "", $0); print; exit}')
  [[ -n "$v" ]] || v=$(awk '/^variable "msk_topic_partitions"/{p=1} p && /default/{gsub(/[^0-9]/, "", $0); print; exit}' infra/terraform/envs/"$env"/variables.tf)
  echo "$v"
}
TOPICS_TF=infra/terraform/modules/msk/topics-job
for t in media.events media.events.long; do
  grep -qE "^[[:space:]]*\"${t//./\\.}\",?[[:space:]]*(#.*)?\$" $TOPICS_TF/variables.tf || bad "reels-4: topic $t is not in $TOPICS_TF/variables.tf"
done
grep -qE -- '--partitions \$\{var\.partitions\}' $TOPICS_TF/main.tf || bad "reels-4: $TOPICS_TF/main.tf no longer creates every topic with var.partitions; teach this guard the per-topic count"
for pair in prod:deploy/services/media-service/values-prod.yaml qa:deploy/services/media-service/values-qa.yaml; do
  env="${pair%%:*}"; f="${pair#*:}"
  parts="$(tf_partitions "$env")"
  if [[ ! "$parts" =~ ^[0-9]+$ ]]; then bad "reels-4: no msk_topic_partitions for $env"; continue; fi
  reps="$(deploy_replicas "$(R "$(id_of "$f")")")"
  for cls in short:media.events long:media.events.long; do
    n=$(awk -v d="media-service-worker-${cls%%:*}" '$1 == d {print $2}' <<<"$reps")
    if [[ ! "$n" =~ ^[0-9]+$ ]]; then bad "reels-4: $f renders no media-service-worker-${cls%%:*} replicas"
    elif (( n > parts )); then bad "reels-4: $f: media-service-worker-${cls%%:*} has $n replicas but ${cls#*:} has $parts partitions in $env"
    else ok "reels-4: $env media-service-worker-${cls%%:*} $n replicas <= $parts partitions of ${cls#*:}"; fi
  done
done

# REG-2: each worker Deployment carries its own grace period and scratch size
# in bytes as env, equal to its pod spec (the worker sizes its SIGTERM drain
# from MEDIA_WORKER_TERMINATION_GRACE_SECONDS, cmd/worker/drain.go).
# worker_env <render> <deployment> <NAME> → the value in that Deployment.
worker_env() { awk -v d="$2" -v e="$3" '/^---/{k=""; n=""} /^kind: /{k=$2} k=="Deployment" && /^  name: /{n=$2}
  n==d && $1=="-" && $2=="name:" && $3==e { getline; v=$2; gsub(/"/, "", v); print v; exit }' <<<"$1"; }
worker_grace() { awk -v d="$2" '/^---/{k=""; n=""} /^kind: /{k=$2} k=="Deployment" && /^  name: /{n=$2}
  n==d && /terminationGracePeriodSeconds:/ {print $2; exit}' <<<"$1"; }
for f in deploy/services/media-service/values-prod.yaml deploy/services/media-service/values-qa.yaml; do
  out="$(R "$(id_of "$f")")"
  for spec in short:600:53687091200 long:3600:85899345920; do
    IFS=: read -r cls grace bytes <<<"$spec"; d="media-service-worker-$cls"
    [[ "$(worker_grace "$out" "$d")" == "$grace" ]] || bad "REG-2: $f $d grace is not $grace"
    [[ "$(worker_env "$out" "$d" MEDIA_WORKER_TERMINATION_GRACE_SECONDS)" == "$grace" ]] || bad "REG-2: $f $d MEDIA_WORKER_TERMINATION_GRACE_SECONDS is not $grace"
    [[ "$(worker_env "$out" "$d" MEDIA_WORKER_SCRATCH_LIMIT_BYTES)" == "$bytes" ]] || bad "REG-2: $f $d MEDIA_WORKER_SCRATCH_LIMIT_BYTES is not $bytes"
  done
done
# Staging and Azure: one both-class worker on the chart's default grace; the
# env must equal whatever the pod spec says.
for f in "${STAGING[@]}" "${AZURE_MEDIA[@]}"; do
  [[ "$f" == deploy/services/media-service/* ]] || continue
  out="$(R "$(id_of "$f")")"; g="$(worker_grace "$out" media-service-worker)"
  [[ "$g" =~ ^[0-9]+$ && "$(worker_env "$out" media-service-worker MEDIA_WORKER_TERMINATION_GRACE_SECONDS)" == "$g" ]] \
    || bad "REG-2: $f: MEDIA_WORKER_TERMINATION_GRACE_SECONDS does not equal the worker's grace ($g)"
  [[ "$(worker_env "$out" media-service-worker MEDIA_WORKER_SCRATCH_LIMIT_BYTES)" =~ ^[0-9]+$ ]] || bad "REG-2: $f: no MEDIA_WORKER_SCRATCH_LIMIT_BYTES"
done
grep -q 'MEDIA_WORKER_TERMINATION_GRACE_SECONDS' <<<"$(R "$(id_of deploy/services/dating-service/values-prod.yaml)")" && bad "REG-2: dating worker got the media worker's limits env"
ok "REG-2: worker grace and scratch bytes rendered as env per class (prod, QA, staging, Azure)"
# N-1: the per-class queue rules collapse the pods per class (every worker
# reports both classes), and the dead pipeline-wide class="" rule is gone.
for f in deploy/services/media-service/values-prod.yaml deploy/services/media-service/values-qa.yaml; do
  out="$(R "$(id_of "$f")")"
  for cls in short long; do
    grep -qF "max by (class) (media_transcode_queue_oldest_waiting_seconds{class=\"$cls\"})" <<<"$out" || bad "N-1: $f: the $cls queue rule does not read max by (class)"
  done
  grep -qF 'class=""' <<<"$out" && bad "N-1: $f: a rule still reads the unlabelled queue gauge"
done
ok "N-1: queue-wait rules are per class, one series per class"
# REG-1: no media-service values file mounts the HS256 secret any more
# (internal/http/jwt_config.go refuses it in production; staging and Azure
# are RS256 only by JWT_DISABLE_HS256).
hits=$(grep -nE 'secretKey: *JWT_SECRET(_PREVIOUS)?\b' deploy/services/media-service/values-*.yaml || true)
[[ -z "$hits" ]] && ok "REG-1: no media-service values file mounts JWT_SECRET" || bad "REG-1: media-service mounts the HS256 secret:"$'\n'"$hits"

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
# With one replica a PDB blocks every node drain unless it allows one pod out.
# The only PDB a QA render may carry is maxUnavailable >= 1 with no
# minAvailable (the media worker PDBs, kept for parity with prod: lv-5).
hits=$(awk '/^---/{p=0} /^kind: PodDisruptionBudget/{p=1; n=""} p && /^  name: /{n=$2}
            p && /^  minAvailable:/ {print FILENAME ": " n " minAvailable " $2}
            p && /^  maxUnavailable:/ && ($2 + 0) < 1 {print FILENAME ": " n " maxUnavailable " $2}' "${QA_OUTS[@]}")
[[ -z "$hits" ]] && ok "QA renders: no drain-blocking PodDisruptionBudget (one replica; only maxUnavailable >= 1 worker budgets)" || bad "QA renders: drain-blocking PDB with one replica:"$'\n'"$hits"
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
echo "── alert rules and probe paths in the renders ──"
EXCL='path!~"/livez|/readyz|/healthz|/metrics"'
alertfail=0
for f in "${PROD[@]}" "${QA[@]}"; do
  id="$(id_of "$f")"; [[ -f "$WORK/out/$id.out" ]] || continue
  out="$(R "$id")"
  svc="$(basename "$(dirname "$f")")"
  # The PrometheusRule document alone.
  rule="$(awk '/^---/{p=0} /^kind: PrometheusRule/{p=1} p' <<<"$out")"
  [[ -n "$rule" ]] || { bad "$f: renders no PrometheusRule"; alertfail=1; continue; }
  for a in ServicePodCrashLooping ServicePodRestarting ServiceNoReadyPod ServiceReadinessFailing; do
    grep -q "alert: $a\$" <<<"$rule" || { bad "$f: default rule $a did not render"; alertfail=1; }
  done
  if grep -qE '\{\{|\}\}|<no value>|%!' <<<"$rule"; then bad "$f: an alert rule rendered template leftovers"; alertfail=1; fi
  grep -qF "container=\"$svc\"" <<<"$rule" || [[ "$f" == deploy/web/* ]] || { bad "$f: pod rules do not select container $svc"; alertfail=1; }
  [[ $(grep -c 'source: atpost-service' <<<"$rule") -eq $(grep -c -- '- alert: ' <<<"$rule") ]] \
    || { bad "$f: not every rule carries source: atpost-service (the Alertmanager route would drop it)"; alertfail=1; }
  if [[ "$f" == deploy/web/* || "$(http_alerts_of "$f")" == off ]]; then
    grep -q 'alert: ServiceHttp' <<<"$rule" && { bad "$f: HTTP rules rendered for a service that records no HTTP metrics"; alertfail=1; }
  else
    m="atpost_${svc//-/_}"
    grep -qF "${m}_http_requests_total{status=~\"5..\", $EXCL}[5m]" <<<"$rule" || { bad "$f: 5xx rule does not read ${m}_http_requests_total"; alertfail=1; }
    grep -qF "histogram_quantile(0.99, sum by (le) (rate(${m}_http_request_duration_seconds_bucket{$EXCL}[5m])))" <<<"$rule" || { bad "$f: p99 rule does not read ${m}_http_request_duration_seconds_bucket"; alertfail=1; }
    grep -q 'alert: ServiceMetricsTargetDown$' <<<"$rule" || { bad "$f: scrape-target rule did not render"; alertfail=1; }
  fi
  # Probe paths of the server container: /livez and /readyz for a Go service.
  if [[ "$f" != deploy/web/* ]]; then
    probes="$(awk '/^ +(livenessProbe|readinessProbe):/{k=$1; n=NR} /^ +path:/ && n && NR == n + 2 {print k $2}' <<<"$out" | sort -u | tr '\n' ' ')"
    [[ "$probes" == "livenessProbe:/livez readinessProbe:/readyz " ]] || { bad "$f: probes render as [$probes], want /livez and /readyz"; alertfail=1; }
  fi
done
[[ $alertfail -eq 0 ]] && ok "every prod and QA render: pod + readiness rules, HTTP rules on the service's own metrics where recorded, probes on /livez and /readyz"

for f in deploy/services/feed-service/values-prod.yaml deploy/services/feed-service/values-qa.yaml; do
  feed="$(R "$(id_of "$f")")"
  for a in FeedConsumerStalled FeedEventRetryStuck FeedConsumerLagHigh FeedConsumerLagCritical FeedEventsDeadLettered FeedFanoutIncomplete FeedRankerFallingBack FeedHydrationFailing FeedHomeLatencyHigh; do
    grep -q "alert: $a\$" <<<"$feed" || bad "$f: feed rule $a did not render"
  done
  grep -q 'name: feed-service.default$' <<<"$feed" && grep -q 'name: feed-service$' <<<"$feed" || bad "$f: want two rule groups (feed-service.default and feed-service)"
  grep -q 'severity: page' <<<"$feed" || bad "$f: a feed rule lost its own severity label"
done
ok "feed-service: stall, lag, dead-letter and the other feed rules render in prod and QA"

# lv-21: the fan-out and schedule rules render, in their own group, routed.
for spec in \
  "notification-service:NotificationFanoutJobStuck NotificationFanoutJobsFailing NotificationFanoutTriggersLost NotificationEventsDeadLettered" \
  "post-service:PostScheduledPublishOverdue PostVideoPendingTooLong PostOutboxRelayStuck PostEventsDeadLettered"; do
  s="${spec%%:*}"
  for f in deploy/services/$s/values-prod.yaml deploy/services/$s/values-qa.yaml; do
    out="$(R "$(id_of "$f")")"
    for a in ${spec#*:}; do
      grep -q "alert: $a\$" <<<"$out" || bad "$f: rule $a did not render"
    done
    grep -q "name: $s\$" <<<"$out" || bad "$f: the $s rule group did not render"
  done
done
ok "notification-service fan-out and post-service schedule/outbox/dead-letter rules render in prod and QA"

grep -q '^kind: PrometheusRule' "$WORK/out/alerts-off.out" && bad "defaultAlerts.enabled=false still renders a PrometheusRule" || ok "defaultAlerts.enabled=false renders no PrometheusRule"
thr="$(cat "$WORK/out/alerts-thresholds.out")"
{ grep -qF '1e-9) > 0.2' <<<"$thr" && grep -qF '[5m]))) > 2.5' <<<"$thr" && grep -qF '[30m])) > 7' <<<"$thr"; } \
  && ok "default alert thresholds follow the values" || bad "default alert thresholds did not follow --set"

echo
if [[ "$fail" -ne 0 ]]; then
  echo "RENDER CHECKS FAILED"
  exit 1
fi
echo "all render checks passed"
