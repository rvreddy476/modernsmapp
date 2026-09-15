#!/usr/bin/env bash
# dev-seed-dating.sh — DEV-ONLY seeder for the Dating (Pulse) internal pilot.
#
# Talks to dating-service directly on localhost with the internal key and the
# identity headers api-gateway injects (X-User-Id, X-Scopes). It never logs in,
# never registers users, never sets a password and never prints a key.
#
#   bash scripts/dev-seed-dating.sh [--reset [--dry-run]] [--dating-url URL] [--media-url URL]
#
#   (no flag)          seed or top up; safe to re-run
#   --reset            remove the seeded synthetic data and the seeded
#                      sparks/matches that involve call_a and call_b
#                      (their own dating profiles stay)
#   --reset --dry-run  show what --reset would delete; change nothing
#
# What it seeds: 20 synthetic adult profiles around Bengaluru (active, with a
# placeholder photo and a passed selfie), dating profiles for call_a and call_b
# when they have none, mutual sparks and matches with both, one call_a <-> call_b
# match, incoming sparks for each, one report and one panic incident from
# synthetic users. Premium is untouched. dating-service asks chat for each
# match's conversation itself (see the runbook for the current chat refusal).
#
# Synthetic user ids, photo ids and selfie-video ids are name-based UUIDv5
# values, so a re-run finds what the last run created. The synthetic users have
# no identity or auth rows at all: identity-profile answers 404 for them and
# dating falls back to the client birth date (the D2 interim rule).
#
# SQL-ONLY / BLOB-ONLY STEPS (no service route exists for them; dev only):
#   [sql-media]   media rows for the placeholder photo and selfie video. Upload
#                 needs a user JWT (media /v1/media/init|confirm), which this
#                 script never obtains. Rows are inserted ready + moderation
#                 passed by the mock scanner, after their objects exist.
#   [blob-media]  object bytes go to the local MinIO with `mc` inside the minio
#                 container, using that container's own root credentials.
#   [blob-marker] dating's photo prepare re-encodes the original, which strips
#                 the mock face marker; the marked placeholder is written back
#                 so the mock liveness check can compare faces.
#   [sql-read]    read-only lookups: which media rows exist; verification counts.
#   [sql-reset]   --reset deletes dating rows by deterministic id (the profile
#                 DELETE route is a soft delete that a re-seed cannot undo).
#   [redis]       deck caches for call_a/call_b are dropped so the verification
#                 deck is computed from the seeded data, not a stale cache.
# Everything else goes through dating-service routes: profiles, preferences,
# photo attach (media-service owner check, prepare, auto moderation), consent,
# selfie challenge + blink liveness, sparks (matches), report, panic, and the
# admin queue reads. Media purge on --reset uses media-service's internal
# dating-photo DELETE route.
#
# Secret handling: INTERNAL_SERVICE_KEYs are read with `docker exec ...
# printenv` into shell variables and reach curl only through its stdin config
# (-K -). MinIO credentials never leave the minio container. Nothing secret is
# written to a file, an argument list or the output.
#
# Git Bash note: helpers assign through `printf -v` instead of `$(...)`; every
# subshell is a fork, and forks are slow on Windows.
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'

# ─── Defaults ───────────────────────────────────────────────────────────────
CALL_A=2d598287-eee7-40b4-a7f5-b46b9412e4e7
CALL_B=66668bc2-a3f6-40a5-9cdd-c998dcf72f29
DATING_URL=http://localhost:8112
MEDIA_URL=http://localhost:8087
RESET=0 DRY_RUN=0

DATING_C=atpost_stack-dating-service-1
MEDIA_C=atpost_stack-media-service-1
MINIO_C=atpost_stack-minio-1
PG_C=atpost_stack-postgres-1
REDIS_C=atpost_stack-redis-1
GATEWAY_C=atpost_stack-api-gateway-1

# Name-based ids: UUIDv5 in the RFC 4122 DNS namespace.
NS_DNS=6ba7b810-9dad-11d1-80b4-00c04fd430c8
SEED_DOMAIN=dating-dev-seed.momentum.local

# Pilot accounts get a dating profile only when they have none. These are
# placeholders the founder can change in the app.
CALL_A_GENDER=male CALL_A_INTERESTED=female CALL_A_LAT=12.9352 CALL_A_LNG=77.6245 # Koramangala
CALL_B_GENDER=female CALL_B_INTERESTED=male CALL_B_LAT=12.9719 CALL_B_LNG=77.6412 # Indiranagar

usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --reset) RESET=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --dating-url) DATING_URL=${2:?}; shift 2 ;;
    --media-url) MEDIA_URL=${2:?}; shift 2 ;;
    -h|--help) usage 0 ;;
    *) echo "unknown argument: $1" >&2; usage 2 ;;
  esac
done
[ "$DRY_RUN" = 0 ] || [ "$RESET" = 1 ] || { echo "ERROR: --dry-run only applies to --reset" >&2; exit 2; }

die() { echo "ERROR: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*" >&2; }
log() { printf '   %s\n' "$*" >&2; }
warn() { printf '   WARN: %s\n' "$*" >&2; WARNINGS=$((WARNINGS + 1)); }
WARNINGS=0

