#!/usr/bin/env bash
# dev-seed-mopedu.sh — DEV-ONLY seeder for the Mopedu Bengaluru pilot test.
#
# Talks to rider-service directly with the internal key and the identity
# headers api-gateway injects (X-User-Id, X-Scopes). It never logs in, never
# registers users, never sets a password and never prints a key.
#
#   bash scripts/dev-seed-mopedu.sh [--reset [--dry-run]] [--keep-online]
#
#   (no flag)          seed or top up; safe to re-run (idempotent)
#   --keep-online      after seeding, keep re-sending online + location pings
#                      for the synthetic captains every 30 s until Ctrl-C
#                      (the stale-GPS worker forces a captain offline 90 s
#                      after the last ping; run this in a second window)
#   --reset            remove exactly what this script created
#   --reset --dry-run  show what --reset would delete; change nothing
#
# What it seeds (Bengaluru, the city row migration 001 keeps):
#   * call_b becomes a Mopedu captain with NO admin step (launch safety):
#     partner row (individual_driver); Aadhaar through the DigiLocker MOCK
#     (start + callback), which also records the Aadhaar and driving-licence
#     documents as verified; an auto vehicle whose RC the mock returns
#     (vehicle verified); a selfie (profile_photo with a media_id) the mock
#     face compare verifies; the partner is approved automatically; and the
#     free trial (trial_7d) taken through POST /subscriptions/checkout — no
#     proof, no admin verify. Going online and the location pings are the
#     captain phone's job.
#   * three synthetic captains (name-based UUIDv5 user ids, NO identity rows)
#     with the same onboarding, set online near MG Road with a location ping,
#     so dispatch finds someone even when call_b is offline. Two drive an
#     auto, one a bike. They never accept an offer.
#   * coupon WELCOME50: 50% up to Rs 50, first ride only, once per user,
#     Bengaluru, active.
#   * fare window "Weekend test peak": Sat-Sun 09:00-21:00 local, x1.15,
#     Bengaluru, all vehicle types (the migration's peaks are Mon-Fri).
#   * nothing for call_a: it stays a plain customer.
#
# SQL-ONLY STEPS (no service route exists for them; dev only):
#   [sql-read]   ids the routes do not expose by name: the Bengaluru city id,
#                the trial plan id, the seeded coupon / window ids, partner
#                ids by user id.
#   [sql-reset]  --reset deletes by deterministic id: there is no DELETE
#                route for partners, coupons or fare windows (deactivate is
#                not removal). Redemptions of the seeded coupon go with it.
#   [redis]      --reset drops the synthetic captains from the online GEO
#                sets so a match pass never offers a deleted partner.
# Everything else goes through rider-service routes.
#
# Transport: rider-service publishes no host port on purpose (it trusts the
# gateway's identity headers), so every call runs `wget` INSIDE the
# rider-service container (alpine, BusyBox wget: GET and POST only, which is
# all this script needs). The internal key is expanded by the container's own
# shell from its environment; it never appears in a host argument list, a
# file or the output. On a non-2xx answer BusyBox wget discards the body, so
# failures report the status code only.
#
# Git Bash note: helpers assign through `printf -v` instead of `$(...)`
# wherever it is cheap; every subshell is a fork, and forks are slow here.
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'

# ─── Defaults ───────────────────────────────────────────────────────────────
CALL_A=2d598287-eee7-40b4-a7f5-b46b9412e4e7
CALL_B=66668bc2-a3f6-40a5-9cdd-c998dcf72f29
RESET=0 DRY_RUN=0 KEEP_ONLINE=0

RIDER_C=atpost_stack-rider-service-1
PG_C=atpost_stack-postgres-1
REDIS_C=atpost_stack-redis-1
GATEWAY_C=atpost_stack-api-gateway-1
RIDER_PORT=8116

# Name-based ids: UUIDv5 in the RFC 4122 DNS namespace.
NS_DNS=6ba7b810-9dad-11d1-80b4-00c04fd430c8
SEED_DOMAIN=mopedu-dev-seed.momentum.local

CITY_NAME=Bengaluru
PLAN_CODE=trial_7d   # the free trial, once per partner, through /subscriptions/checkout
COUPON_CODE=WELCOME50
WINDOW_NAME="Weekend test peak"
WINDOW_DAYS=96        # Sat=32 + Sun=64 (Mon=1 ... Sun=64)
WINDOW_START=540      # 09:00 local
WINDOW_END=1260       # 21:00 local
WINDOW_BPS=11500      # x1.15
WINDOW_PRIORITY=20    # above the seeded Night (5) and peak (10) windows

# call_b's captain profile. Synthetic registration numbers (ZZ series); the
# licence number comes from the DigiLocker mock.
CALL_B_NAME="Mopedu Test Captain"
CALL_B_PHONE=+919000000102
CALL_B_REG=KA01ZZ0102
KEEP_ONLINE_EVERY=30

# slug|full name|phone|vehicle type|registration|lat|lng  (near MG Road)
CAPTAIN_ROWS=$(cat <<'EOF'
ravi|Ravi Kumar|+919000000201|auto|KA01ZZ0201|12.9758|77.6045
suresh|Suresh Babu|+919000000202|auto|KA01ZZ0202|12.9722|77.6100
manju|Manjunath S|+919000000203|bike|KA01ZZ0203|12.9790|77.6010
EOF
)

usage() { sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --reset) RESET=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --keep-online) KEEP_ONLINE=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "unknown argument: $1" >&2; usage 2 ;;
  esac
done
[ "$DRY_RUN" = 0 ] || [ "$RESET" = 1 ] || { echo "ERROR: --dry-run only applies to --reset" >&2; exit 2; }
[ "$KEEP_ONLINE" = 0 ] || [ "$RESET" = 0 ] || { echo "ERROR: --keep-online and --reset are mutually exclusive" >&2; exit 2; }

die() { echo "ERROR: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*" >&2; }
log() { printf '   %s\n' "$*" >&2; }
warn() { printf '   WARN: %s\n' "$*" >&2; WARNINGS=$((WARNINGS + 1)); }
WARNINGS=0

# ─── Dev-stack guard ────────────────────────────────────────────────────────
for tool in docker sha1sum; do command -v "$tool" >/dev/null || die "$tool is required"; done
for c in "$RIDER_C" "$PG_C" "$REDIS_C"; do
  [ "$(docker inspect -f '{{.State.Running}}' "$c" 2>/dev/null || true)" = true ] || die "refusing: container $c is not running (local dev stack only)"
done

# dev compose leaves ENV unset on rider-service; anything named is refused
# unless it is local/dev.
RIDER_ENV=$(docker exec "$RIDER_C" printenv ENV 2>/dev/null || true)
case "${RIDER_ENV,,}" in
  ''|local|dev|development) ;;
  *) die "refusing: rider-service ENV is '$RIDER_ENV', expected unset, local or dev" ;;