# ─── Dev-stack guard ────────────────────────────────────────────────────────
is_local_url() { [[ $1 =~ ^http://(localhost|127\.0\.0\.1)(:[0-9]+)?/?$ ]]; }
is_local_url "$DATING_URL" || die "refusing: --dating-url must be http://localhost[:port]"
is_local_url "$MEDIA_URL" || die "refusing: --media-url must be http://localhost[:port]"
DATING_URL=${DATING_URL%/} MEDIA_URL=${MEDIA_URL%/}
for tool in docker curl sha1sum; do command -v "$tool" >/dev/null || die "$tool is required"; done

for c in "$DATING_C" "$MEDIA_C" "$MINIO_C" "$PG_C" "$REDIS_C"; do
  [ "$(docker inspect -f '{{.State.Running}}' "$c" 2>/dev/null || true)" = true ] || die "refusing: container $c is not running (local dev stack only)"
done

# dev compose leaves ENV unset on dating-service; anything named is refused
# unless it is local/dev. The checks below pin the target to this laptop stack.
DATING_ENV=$(docker exec "$DATING_C" printenv ENV 2>/dev/null || true)
case "${DATING_ENV,,}" in
  ''|local|dev|development) ;;
  *) die "refusing: dating-service ENV is '$DATING_ENV', expected unset, local or dev" ;;
esac
DSN_LOCAL=$(docker exec "$DATING_C" sh -c 'case "$POSTGRES_DSN" in *@postgres:5432/app\?*|*@postgres:5432/app) echo yes;; *) echo no;; esac' 2>/dev/null || echo no)
[ "$DSN_LOCAL" = yes ] || die "refusing: dating-service is not on the compose postgres 'app' database"
MEDIA_MODE=$(docker exec "$MEDIA_C" sh -c 'case "$ENV" in local|dev|development) ;; *) echo env; exit 0;; esac
  [ "$MEDIA_SCANNER_BACKEND" = mock ] || { echo scanner; exit 0; }
  [ "$MEDIA_FACE_COMPARE_BACKEND" = mock ] || { echo faces; exit 0; }
  echo ok' 2>/dev/null || echo unreachable)
[ "$MEDIA_MODE" = ok ] || die "refusing: media-service must run ENV local/dev with the mock scanner and mock face provider (got: $MEDIA_MODE)"
MEDIA_BUCKET=$(docker exec "$MEDIA_C" printenv MINIO_BUCKET 2>/dev/null || true)
[[ $MEDIA_BUCKET =~ ^[a-z0-9.-]+$ ]] || die "media-service has no usable MINIO_BUCKET"

KEY=$(docker exec "$DATING_C" printenv INTERNAL_SERVICE_KEY 2>/dev/null || true)
[ -n "$KEY" ] || die "dating-service has no INTERNAL_SERVICE_KEY"
MKEY=''

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"; unset KEY MKEY' EXIT

# ─── HTTP ───────────────────────────────────────────────────────────────────
# cqv VAR VALUE — VALUE quoted for a curl config file, assigned to VAR.
cqv() { local _s=${2//\\/\\\\}; _s=${_s//\"/\\\"}; _s=${_s//$'\n'/\\n}; _s=${_s//$'\r'/\\r}; printf -v "$1" '"%s"' "$_s"; }

CODE='' BODY=''
# http METHOD URL BODY [HEADER...] — everything goes to curl on stdin.
http() {
  local method=$1 url=$2 body=$3 cfg h q out
  shift 3
  cfg=$'silent\nshow-error\nmax-time = 45\n'
  cqv q "$method"; cfg+="request = $q"$'\n'
  cqv q "$url"; cfg+="url = $q"$'\n'
  cfg+='write-out = "\n%{http_code}"'$'\n'
  for h in "$@"; do cqv q "$h"; cfg+="header = $q"$'\n'; done
  if [ -n "$body" ]; then cqv q "$body"; cfg+="data-binary = $q"$'\n'; fi
  if ! out=$(printf '%s' "$cfg" | curl -K -); then
    CODE=000 BODY=''
    return 0
  fi
  CODE=${out##*$'\n'}
  BODY=${out%$'\n'*}
  [ "$BODY" = "$CODE" ] && BODY=''
  return 0
}

# dating METHOD PATH USER BODY [admin]
dating() {
  local method=$1 path=$2 user=$3 body=$4
  local hdrs=("X-Internal-Service-Key: $KEY" "X-User-Id: $user" "Accept: application/json")
  [ -n "$body" ] && hdrs+=("Content-Type: application/json")
  [ "${5:-}" = admin ] && hdrs+=("X-Scopes: moderator")
  http "$method" "$DATING_URL$path" "$body" "${hdrs[@]}"
}

check() { local c; for c in "$@"; do [ "$CODE" = "$c" ] && return 0; done; return 1; }

# jget VAR JSON KEY — the first "KEY":"value" string in the document, or "".
jget() {
  local _re="\"$3\":\"([^\"]*)\""
  printf -v "$1" '%s' ''
  if [[ $2 =~ $_re ]]; then printf -v "$1" '%s' "${BASH_REMATCH[1]}"; fi
}
# countv VAR HAYSTACK NEEDLE — occurrences of a literal substring.
countv() { local _s=$2 _n=0; while [[ $_s == *"$3"* ]]; do _s=${_s#*"$3"}; _n=$((_n + 1)); done; printf -v "$1" '%s' "$_n"; }

# fail prints the status and the error envelope's code/message. dating-service
# error messages never echo keys.
fail() {
  local ec msg
  jget ec "$BODY" code
  jget msg "$BODY" message
  echo "FAILED: $* -> HTTP $CODE $ec $msg" >&2
  exit 1
}

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

# slug|first name|gender|birth date|intent|lat|lng|area|occupation
# Women are placed at varied distances from call_a (Koramangala), men from
# call_b (Indiranagar): lt_5_km, km_5_10, km_10_25 and gt_25_km all occur.
PROFILE_ROWS=$(cat <<'EOF'
ananya|Ananya|female|1997-03-14|serious|12.9116|77.6389|HSR Layout|Product designer
diya|Diya|female|1999-07-02|casual|12.9166|77.6101|BTM Layout|Data analyst
kavya|Kavya|female|1995-11-21|serious|12.9610|77.6387|Domlur|Architect
riya|Riya|female|1998-01-09|casual|12.9260|77.6762|Bellandur|UX researcher
sneha|Sneha|female|1993-05-30|marriage|12.9569|77.7011|Marathahalli|Software engineer
tanvi|Tanvi|female|2000-09-17|casual|12.9255|77.5468|Banashankari|Illustrator
aditi|Aditi|female|1994-12-04|serious|12.9698|77.7500|Whitefield|Product manager
isha|Isha|female|1996-08-25|marriage|13.0358|77.5970|Hebbal|Doctor
lavanya|Lavanya|female|1992-02-11|serious|12.8452|77.6602|Electronic City|Chartered accountant
zara|Zara|female|2001-06-06|casual|13.2468|77.7120|Devanahalli|Pilot trainee
arjun|Arjun|male|1996-10-19|serious|12.9784|77.6408|Indiranagar|Backend engineer
rohan|Rohan|male|1998-04-03|casual|12.9817|77.6286|Ulsoor|Chef
kabir|Kabir|male|1994-07-27|serious|12.9602|77.6474|Old Airport Road|Photographer
vikram|Vikram|male|1991-01-15|marriage|13.0031|77.5643|Malleshwaram|Lawyer
aarav|Aarav|male|1999-12-08|casual|13.0077|77.6960|KR Puram|Musician
dev|Dev|male|1997-05-22|serious|12.9116|77.6389|HSR Layout|Startup founder
ishaan|Ishaan|male|1995-03-03|serious|13.0285|77.5406|Yeshwanthpur|Civil engineer
nikhil|Nikhil|male|1993-09-12|marriage|12.9077|77.4854|Kengeri|Teacher
omkar|Omkar|male|2000-02-28|casual|12.9698|77.7500|Whitefield|Game developer
sameer|Sameer|male|1992-11-30|serious|13.2957|77.5364|Doddaballapur|Agronomist
EOF
)
mapfile -t ROWS <<<"$PROFILE_ROWS"

declare -A UID_OF FIRST GENDER DOB INTENT LAT LNG AREA JOB PHOTO_MID VIDEO_MID STATUS OWNED PHOTO_ID
SYNTH=()
for row in "${ROWS[@]}"; do
  IFS='|' read -r slug first gender dob intent lat lng area job <<<"$row"
  SYNTH+=("$slug")
  FIRST[$slug]=$first GENDER[$slug]=$gender DOB[$slug]=$dob INTENT[$slug]=$intent
  LAT[$slug]=$lat LNG[$slug]=$lng AREA[$slug]=$area JOB[$slug]=$job
done
PILOTS=(call_a call_b)
ALL=("${PILOTS[@]}" "${SYNTH[@]}")

id_names=(python.org "$SEED_DOMAIN/moderator")
for slug in "${SYNTH[@]}"; do id_names+=("$SEED_DOMAIN/user/$slug"); done
for slug in "${ALL[@]}"; do id_names+=("$SEED_DOMAIN/media/photo/$slug" "$SEED_DOMAIN/media/selfie-video/$slug"); done
compute_ids "${id_names[@]}"
[ "${IDS[python.org]}" = 886313e1-3b8a-5372-9b90-0c9aee199e5d ] || die "UUIDv5 self-check failed"
for slug in "${SYNTH[@]}"; do UID_OF[$slug]=${IDS[$SEED_DOMAIN/user/$slug]}; done
UID_OF[call_a]=$CALL_A UID_OF[call_b]=$CALL_B
for slug in "${ALL[@]}"; do
  PHOTO_MID[$slug]=${IDS[$SEED_DOMAIN/media/photo/$slug]}
  VIDEO_MID[$slug]=${IDS[$SEED_DOMAIN/media/selfie-video/$slug]}
done
SEED_MODERATOR=${IDS[$SEED_DOMAIN/moderator]} # admin read actor only; not an account

# ─── Local helpers (dev infra) ──────────────────────────────────────────────
psql_app() { docker exec -i "$PG_C" psql -U postgres -d app -tAqX -v ON_ERROR_STOP=1; }
redis_del() { docker exec "$REDIS_C" redis-cli DEL "$@" >/dev/null; }

# [blob-media] Stage files under $TMP/stage/<storage key>, then copy the whole
# tree into MinIO in one `mc cp --recursive` run inside the minio container.
upload_stage() {
  [ -d "$TMP/stage" ] || return 0
  docker exec "$MINIO_C" rm -rf /tmp/dating-seed
  (cd "$TMP" && docker cp stage "$MINIO_C:/tmp/dating-seed") >/dev/null
  local out
  if ! out=$(docker exec "$MINIO_C" sh -c 'MC_HOST_seed="http://${MINIO_ROOT_USER}:${MINIO_ROOT_PASSWORD}@localhost:9000" mc cp --quiet --recursive /tmp/dating-seed/ "seed/$1/" >/dev/null; rc=$?; rm -rf /tmp/dating-seed; exit $rc' _ "$MEDIA_BUCKET" 2>&1); then
    echo "$out" | sed -E 's#://[^@/ ]+@#://<redacted>@#g' >&2
    die "MinIO upload failed"
  fi
  rm -rf "$TMP/stage"
}

BASE_COLORS=(0x3b5b8c 0x8c5b3b 0x4f7f5a 0x7a4f7f)
declare -A BASE_SIZE
# base_jpeg INDEX — a 600x750 solid-colour JPEG, rendered once; path in REPLY.
base_jpeg() {
  REPLY="$TMP/base$1.jpg"
  if [ ! -s "$REPLY" ]; then
    docker exec "$MEDIA_C" ffmpeg -hide_banner -loglevel error -f lavfi -i "color=c=${BASE_COLORS[$1]}:s=600x750" \
      -frames:v 1 -c:v mjpeg -f image2pipe pipe:1 >"$REPLY"
    [ -s "$REPLY" ] || die "could not render the placeholder JPEG"
    BASE_SIZE[$1]=$(wc -c <"$REPLY")
    BASE_SIZE[$1]=${BASE_SIZE[$1]// /}
  fi
}
# stage_photo SLUG INDEX — placeholder JPEG plus the mock face marker; size in REPLY.
stage_photo() {
  local slug=$1 dir marker b=$(($2 % 4))
  dir="$TMP/stage/user/${UID_OF[$slug]}/${PHOTO_MID[$slug]}"
  mkdir -p "$dir"
  base_jpeg "$b"
  printf -v marker '\nATPOST-FACE-TEST:v1:faces=1:subject=devseed-%s\n' "$slug"
  { cat "$REPLY"; printf '%s' "$marker"; } >"$dir/original"
  REPLY=$((BASE_SIZE[$b] + ${#marker}))
}
# stage_video SLUG — a stand-in "video": the mock liveness analyzer reads only
# its marker (two blinks, one face, same subject as the photo). Size in REPLY.
stage_video() {
  local slug=$1 dir head=$'\x18ftypmp42' marker
  dir="$TMP/stage/user/${UID_OF[$slug]}/${VIDEO_MID[$slug]}"
  mkdir -p "$dir"
  printf -v marker 'ATPOST-LIVENESS-TEST:v1:blinks=2:faces=1:subject=devseed-%s:duration_ms=3000\n' "$slug"
  printf '%s\n%s' "$head" "$marker" >"$dir/original"
  REPLY=$((${#head} + 1 + ${#marker}))
}

# ─── Reset ──────────────────────────────────────────────────────────────────
do_reset() {
  if [ "$DRY_RUN" = 1 ]; then
    step "Reset DRY RUN: counting what would be deleted (rolled back)"
  else
    step "Reset: synthetic dating data (by deterministic id) and seeded pilot sparks/matches"
  fi
  local values='' slug
  for slug in "${SYNTH[@]}"; do values+="('${UID_OF[$slug]}'),"; done
  values=${values%,}

  # [sql-reset] one transaction; dating_admin_audit is append-only and is left.
  local sql
  sql=$(cat <<SQL
BEGIN;
CREATE TEMP TABLE seed_ids(id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO seed_ids VALUES $values;
CREATE TEMP TABLE seed_counts(t text, n bigint) ON COMMIT DROP;
WITH d AS (DELETE FROM dating_matches WHERE user_a IN (SELECT id FROM seed_ids) OR user_b IN (SELECT id FROM seed_ids)
   OR (user_a = LEAST('$CALL_A'::uuid, '$CALL_B'::uuid) AND user_b = GREATEST('$CALL_A'::uuid, '$CALL_B'::uuid)) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_matches', count(*) FROM d;
WITH d AS (DELETE FROM dating_sparks WHERE from_user_id IN (SELECT id FROM seed_ids) OR to_user_id IN (SELECT id FROM seed_ids)
   OR (from_user_id IN ('$CALL_A', '$CALL_B') AND to_user_id IN ('$CALL_A', '$CALL_B')) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_sparks', count(*) FROM d;
WITH d AS (DELETE FROM dating_reports WHERE reporter_id IN (SELECT id FROM seed_ids) OR target_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_reports', count(*) FROM d;
WITH d AS (DELETE FROM dating_panic_incidents WHERE user_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_panic_incidents', count(*) FROM d;
WITH d AS (DELETE FROM dating_blocks WHERE user_id IN (SELECT id FROM seed_ids) OR blocked_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_blocks', count(*) FROM d;
WITH d AS (DELETE FROM dating_passes WHERE user_id IN (SELECT id FROM seed_ids) OR candidate_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_passes', count(*) FROM d;
WITH d AS (DELETE FROM dating_stashes WHERE user_id IN (SELECT id FROM seed_ids) OR candidate_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_stashes', count(*) FROM d;
WITH d AS (DELETE FROM dating_trusted_contacts WHERE user_id IN (SELECT id FROM seed_ids) OR contact_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_trusted_contacts', count(*) FROM d;
WITH d AS (DELETE FROM dating_location_shares WHERE user_id IN (SELECT id FROM seed_ids) OR recipient_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_location_shares', count(*) FROM d;
WITH d AS (DELETE FROM dating_meets WHERE user_id IN (SELECT id FROM seed_ids) OR with_user_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_meets', count(*) FROM d;
WITH d AS (DELETE FROM dating_vouches WHERE voucher_id IN (SELECT id FROM seed_ids) OR vouchee_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_vouches', count(*) FROM d;
WITH d AS (DELETE FROM dating_explain_ledger WHERE viewer_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_explain_ledger', count(*) FROM d;
WITH d AS (DELETE FROM dating_spark_ledger WHERE from_user_id IN (SELECT id FROM seed_ids) RETURNING 1)
  INSERT INTO seed_counts SELECT 'dating_spark_ledger', count(*) FROM d;
SQL
)
  local t
  for t in dating_selfie_attempts dating_selfie_challenges dating_verifications dating_consent_log \
    dating_location_changes dating_photos dating_prompts dating_preferences dating_tunes dating_echo_cache \
    dating_account_risk dating_device_fingerprints dating_safety_events dating_data_exports \
    dating_boost_balances dating_premium_purchases dating_payment_intents dating_premium_subscriptions dating_profiles; do
    sql+=$'\n'"WITH d AS (DELETE FROM $t WHERE user_id IN (SELECT id FROM seed_ids) RETURNING 1) INSERT INTO seed_counts SELECT '$t', count(*) FROM d;"
  done
  sql+=$'\n'"SELECT t || '=' || n FROM seed_counts WHERE n > 0 ORDER BY t;"
  if [ "$DRY_RUN" = 1 ]; then sql+=$'\n'"ROLLBACK;"; else sql+=$'\n'"COMMIT;"; fi
  local rows total=0 line verb=deleted
  [ "$DRY_RUN" = 1 ] && verb="would delete"
  rows=$(printf '%s\n' "$sql" | psql_app)
  for line in $rows; do
    log "$verb $line"
    total=$((total + ${line#*=}))
  done
  log "dating rows $verb: $total"

  if [ "$DRY_RUN" = 1 ]; then
    log "would purge up to $((${#SYNTH[@]} * 2)) media assets and drop deck caches; nothing changed"
    return 0
  fi

  # Media: media-service's internal dating-photo purge (owner-checked; rows,
  # renditions, blurred variant, objects). 404 = already gone.
  MKEY=$(docker exec "$MEDIA_C" printenv INTERNAL_SERVICE_KEY 2>/dev/null || true)
  [ -n "$MKEY" ] || die "media-service has no INTERNAL_SERVICE_KEY"
  local purged=0 gone=0 mid
  for slug in "${SYNTH[@]}"; do
    for mid in "${PHOTO_MID[$slug]}" "${VIDEO_MID[$slug]}"; do
      http DELETE "$MEDIA_URL/internal/v1/media/dating-photos/$mid?requester_user_id=${UID_OF[$slug]}" '' \
        "X-Internal-Service-Key: $MKEY" "Accept: application/json"
      if check 200; then purged=$((purged + 1)); elif check 404; then gone=$((gone + 1)); else fail "purge media for $slug"; fi
    done
  done
  MKEY=''
  log "media purged: $purged (already absent: $gone)"

  # [redis] cached decks that could still show the removed profiles.
  local keys=("dating:pulse:today:v3:$CALL_A" "dating:pulse:today:v3:$CALL_B")
  for slug in "${SYNTH[@]}"; do keys+=("dating:pulse:today:v3:${UID_OF[$slug]}" "dating:deck_membership:${UID_OF[$slug]}"); done
  redis_del "${keys[@]}"
  log "deck caches dropped"
  cat <<EOF

Reset done. Left in place on purpose:
   - call_a / call_b dating profiles, photos and consents (only their seeded
     sparks and matches were removed)
   - chat conversations dating opened for the removed matches (chat-owned)
   - trust-safety grievances and notification ops alerts raised by the seeded
     report and panic, dating_admin_audit rows (append-only)
EOF
}

# ─── Seed stages ────────────────────────────────────────────────────────────
load_status() { # slug -> STATUS[slug] ("" when no profile)
  dating GET /v1/dating/profile "${UID_OF[$1]}" ''
  if check 404; then STATUS[$1]=''; return 0; fi
  check 200 || fail "get profile ($1)"
  jget "STATUS[$1]" "$BODY" profile_status
}

CREATED=0 REUSED=0
seed_profiles() {
  step "Profiles"
  local slug body prefs is_pilot
  for slug in "${ALL[@]}"; do
    load_status "$slug"
    is_pilot=0
    [[ $slug == call_* ]] && is_pilot=1
    if [ -n "${STATUS[$slug]}" ] && [ "${STATUS[$slug]}" != draft ]; then
      REUSED=$((REUSED + 1))
      continue
    fi
    if [ "$is_pilot" = 1 ] && [ -n "${STATUS[$slug]}" ]; then
      warn "$slug has a draft dating profile of their own; left alone (finish it in the app)"
      continue
    fi
    if [ "$is_pilot" = 1 ]; then
      local g i la ln
      if [ "$slug" = call_a ]; then g=$CALL_A_GENDER i=$CALL_A_INTERESTED la=$CALL_A_LAT ln=$CALL_A_LNG
      else g=$CALL_B_GENDER i=$CALL_B_INTERESTED la=$CALL_B_LAT ln=$CALL_B_LNG; fi
      # First name and birth date come from identity (registration DOB).
      body="{\"intent\":\"serious\",\"gender\":\"$g\",\"bio\":\"Dating pilot tester\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"country\":\"India\",\"latitude\":$la,\"longitude\":$ln}"
      prefs="{\"interested_in_gender\":\"$i\",\"min_age\":18,\"max_age\":45,\"distance_km\":50}"
    else
      local want=male
      [ "${GENDER[$slug]}" = male ] && want=female
      body="{\"intent\":\"${INTENT[$slug]}\",\"gender\":\"${GENDER[$slug]}\",\"first_name\":\"${FIRST[$slug]}\",\"birth_date\":\"${DOB[$slug]}T00:00:00Z\",\"bio\":\"Synthetic dev seed profile (not a real person). ${AREA[$slug]}.\",\"occupation\":\"${JOB[$slug]}\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"country\":\"India\",\"latitude\":${LAT[$slug]},\"longitude\":${LNG[$slug]},\"language_prefs\":[\"en\"]}"
      prefs="{\"interested_in_gender\":\"$want\",\"min_age\":21,\"max_age\":40,\"distance_km\":50}"
    fi
    dating POST /v1/dating/profile "${UID_OF[$slug]}" "$body"
    check 200 201 || fail "upsert profile ($slug)"
    dating PUT /v1/dating/preferences "${UID_OF[$slug]}" "$prefs"
    check 200 || fail "put preferences ($slug)"
    load_status "$slug"
    [ "$is_pilot" = 1 ] && OWNED[$slug]=1
    CREATED=$((CREATED + 1))
  done
  declare -A tally=()
  for slug in "${ALL[@]}"; do tally[${STATUS[$slug]:-none}]=$((${tally[${STATUS[$slug]:-none}]:-0} + 1)); done
  local summary=''
  for slug in "${!tally[@]}"; do summary+="$slug=${tally[$slug]} "; done
  log "created $CREATED, reused $REUSED (statuses: ${summary% })"
}

# Onboarding applies to synthetic users, and to a pilot account only when this
# seeder created its profile or its seeded placeholder media already exists.
needs_onboarding() {
  case "${STATUS[$1]}" in pending_photo|pending_selfie) ;; *) return 1 ;; esac
  [[ $1 != call_* ]] || [ "${OWNED[$1]:-0}" = 1 ]
}

MEDIA_INSERTED=0
seed_media() {
  step "Placeholder media [sql-media] [blob-media]"
  local slug ids='' existing
  for slug in "${ALL[@]}"; do ids+="'${PHOTO_MID[$slug]}','${VIDEO_MID[$slug]}',"; done
  # [sql-read] which seeded media rows already exist.
  existing=$(printf "SELECT id FROM public.media_assets WHERE id IN (%s);\n" "${ids%,}" | psql_app)
  for slug in "${PILOTS[@]}"; do
    [[ $existing == *"${PHOTO_MID[$slug]}"* ]] && OWNED[$slug]=1
  done
  local values='' i=0
  for slug in "${ALL[@]}"; do
    i=$((i + 1))
    needs_onboarding "$slug" || continue
    if [[ $existing != *"${PHOTO_MID[$slug]}"* ]]; then
      stage_photo "$slug" "$i"
      values+="('${PHOTO_MID[$slug]}','${UID_OF[$slug]}','image','general','image/jpeg',$REPLY,'$MEDIA_BUCKET','user/${UID_OF[$slug]}/${PHOTO_MID[$slug]}/original','ready',600,750,NULL,'passed','mock','[]'::jsonb,now(),now()),"
    fi
    if [[ $existing != *"${VIDEO_MID[$slug]}"* ]]; then
      stage_video "$slug"
      values+="('${VIDEO_MID[$slug]}','${UID_OF[$slug]}','video','general','video/mp4',$REPLY,'$MEDIA_BUCKET','user/${UID_OF[$slug]}/${VIDEO_MID[$slug]}/original','ready',NULL,NULL,3000,'passed','mock','[]'::jsonb,now(),now()),"
    fi
  done
  if [ -z "$values" ]; then
    log "no media to create"
    return 0
  fi
  upload_stage # objects first, so a row never points at a missing object
  MEDIA_INSERTED=$(printf "WITH ins AS (INSERT INTO public.media_assets (id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes, storage_bucket, storage_key, processing_status, width, height, duration_ms, moderation_status, moderation_scanner, moderation_labels, created_at, updated_at) VALUES %s ON CONFLICT (id) DO NOTHING RETURNING 1) SELECT count(*) FROM ins;\n" "${values%,}" | psql_app)
  log "media rows inserted: $MEDIA_INSERTED"
}

PHOTOS_ATTACHED=0
seed_photos() {
  step "Photos (dating-service: owner check, prepare, auto moderation)"
  local slug ms ec
  for slug in "${ALL[@]}"; do
    [ "${STATUS[$slug]}" = pending_photo ] && needs_onboarding "$slug" || continue
    dating POST /v1/dating/photos "${UID_OF[$slug]}" "{\"media_id\":\"${PHOTO_MID[$slug]}\",\"is_primary\":true,\"sort_order\":0,\"visibility\":\"public\"}"
    if check 409; then
      jget ec "$BODY" code
      warn "$slug: photo not attached ($ec)"
    else
      check 200 201 || fail "attach photo ($slug)"
      jget ms "$BODY" moderation_status
      [ "$ms" = approved ] || warn "$slug: photo moderation is '$ms', not approved"
      PHOTOS_ATTACHED=$((PHOTOS_ATTACHED + 1))
    fi
    load_status "$slug"
  done
  log "photos attached: $PHOTOS_ATTACHED"
}

MARKERS=0
restore_markers() {
  step "Mock face marker on prepared photos [blob-marker]"
  local slug i=0
  for slug in "${ALL[@]}"; do
    i=$((i + 1))
    [ "${STATUS[$slug]}" = pending_selfie ] && needs_onboarding "$slug" || continue
    stage_photo "$slug" "$i"
    MARKERS=$((MARKERS + 1))
  done
  upload_stage
  log "markers restored: $MARKERS"
}

SELFIES=0
seed_selfies() {
  step "Selfie (consent, blink challenge, liveness)"
  local slug ch st reason
  for slug in "${ALL[@]}"; do
    [ "${STATUS[$slug]}" = pending_selfie ] && needs_onboarding "$slug" || continue
    dating PUT /v1/dating/consents/biometric_selfie "${UID_OF[$slug]}" '{"granted":true}'
    check 200 || fail "grant biometric_selfie consent ($slug)"
    dating POST /v1/dating/verification/selfie/challenge "${UID_OF[$slug]}" ''
    check 200 201 || fail "selfie challenge ($slug)"
    jget ch "$BODY" challenge_id
    dating POST /v1/dating/verification/selfie "${UID_OF[$slug]}" "{\"challenge_id\":\"$ch\",\"video_media_id\":\"${VIDEO_MID[$slug]}\"}"
    check 200 201 || fail "submit selfie ($slug)"
    jget st "$BODY" status
    jget reason "$BODY" reason
    if [ "$st" = passed ]; then SELFIES=$((SELFIES + 1)); else warn "$slug: selfie $st $reason"; fi
    load_status "$slug"
  done
  log "selfies passed: $SELFIES"
}

# photo_ref SLUG — the user's primary photo id (spark target) into PHOTO_ID[slug].
photo_ref() {
  if [ -z "${PHOTO_ID[$1]:-}" ]; then
    dating GET /v1/dating/photos/me "${UID_OF[$1]}" ''
    check 200 || fail "list photos ($1)"
    jget "PHOTO_ID[$1]" "$BODY" id
  fi
  [ -n "${PHOTO_ID[$1]}" ] || fail "no photo to spark on ($1)"
}

SPARKS_SENT=0 SPARKS_KEPT=0
spark_once() { # FROM TO
  local from=$1 to=$2
  dating GET "/v1/dating/sparks/incoming?limit=200" "${UID_OF[$to]}" ''
  check 200 || fail "list incoming sparks ($to)"
  if [[ $BODY == *"${UID_OF[$from]}"* ]]; then SPARKS_KEPT=$((SPARKS_KEPT + 1)); return 0; fi
  photo_ref "$to"
  dating POST /v1/dating/sparks "${UID_OF[$from]}" "{\"to_user_id\":\"${UID_OF[$to]}\",\"target_kind\":\"photo\",\"target_ref\":\"${PHOTO_ID[$to]}\",\"note\":\"Dev seed spark\"}"
  check 200 201 || fail "spark $from -> $to"
  SPARKS_SENT=$((SPARKS_SENT + 1))
}
matched() { # A B
  dating GET /v1/dating/matches "${UID_OF[$1]}" ''
  check 200 || fail "list matches ($1)"
  [[ $BODY == *"${UID_OF[$2]}"* ]]
}
ensure_match() { # A B — B sparks first, A sparks back
  if matched "$1" "$2"; then SPARKS_KEPT=$((SPARKS_KEPT + 2)); return 0; fi
  spark_once "$2" "$1"
  spark_once "$1" "$2"
  matched "$1" "$2" || warn "no match formed between $1 and $2"
}

REPORT_STATE='' PANIC_STATE=''
seed_social() {
  step "Sparks and matches"
  local slug
  for slug in call_a call_b ananya riya diya aditi zara arjun dev rohan ishaan sameer kabir vikram kavya; do
    [ "${STATUS[$slug]}" = active ] || warn "$slug is '${STATUS[$slug]:-none}', not active; steps involving it are skipped"
  done
  if [ "${STATUS[call_a]}" = active ]; then
    for slug in ananya riya; do [ "${STATUS[$slug]}" = active ] && ensure_match call_a "$slug"; done
    for slug in diya aditi zara; do [ "${STATUS[$slug]}" = active ] && spark_once "$slug" call_a; done
  fi
  if [ "${STATUS[call_b]}" = active ]; then
    for slug in arjun dev; do [ "${STATUS[$slug]}" = active ] && ensure_match call_b "$slug"; done
    for slug in rohan ishaan sameer; do [ "${STATUS[$slug]}" = active ] && spark_once "$slug" call_b; done
  fi
  if [ "${STATUS[call_a]}" = active ] && [ "${STATUS[call_b]}" = active ]; then
    ensure_match call_a call_b
  fi
  log "sparks sent: $SPARKS_SENT, already present: $SPARKS_KEPT"

  step "Admin queue: one report, one panic (synthetic users)"
  dating GET "/v1/dating/admin/reports?limit=200" "$SEED_MODERATOR" '' admin
  check 200 || fail "list reports"
  if [[ $BODY == *"\"reporter_id\":\"${UID_OF[kabir]}\""* ]]; then
    REPORT_STATE=existing
  elif [ "${STATUS[kabir]}" = active ]; then
    dating POST /v1/dating/safety/report "${UID_OF[kabir]}" "{\"target_id\":\"${UID_OF[vikram]}\",\"reason\":\"fake_profile\",\"details\":\"Dev seed: synthetic report for the moderation queue.\"}"
    check 201 200 || fail "report (kabir -> vikram)"
    REPORT_STATE=created
  fi
  dating GET "/v1/dating/admin/safety/panic?limit=200" "$SEED_MODERATOR" '' admin
  check 200 || fail "list panic incidents"
  if [[ $BODY == *"\"user_id\":\"${UID_OF[kavya]}\""* ]]; then
    PANIC_STATE=existing
  elif [ "${STATUS[kavya]}" = active ]; then
    dating POST /v1/dating/safety/panic "${UID_OF[kavya]}" '{"latitude":12.9612,"longitude":77.6391,"context":{"note":"dev seed"}}'
    check 200 || fail "panic (kavya)"
    PANIC_STATE=created
  fi
  log "report: ${REPORT_STATE:-skipped}, panic: ${PANIC_STATE:-skipped}"
}

verify() {
  step "Verify (dating-service routes as the gateway would call them)"
  # [redis] a deck cached before the seed would hide the new profiles.
  redis_del "dating:pulse:today:v3:$CALL_A" "dating:pulse:today:v3:$CALL_B"
  local slug ids='' who deck buckets cards matches convs n
  for slug in "${SYNTH[@]}"; do ids+="'${UID_OF[$slug]}',"; done
  # [sql-read] there is no route that counts profiles.
  V_ACTIVE=$(printf "SELECT count(*) FILTER (WHERE profile_status = 'active') || ' ' || count(*) FILTER (WHERE profile_status = 'active' AND user_id IN (%s)) FROM dating_profiles;\n" "${ids%,}" | psql_app)
  for who in call_a call_b; do
    dating GET /v1/dating/pulse/today "${UID_OF[$who]}" ''
    check 200 || fail "pulse today ($who)"
    deck=$BODY
    buckets=$(printf '%s' "$deck" | grep -o '"distance_bucket":"[a-z0-9_]*"' | cut -d'"' -f4 | sort | uniq -c | awk '{printf "%s=%s ", $2, $1}' || true)
    countv cards "$deck" '"candidate_id"'
    printf -v "V_DECK_$who" '%s cards (%s)%s' "$cards" "${buckets% }" "$([[ $deck == *'"cohort_gated":true'* ]] && echo ' cohort_gated' || true)"
    dating GET /v1/dating/matches "${UID_OF[$who]}" ''
    check 200 || fail "matches ($who)"
    countv matches "$BODY" '"matched_at"'
    countv convs "$BODY" '"conversation_id"'
    printf -v "V_MATCH_$who" '%s (with conversation: %s)' "$matches" "$convs"
    [ "$convs" = "$matches" ] || warn "$who: $((matches - convs)) match(es) have no chat conversation (dating-service logs 'saga: create conversation failed'; see runbook section 2)"
    dating GET "/v1/dating/sparks/incoming?limit=200" "${UID_OF[$who]}" ''
    check 200 || fail "incoming sparks ($who)"
    n=0
    for slug in "${SYNTH[@]}"; do [[ $BODY == *"${UID_OF[$slug]}"* ]] && n=$((n + 1)); done
    printf -v "V_INCOMING_$who" '%s from seeded profiles' "$n"
  done
  dating GET "/v1/dating/admin/reports?limit=200" "$SEED_MODERATOR" '' admin
  countv V_REPORTS "$BODY" '"reporter_id"'
  dating GET "/v1/dating/admin/safety/panic?limit=200" "$SEED_MODERATOR" '' admin
  countv V_PANICS "$BODY" '"trigger_count"'
  dating GET "/v1/dating/admin/photos/pending?limit=200" "$SEED_MODERATOR" '' admin
  countv V_PHOTOS "$BODY" '"media_id"'
  dating GET "/v1/dating/admin/verification/selfie/pending?limit=200" "$SEED_MODERATOR" '' admin
  countv V_SELFIES "$BODY" '"user_id"'
}

# ─── Main ───────────────────────────────────────────────────────────────────
echo "Dating dev seed — dating-service $DATING_URL (ENV=${DATING_ENV:-unset}), media mock providers, bucket $MEDIA_BUCKET"

deadline=$((SECONDS + 60))
until [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$DATING_URL/healthz" || true)" = 200 ]; do
  ((SECONDS < deadline)) || die "dating-service /healthz did not return 200 within 60 s"
  sleep 3
done
echo "healthz: 200"

PILOT_IDS=$(docker exec "$GATEWAY_C" printenv DATING_PILOT_USER_IDS 2>/dev/null || true)
PILOT_IDS=",${PILOT_IDS// /},"
PILOT_IDS=${PILOT_IDS,,}
printf 'gateway DATING_PILOT_USER_IDS: call_a %s, call_b %s\n' \
  "$([[ $PILOT_IDS == *",$CALL_A,"* ]] && echo listed || echo MISSING)" \
  "$([[ $PILOT_IDS == *",$CALL_B,"* ]] && echo listed || echo MISSING)"
unset PILOT_IDS

if [ "$RESET" = 1 ]; then
  do_reset
  exit 0
fi

seed_profiles
seed_media
seed_photos
restore_markers
seed_selfies
seed_social
verify
KEY=''

step "Summary"
printf '   %-16s created=%s reused=%s (20 synthetic + call_a/call_b)\n' Profiles "$CREATED" "$REUSED"
printf '   %-16s media_rows=%s photos_attached=%s markers_restored=%s selfies_passed=%s\n' Onboarding "$MEDIA_INSERTED" "$PHOTOS_ATTACHED" "$MARKERS" "$SELFIES"
printf '   %-16s sent=%s already_present=%s\n' Sparks "$SPARKS_SENT" "$SPARKS_KEPT"
printf '   %-16s report=%s panic=%s\n' "Admin seeds" "${REPORT_STATE:-skipped}" "${PANIC_STATE:-skipped}"
printf '   %-16s all=%s seeded=%s\n' "Active profiles" "${V_ACTIVE% *}" "${V_ACTIVE#* }"
printf '   %-16s call_a: %s\n' Deck "$V_DECK_call_a"
printf '   %-16s call_b: %s\n' '' "$V_DECK_call_b"
printf '   %-16s call_a: %s; call_b: %s\n' Matches "$V_MATCH_call_a" "$V_MATCH_call_b"
printf '   %-16s call_a: %s; call_b: %s\n' "Incoming sparks" "$V_INCOMING_call_a" "$V_INCOMING_call_b"
printf '   %-16s reports=%s panic_incidents=%s photos_pending=%s selfies_pending=%s\n' "Admin queue" "$V_REPORTS" "$V_PANICS" "$V_PHOTOS" "$V_SELFIES"
printf '   %-16s %s\n' Warnings "$WARNINGS"
cat <<EOF

Next manual steps
   1. Phones: call_a and call_b open Dating. Pulse shows today's deck with
      distance buckets; Matches shows the seeded matches and each other.
   2. A match expires after 7 days unless someone sends the first message.
   3. Moderation, panic handling and reset: docs/runbooks/dating-internal-pilot.md
EOF