esac
DSN_LOCAL=$(docker exec "$RIDER_C" sh -c 'case "$POSTGRES_DSN" in *@postgres:5432/app\?*|*@postgres:5432/app) echo yes;; *) echo no;; esac' 2>/dev/null || echo no)
[ "$DSN_LOCAL" = yes ] || die "refusing: rider-service is not on the compose postgres 'app' database"
HAS_KEY=$(docker exec "$RIDER_C" sh -c '[ -n "$INTERNAL_SERVICE_KEY" ] && echo yes || echo no' 2>/dev/null || echo no)
[ "$HAS_KEY" = yes ] || die "rider-service has no INTERNAL_SERVICE_KEY"
DIGILOCKER=$(docker exec "$RIDER_C" printenv DIGILOCKER_MODE 2>/dev/null || true)
[ "${DIGILOCKER:-mock}" = mock ] || die "refusing: rider-service DIGILOCKER_MODE is '$DIGILOCKER', expected mock"
# The selfie is verified by the face-compare MOCK on the dev stack; against a
# real media-service the seeded media ids do not exist and every captain
# would wait for a human.
FACE_MODE=$(docker exec "$RIDER_C" printenv MOPEDU_FACE_COMPARE_MODE 2>/dev/null || true)
[ "${FACE_MODE:-}" = mock ] || die "refusing: rider-service MOPEDU_FACE_COMPARE_MODE is '${FACE_MODE:-unset}', expected mock (dev compose sets it)"
COUPONS_ON=$(docker exec "$RIDER_C" printenv MOPEDU_COUPONS_ENABLED 2>/dev/null || true)
PII_SET=$(docker exec "$RIDER_C" sh -c '[ -n "$RIDER_PII_KEYS" ] && echo yes || echo no' 2>/dev/null || echo no)

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# ─── HTTP (BusyBox wget inside the rider-service container) ─────────────────
CODE='' BODY=''
# rider METHOD PATH USER BODY [admin]
# The body travels on stdin into the container; the key is read there from
# the container's own environment. BODY ends up "" on a non-2xx answer.
rider() {
  local method=$1 path=$2 user=$3 body=$4 scopes='' out
  [ "${5:-}" = admin ] && scopes=admin
  out=$(printf '%s' "$body" | docker exec -i "$RIDER_C" sh -c '
    m=$1; p=$2; u=$3; sc=$4; port=$5
    set -- -S -T 45 -O - --header "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" --header "Accept: application/json"
    [ -n "$u" ] && set -- "$@" --header "X-User-Id: $u"
    [ -n "$sc" ] && set -- "$@" --header "X-Scopes: $sc"
    if [ "$m" = POST ]; then
      cat >/tmp/mopedu-seed-body.$$
      set -- "$@" --header "Content-Type: application/json" --post-file /tmp/mopedu-seed-body.$$
    fi
    wget "$@" "http://localhost:$port$p" 2>/tmp/mopedu-seed-hdr.$$ || true
    code=$(sed -n "s/.*HTTP\/[0-9.]* \([0-9][0-9][0-9]\).*/\1/p" /tmp/mopedu-seed-hdr.$$ | tail -1)
    rm -f /tmp/mopedu-seed-body.$$ /tmp/mopedu-seed-hdr.$$
    printf "\n%s\n" "${code:-000}"' _ "$method" "$path" "$user" "$scopes" "$RIDER_PORT") || true
  out=${out%$'\n'}
  CODE=${out##*$'\n'}
  BODY=${out%$'\n'*}
  [ "$BODY" = "$CODE" ] && BODY=''
  return 0
}

check() { local c; for c in "$@"; do [ "$CODE" = "$c" ] && return 0; done; return 1; }

# fail prints the status and, when a body survived, the error envelope's
# code/message. rider-service error messages never echo keys.
fail() {
  local ec msg
  jget ec "$BODY" code
  jget msg "$BODY" message
  echo "FAILED: $* -> HTTP $CODE $ec $msg" >&2
  exit 1
}

# ─── JSON (no jq on this host) ──────────────────────────────────────────────
# jget VAR JSON KEY — the first "KEY":"value" string in the document, or "".
jget() {
  local _re="\"$3\":\"([^\"]*)\""
  printf -v "$1" '%s' ''
  if [[ $2 =~ $_re ]]; then printf -v "$1" '%s' "${BASH_REMATCH[1]}"; fi
}
# jnum VAR JSON KEY — the first "KEY":<number|true|false|null>, or "".
jnum() {
  local _re="\"$3\":(-?[0-9.]+|true|false|null)"
  printf -v "$1" '%s' ''
  if [[ $2 =~ $_re ]]; then printf -v "$1" '%s' "${BASH_REMATCH[1]}"; fi
}
# countv VAR HAYSTACK NEEDLE — occurrences of a literal substring.
countv() { local _s=$2 _n=0; while [[ $_s == *"$3"* ]]; do _s=${_s#*"$3"}; _n=$((_n + 1)); done; printf -v "$1" '%s' "$_n"; }

# ─── Ids ────────────────────────────────────────────────────────────────────
# compute_ids NAME... — UUIDv5 (DNS namespace) for every name in ONE sha1sum
# run; fills IDS[name].
declare -A IDS
compute_ids() {
  local ns=${NS_DNS//-/} esc='' i dir="$TMP/ids" h f v
  local -a names=("$@") files=()
  for ((i = 0; i < 32; i += 2)); do esc+="\\x${ns:i:2}"; done
  mkdir -p "$dir"
  for i in "${!names[@]}"; do
    { printf "$esc"; printf '%s' "${names[$i]}"; } >"$dir/$i"
    files+=("$dir/$i")
  done
  i=0
  while read -r h f; do
    printf -v v '%x' $(((0x${h:16:1} & 0x3) | 0x8))
    IDS[${names[$i]}]="${h:0:8}-${h:8:4}-5${h:13:3}-$v${h:17:3}-${h:20:12}"
    i=$((i + 1))
  done < <(sha1sum -- "${files[@]}")
  [ "$i" = "${#names[@]}" ] || die "id computation incomplete"
}

mapfile -t ROWS <<<"$CAPTAIN_ROWS"
declare -A UID_OF NAME PHONE VTYPE REG LAT LNG PID
SYNTH=()
for row in "${ROWS[@]}"; do
  IFS='|' read -r slug name phone vtype reg lat lng <<<"$row"
  SYNTH+=("$slug")
  NAME[$slug]=$name PHONE[$slug]=$phone VTYPE[$slug]=$vtype REG[$slug]=$reg LAT[$slug]=$lat LNG[$slug]=$lng
done
NAME[call_b]=$CALL_B_NAME PHONE[call_b]=$CALL_B_PHONE VTYPE[call_b]=auto REG[call_b]=$CALL_B_REG
ALL=(call_b "${SYNTH[@]}")

id_names=(python.org "$SEED_DOMAIN/admin")
for slug in "${SYNTH[@]}"; do id_names+=("$SEED_DOMAIN/user/$slug"); done
for slug in "${ALL[@]}"; do id_names+=("$SEED_DOMAIN/subscribe/$slug" "$SEED_DOMAIN/selfie/$slug"); done
compute_ids "${id_names[@]}"
[ "${IDS[python.org]}" = 886313e1-3b8a-5372-9b90-0c9aee199e5d ] || die "UUIDv5 self-check failed"
for slug in "${SYNTH[@]}"; do UID_OF[$slug]=${IDS[$SEED_DOMAIN/user/$slug]}; done
UID_OF[call_b]=$CALL_B
SEED_ADMIN=${IDS[$SEED_DOMAIN/admin]} # legacy admin actor (audit rows only); not an account

# ─── Local helpers (dev infra) ──────────────────────────────────────────────
psql_app() { docker exec -i "$PG_C" psql -U postgres -d app -tAqX -v ON_ERROR_STOP=1; }
# sqlv VAR SQL — one scalar from the app database. [sql-read]
sqlv() { local _v; _v=$(printf '%s\n' "$2" | psql_app); printf -v "$1" '%s' "$_v"; }
redis_zrem() { docker exec "$REDIS_C" redis-cli ZREM "$@" >/dev/null; }

# ─── Reset ──────────────────────────────────────────────────────────────────
# One transaction, everything by deterministic id: the synthetic captains
# (by user id), call_b's captain rows (only when no ride references the
# partner), the seeded coupon and its redemptions, the seeded window.
# rider_admin_audit_logs is append-only and is left.
do_reset() {
  if [ "$DRY_RUN" = 1 ]; then
    step "Reset DRY RUN: counting what would be deleted (rolled back)"
  else
    step "Reset: seeded captains, coupon and fare window (by deterministic id)"
  fi
  local values='' slug
  for slug in "${SYNTH[@]}"; do values+="('${UID_OF[$slug]}'),"; done
  values=${values%,}

  local sql
  sql=$(cat <<SQL
BEGIN;
CREATE TEMP TABLE seed_users(id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO seed_users VALUES $values;
-- call_b's captain rows go only when no ride ever referenced the partner.
INSERT INTO seed_users
  SELECT p.user_id FROM rider_partners p
  WHERE p.user_id = '$CALL_B'
    AND NOT EXISTS (SELECT 1 FROM rider_rides r WHERE r.partner_id = p.id)
ON CONFLICT DO NOTHING;
CREATE TEMP TABLE seed_partners(id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO seed_partners SELECT id FROM rider_partners WHERE user_id IN (SELECT id FROM seed_users);
CREATE TEMP TABLE seed_counts(t text, n bigint) ON COMMIT DROP;
INSERT INTO seed_counts SELECT 'call_b_partner_kept_(has_rides)', count(*) FROM rider_partners p
  WHERE p.user_id = '$CALL_B' AND EXISTS (SELECT 1 FROM rider_rides r WHERE r.partner_id = p.id);
INSERT INTO seed_counts SELECT 'synthetic_partner_kept_(has_rides)', count(*) FROM rider_partners p
  WHERE p.id IN (SELECT id FROM seed_partners) AND EXISTS (SELECT 1 FROM rider_rides r WHERE r.partner_id = p.id);
DELETE FROM seed_partners WHERE id IN (SELECT partner_id FROM rider_rides WHERE partner_id IS NOT NULL);
WITH d AS (DELETE FROM rider_ride_offers WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_ride_offers', count(*) FROM d;
WITH d AS (DELETE FROM rider_ride_track_points WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_ride_track_points', count(*) FROM d;
WITH d AS (DELETE FROM rider_partner_locations WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_partner_locations', count(*) FROM d;
WITH d AS (DELETE FROM rider_subscription_payments WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_subscription_payments', count(*) FROM d;
WITH d AS (DELETE FROM rider_partner_subscriptions WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_partner_subscriptions', count(*) FROM d;
WITH d AS (DELETE FROM rider_vehicle_documents WHERE vehicle_id IN (SELECT id FROM rider_vehicles WHERE partner_id IN (SELECT id FROM seed_partners)) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_vehicle_documents', count(*) FROM d;
WITH d AS (DELETE FROM rider_vehicles WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_vehicles', count(*) FROM d;
WITH d AS (DELETE FROM rider_partner_documents WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_partner_documents', count(*) FROM d;
WITH d AS (DELETE FROM rider_partner_aadhaar_verifications WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_partner_aadhaar_verifications', count(*) FROM d;
WITH d AS (DELETE FROM rider_doc_reminders_sent WHERE partner_id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_doc_reminders_sent', count(*) FROM d;
WITH d AS (DELETE FROM rider_partners WHERE id IN (SELECT id FROM seed_partners) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_partners', count(*) FROM d;
WITH d AS (DELETE FROM rider_idempotency WHERE user_id IN (SELECT id FROM seed_users) RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_idempotency', count(*) FROM d;
WITH d AS (DELETE FROM rider.identity_role_intents WHERE user_id IN (SELECT id FROM seed_users) AND user_id <> '$CALL_B' RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider.identity_role_intents_(synthetic)', count(*) FROM d;
WITH d AS (DELETE FROM rider_coupon_redemptions WHERE coupon_id IN (SELECT id FROM rider_coupons WHERE UPPER(code) = '$COUPON_CODE') RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_coupon_redemptions', count(*) FROM d;
WITH d AS (DELETE FROM rider_coupons WHERE UPPER(code) = '$COUPON_CODE' RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_coupons', count(*) FROM d;
WITH d AS (DELETE FROM rider_fare_windows WHERE name = '$WINDOW_NAME'
     AND city_id IN (SELECT id FROM rider_cities WHERE name = '$CITY_NAME') RETURNING 1)
  INSERT INTO seed_counts SELECT 'rider_fare_windows', count(*) FROM d;
SELECT t || '=' || n FROM seed_counts WHERE n > 0 ORDER BY t;
SQL
)
  if [ "$DRY_RUN" = 1 ]; then sql+=$'\n'"ROLLBACK;"; else sql+=$'\n'"COMMIT;"; fi
  local rows total=0 line verb=deleted
  [ "$DRY_RUN" = 1 ] && verb="would delete"
  rows=$(printf '%s\n' "$sql" | psql_app)
  for line in $rows; do
    case "$line" in *_kept_*) warn "${line%%=*}: ${line#*=} (left in place)"; continue ;; esac
    log "$verb $line"
    total=$((total + ${line#*=}))
  done
  log "rows $verb: $total"

  if [ "$DRY_RUN" = 1 ]; then
    log "would drop the synthetic captains from the Redis online GEO sets; nothing changed"
    return 0
  fi
  # [redis] a match pass must never offer a deleted partner.
  local city_id members=() slug pid
  sqlv city_id "SELECT id FROM rider_cities WHERE name = '$CITY_NAME' LIMIT 1;"
  for slug in "${SYNTH[@]}"; do
    pid=${PID[$slug]:-}
    [ -n "$pid" ] && members+=("$pid")
  done
  if [ ${#members[@]} -gt 0 ]; then
    [ -n "$city_id" ] && redis_zrem "rider:online:$city_id" "${members[@]}" || true
    redis_zrem "rider:online" "${members[@]}" || true
    log "redis online sets: removed ${#members[@]} synthetic partner id(s)"
  fi
  cat <<EOF

Reset done. Left in place on purpose:
   - the migrations' own seeds (cities, zones, plans, fare rules, the
     Mon-Fri peaks and the Night windows)
   - call_b's captain rows when a ride referenced the partner, and every
     ride, quote, payment, refund and outstanding row (test history)
   - rider_admin_audit_logs rows (append-only) and call_b's identity role
     intent (identity really granted rider_partner; revoke it there)
EOF
}

# ─── Seed stages ────────────────────────────────────────────────────────────
CITY_ID='' PLAN_ID=''
load_refs() {
  # [sql-read] the routes list cities and plans but not by name.
  sqlv CITY_ID "SELECT id FROM rider_cities WHERE name = '$CITY_NAME' AND is_active LIMIT 1;"
  [ -n "$CITY_ID" ] || die "no active city named $CITY_NAME (migrations not applied?)"
  sqlv PLAN_ID "SELECT id FROM rider_subscription_plans WHERE code = '$PLAN_CODE' AND is_active LIMIT 1;"
  [ -n "$PLAN_ID" ] || die "no active plan $PLAN_CODE"
  log "$CITY_NAME city_id=$CITY_ID, plan $PLAN_CODE id=$PLAN_ID"
}

# [sql-read] partner ids by user id (also for --reset, which needs them for
# the Redis cleanup before the rows go).
load_partner_ids() {
  local slug
  for slug in "${ALL[@]}"; do
    sqlv "PID[$slug]" "SELECT id FROM rider_partners WHERE user_id = '${UID_OF[$slug]}' AND deleted_at IS NULL LIMIT 1;"
  done
}

declare -A PSTATUS KYC
load_partner() { # slug -> PSTATUS/KYC/PID ("" when none)
  rider GET /v1/rider/partners/me "${UID_OF[$1]}" ''
  if check 404; then PSTATUS[$1]='' KYC[$1]='' PID[$1]=''; return 0; fi
  check 200 || fail "get partner ($1)"
  jget "PID[$1]" "$BODY" id
  jget "PSTATUS[$1]" "$BODY" status
  jget "KYC[$1]" "$BODY" kyc_status
}

CREATED=0 REUSED=0 DOCS=0 VEHICLES=0 SUBS=0 APPROVED=0 ONLINE=0
seed_captain() {
  local slug=$1 uid=${UID_OF[$1]} pid ids id body
  step "Captain $slug (${NAME[$slug]}, ${VTYPE[$slug]})"
  load_partner "$slug"
  if [ -z "${PID[$slug]}" ]; then
    body="{\"partner_type\":\"individual_driver\",\"full_name\":\"${NAME[$slug]}\",\"phone\":\"${PHONE[$slug]}\",\"city_id\":\"$CITY_ID\"}"
    rider POST /v1/rider/partners "$uid" "$body"
    check 201 200 || fail "create partner ($slug)"
    load_partner "$slug"
    [ -n "${PID[$slug]}" ] || die "partner $slug not readable after create"
    CREATED=$((CREATED + 1))
    log "created partner ${PID[$slug]} (${PSTATUS[$slug]})"
  else
    REUSED=$((REUSED + 1))
    log "reusing partner ${PID[$slug]} (${PSTATUS[$slug]}, kyc ${KYC[$slug]})"
  fi
  pid=${PID[$slug]}
  case "${PSTATUS[$slug]}" in
    suspended|blocked|rejected) die "$slug is ${PSTATUS[$slug]}; fix it in the console or --reset first" ;;
  esac

  # Aadhaar through the DigiLocker mock: the callback records the Aadhaar
  # and driving-licence documents as verified (source digilocker, verified
  # by "auto"). No upload, no admin.
  rider GET /v1/rider/partners/me/documents "$uid" ''
  check 200 || fail "list documents ($slug)"
  if [[ $BODY != *'"document_type":"aadhaar"'* ]]; then
    rider POST /v1/rider/partners/me/aadhaar/start "$uid" '{}'
    check 200 || fail "aadhaar start ($slug)"
    local state; jget state "$BODY" state
    [ -n "$state" ] || die "aadhaar start returned no state ($slug)"
    rider POST /v1/rider/partners/me/aadhaar/callback "$uid" "{\"code\":\"dev-seed-$slug\",\"state\":\"$state\"}"
    check 200 || fail "aadhaar callback ($slug)"
    DOCS=$((DOCS + 2))
    log "aadhaar + driving licence verified through the DigiLocker mock"
  fi

  # Vehicle: the RC is pulled from DigiLocker (mock) on creation, so the
  # vehicle is approved with no admin step.
  rider GET /v1/rider/partners/me/vehicles "$uid" ''
  check 200 || fail "list vehicles ($slug)"
  if [[ $BODY != *"\"registration_number\":\"${REG[$slug]}\""* ]]; then
    rider POST /v1/rider/partners/me/vehicles "$uid" "{\"vehicle_type\":\"${VTYPE[$slug]}\",\"registration_number\":\"${REG[$slug]}\",\"brand\":\"Bajaj\",\"model\":\"Dev seed\",\"color\":\"Yellow\",\"manufacture_year\":2022,\"fuel_type\":\"cng\"}"
    check 201 200 || fail "add vehicle ($slug)"
    VEHICLES=$((VEHICLES + 1))
    local vst; jget vst "$BODY" status
    [ "$vst" = approved ] || warn "vehicle ${REG[$slug]} is '$vst' (expected approved: RC from the DigiLocker mock)"
  fi

  # Selfie: an upload with a media_id; the face-compare mock verifies it
  # against the licence photo and the evaluator approves the partner.
  rider GET /v1/rider/partners/me/documents "$uid" ''
  check 200 || fail "list documents ($slug)"
  if [[ $BODY != *'"document_type":"profile_photo"'* ]]; then
    local media=${IDS[$SEED_DOMAIN/selfie/$slug]}
    rider POST /v1/rider/partners/me/documents "$uid" "{\"document_type\":\"profile_photo\",\"file_url\":\"media://$media\",\"media_id\":\"$media\"}"
    check 201 200 || fail "upload selfie ($slug)"
    DOCS=$((DOCS + 1))
  fi

  # Approval is automatic once Aadhaar, DL, selfie and one RC are verified.
  # The ONLY human fallback is a manually uploaded document; the seed never
  # uploads one, so anything but approved here is a bug to report.
  load_partner "$slug"
  rider GET /v1/rider/partners/me/onboarding "$uid" ''
  check 200 || fail "onboarding status ($slug)"
  local ob; jget ob "$BODY" status
  if [ "${PSTATUS[$slug]}" = approved ] && [ "${KYC[$slug]}" = approved ]; then
    APPROVED=$((APPROVED + 1))
  else
    die "$slug ended ${PSTATUS[$slug]}/${KYC[$slug]} (onboarding '$ob': $BODY); expected automatic approval"
  fi
  log "partner approved automatically (onboarding $ob)"

  # Subscription: the free trial through the checkout route (once per
  # partner, ever). No proof, no admin verify.
  rider GET /v1/rider/subscriptions/me "$uid" ''
  if check 404; then
    rider POST /v1/rider/subscriptions/checkout "$uid" "{\"plan_code\":\"$PLAN_CODE\"}"
    if check 200; then
      SUBS=$((SUBS + 1))
      local st; jget st "$BODY" status
      log "subscription $PLAN_CODE $st (checkout, no admin)"
    elif check 409; then
      warn "$slug already used the free trial and has no active subscription; pay a plan from the captain app (UPI test step) or --reset"
    else
      fail "checkout $PLAN_CODE ($slug)"
    fi
  else
    check 200 || fail "get subscription ($slug)"
  fi
}

# ping SLUG — online + one location fix (Postgres mirror + Redis GEO).
ping_captain() {
  local slug=$1 uid=${UID_OF[$1]}
  rider POST /v1/rider/partners/me/online "$uid" ''
  check 200 || fail "go online ($slug)"
  rider POST /v1/rider/partners/me/location "$uid" "{\"lat\":${LAT[$slug]},\"lng\":${LNG[$slug]},\"speed\":0,\"heading\":90}"
  check 200 || fail "location ping ($slug)"
}

seed_online() {
  step "Synthetic captains online near MG Road"
  local slug
  for slug in "${SYNTH[@]}"; do
    ping_captain "$slug"
    ONLINE=$((ONLINE + 1))
  done
  log "online with a fresh fix: $ONLINE (the stale-GPS worker forces them offline 90 s after the last ping; see --keep-online)"
}

COUPON_STATE='' WINDOW_STATE=''
seed_coupon() {
  step "Coupon $COUPON_CODE"
  local id active
  sqlv id "SELECT id FROM rider_coupons WHERE UPPER(code) = '$COUPON_CODE' LIMIT 1;"
  if [ -n "$id" ]; then
    sqlv active "SELECT is_active FROM rider_coupons WHERE id = '$id';"
    COUPON_STATE="existing (active=$active)"
    log "exists ($id, active=$active); not touched"
    return 0
  fi
  rider POST /v1/rider/admin/coupons "$SEED_ADMIN" "{\"code\":\"$COUPON_CODE\",\"description\":\"Dev seed: 50% off your first Mopedu ride, up to Rs 50\",\"discount_type\":\"percent\",\"percent_bps\":5000,\"max_discount_paise\":5000,\"min_fare_paise\":0,\"city_id\":\"$CITY_ID\",\"first_ride_only\":true,\"per_user_limit\":1,\"total_limit\":0,\"is_active\":true}" admin
  check 201 200 || fail "create coupon"
  jget id "$BODY" id
  COUPON_STATE=created
  log "created $id (percent 50%, cap Rs 50, first ride only)"
}

seed_window() {
  step "Fare window '$WINDOW_NAME' (Sat-Sun 09:00-21:00 x1.15, $CITY_NAME)"
  local id
  sqlv id "SELECT id FROM rider_fare_windows WHERE city_id = '$CITY_ID' AND name = '$WINDOW_NAME' LIMIT 1;"
  if [ -n "$id" ]; then
    local active; sqlv active "SELECT is_active FROM rider_fare_windows WHERE id = '$id';"
    WINDOW_STATE="existing (active=$active)"
    log "exists ($id, active=$active); not touched"
    return 0
  fi
  rider POST /v1/rider/admin/fare-windows "$SEED_ADMIN" "{\"city_id\":\"$CITY_ID\",\"vehicle_type\":null,\"name\":\"$WINDOW_NAME\",\"days_of_week\":$WINDOW_DAYS,\"start_minute\":$WINDOW_START,\"end_minute\":$WINDOW_END,\"multiplier_bps\":$WINDOW_BPS,\"priority\":$WINDOW_PRIORITY,\"is_active\":true}" admin
  check 201 200 || fail "create fare window"
  jget id "$BODY" id
  WINDOW_STATE=created
  log "created $id"
}

verify() {
  step "Verify (rider-service routes as the gateway would call them)"
  local slug n
  rider GET "/v1/rider/coupons/validate?code=$COUPON_CODE&city_id=$CITY_ID" '' ''
  if check 200; then V_COUPON=valid; else jget V_COUPON "$BODY" code; V_COUPON="HTTP $CODE ${V_COUPON:-}"; fi
  rider GET "/v1/rider/admin/fare-windows?city_id=$CITY_ID" "$SEED_ADMIN" '' admin
  check 200 || fail "list fare windows"
  countv n "$BODY" "\"name\":\"$WINDOW_NAME\""
  V_WINDOW="$n row(s) named '$WINDOW_NAME'"
  rider GET "/v1/rider/admin/partners?status=approved&limit=500" "$SEED_ADMIN" '' admin
  check 200 || fail "list approved partners"
  n=0
  for slug in "${ALL[@]}"; do [[ $BODY == *"${UID_OF[$slug]}"* ]] && n=$((n + 1)); done
  V_APPROVED="$n of ${#ALL[@]} seeded captains approved"
  # [sql-read] no route counts online partners by id.
  local ids='' ; for slug in "${SYNTH[@]}"; do ids+="'${PID[$slug]}',"; done
  sqlv V_ONLINE "SELECT count(*) FROM rider_partner_locations WHERE is_online AND partner_id IN (${ids%,});"
  rider GET "/v1/rider/admin/surge?city_id=$CITY_ID" "$SEED_ADMIN" '' admin
  check 200 && V_SURGE=ok || V_SURGE="HTTP $CODE"
}

# ─── Keep-online loop ───────────────────────────────────────────────────────
keep_online() {
  step "Keeping the synthetic captains online (a ping every ${KEEP_ONLINE_EVERY} s; Ctrl-C to stop)"
  local slug n=0
  trap 'echo; log "stopped after $n round(s); the stale-GPS worker takes them offline within 90 s"; exit 0' INT TERM
  while :; do
    for slug in "${SYNTH[@]}"; do ping_captain "$slug"; done
    n=$((n + 1))
    printf '   %s round %s: %s captains pinged\n' "$(date +%H:%M:%S)" "$n" "${#SYNTH[@]}" >&2
    sleep "$KEEP_ONLINE_EVERY"
  done
}

# ─── Main ───────────────────────────────────────────────────────────────────
echo "Mopedu dev seed — rider-service in $RIDER_C (ENV=${RIDER_ENV:-unset}, digilocker mock, coupons ${COUPONS_ON:-unset}, RIDER_PII_KEYS set: $PII_SET)"

deadline=$((SECONDS + 60))
until docker exec "$RIDER_C" wget -q -T 5 -O /dev/null "http://localhost:$RIDER_PORT/healthz" 2>/dev/null; do
  ((SECONDS < deadline)) || die "rider-service /healthz did not answer within 60 s"
  sleep 3
done
echo "healthz: 200"

# The dev containers may still run the image from before the pricing and
# payments lanes; that image has no fare-window route (404) and none of the
# tables this script seeds.
rider GET /v1/rider/admin/fare-windows "$SEED_ADMIN" '' admin
check 200 || die "rider-service answers HTTP $CODE on /v1/rider/admin/fare-windows: rebuild it first (docs/runbooks/mopedu-weekend-test.md, step 2)"

GATE=$(docker exec "$GATEWAY_C" printenv RIDER_PUBLIC_ENABLED 2>/dev/null || true)
printf 'gateway RIDER_PUBLIC_ENABLED: %s\n' "${GATE:-MISSING (phones get 404 on /v1/rider until it is true)}"
[ "${COUPONS_ON:-}" = true ] || warn "MOPEDU_COUPONS_ENABLED is '${COUPONS_ON:-unset}': $COUPON_CODE will validate as COUPONS_DISABLED"
[ "$PII_SET" = yes ] || warn "RIDER_PII_KEYS is empty: offers cannot be accepted (OTP_SEALING_NOT_CONFIGURED)"

load_partner_ids
if [ "$RESET" = 1 ]; then
  do_reset
  exit 0
fi

load_refs
for slug in "${ALL[@]}"; do seed_captain "$slug"; done
seed_online
seed_coupon
seed_window
verify

step "Summary"
printf '   %-18s created=%s reused=%s (call_b + %s synthetic)\n' Captains "$CREATED" "$REUSED" "${#SYNTH[@]}"
printf '   %-18s documents=%s vehicles=%s subscriptions=%s approvals=%s\n' Onboarding "$DOCS" "$VEHICLES" "$SUBS" "$APPROVED"
printf '   %-18s pinged=%s online_now=%s\n' "Synthetic online" "$ONLINE" "$V_ONLINE"
printf '   %-18s %s; validate: %s\n' "Coupon $COUPON_CODE" "$COUPON_STATE" "$V_COUPON"
printf '   %-18s %s; %s\n' "Weekend window" "$WINDOW_STATE" "$V_WINDOW"
printf '   %-18s %s; surge route: %s\n' "Approved" "$V_APPROVED" "$V_SURGE"
printf '   %-18s %s\n' Warnings "$WARNINGS"
for slug in "${ALL[@]}"; do printf '   %-18s user=%s partner=%s\n' "$slug" "${UID_OF[$slug]}" "${PID[$slug]}"; done
cat <<EOF

Next manual steps
   1. Second Git Bash window, for the whole test session:
        bash $0 --keep-online
      (without it the three synthetic captains drop offline after 90 s).
   2. call_b: sign in to the Mopedu Captain app, go online near MG Road.
   3. call_a: open Mopedu in Momentum, estimate, book. Walkthrough and SQL
      checks: docs/runbooks/mopedu-weekend-test.md
EOF

if [ "$KEEP_ONLINE" = 1 ]; then keep_online; fi
