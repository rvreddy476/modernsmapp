#!/usr/bin/env bash
# dev-seed-feast.sh — DEV-ONLY seeder for Feast Kitchen / Feast Rider testing.
#
# Talks to food-service directly on localhost with the internal key and the
# identity headers api-gateway injects (X-User-Id, X-Scopes). It never logs in,
# never registers users and never prints a key, token, DigiLocker code/state or
# webhook signature. See scripts/README.md.
#
#   bash scripts/dev-seed-feast.sh [--order-only] [--owner ID] [--rider ID]
#        [--customer ID] [--admin ID] [--food-url URL] [--payments-url URL]
#
# Secret handling: INTERNAL_SERVICE_KEY and RAZORPAY_WEBHOOK_SECRET are read
# into shell variables with `docker exec ... printenv` and only ever reach
# curl through its stdin config (-K -) or bash builtins (the HMAC below). They
# never appear in a process argument list, a file or the output.
set -euo pipefail

# ─── Defaults ───────────────────────────────────────────────────────────────
OWNER=2d598287-eee7-40b4-a7f5-b46b9412e4e7    # call_a
RIDER=66668bc2-a3f6-40a5-9cdd-c998dcf72f29    # call_b
CUSTOMER=c3294f15-4e03-4749-8e5f-0c478398130b # a msgtest_* account
ADMIN=d74639c3-8270-404c-a2c7-93d5d2fcfa68    # another msgtest_* account (audit actor only)
FOOD_URL=http://localhost:8113
PAY_URL=http://localhost:8102
ORDER_ONLY=0

FOOD_C=atpost_stack-food-service-1
PAY_C=atpost_stack-payments-service-1
PG_C=atpost_stack-postgres-1

RESTAURANT_NAME="Feast Test Kitchen"
ADDRESS_LABEL="Feast Dev Seed"
# Koramangala; the customer address is ~2.5 km south-east (HSR Layout).
REST_LAT=12.9352 REST_LNG=77.6245
ADDR_LAT=12.9150 ADDR_LNG=77.6350
# Synthetic golden values from shared/kyc tests (all zero serials / ZZ series).
PAN=ZZZPZ0000Z
FSSAI=10099999000000
DL_NUMBER=KA0120200000001 DL_MASK_TAIL=0001
RC_NUMBER=MH12ZZ0000 RC_MASK_TAIL=0000

usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --owner) OWNER=${2:?}; shift 2 ;;
    --rider) RIDER=${2:?}; shift 2 ;;
    --customer) CUSTOMER=${2:?}; shift 2 ;;
    --admin) ADMIN=${2:?}; shift 2 ;;
    --food-url) FOOD_URL=${2:?}; shift 2 ;;
    --payments-url) PAY_URL=${2:?}; shift 2 ;;
    --order-only) ORDER_ONLY=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "unknown argument: $1" >&2; usage 2 ;;
  esac
done

die() { echo "ERROR: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*" >&2; }
# Progress goes to stderr: several helpers return ids on stdout.
log() { printf '   %s\n' "$*" >&2; }
warn() { printf '   WARN: %s\n' "$*" >&2; }

UUID_RE='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
for pair in "owner:$OWNER" "rider:$RIDER" "customer:$CUSTOMER" "admin:$ADMIN"; do
  [[ ${pair#*:} =~ $UUID_RE ]] || die "--${pair%%:*} must be a lowercase UUID"
done
[ "$CUSTOMER" != "$OWNER" ] && [ "$CUSTOMER" != "$RIDER" ] || die "the customer must differ from the owner and the rider"

# ─── Dev-stack guard ────────────────────────────────────────────────────────
is_local_url() { [[ $1 =~ ^http://(localhost|127\.0\.0\.1)(:[0-9]+)?/?$ ]]; }
is_local_url "$FOOD_URL" || die "refusing: --food-url must be http://localhost[:port]"
is_local_url "$PAY_URL" || die "refusing: --payments-url must be http://localhost[:port]"
FOOD_URL=${FOOD_URL%/} PAY_URL=${PAY_URL%/}
command -v docker >/dev/null || die "docker is required"
command -v curl >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

FOOD_ENV=$(docker exec "$FOOD_C" printenv ENV 2>/dev/null || true)
case "$FOOD_ENV" in
  local|dev|development) ;;
  *) die "refusing: food-service ENV is '${FOOD_ENV:-unset}', expected local or dev" ;;
esac
# payments-service must be on Razorpay TEST keys (prints yes/no only).
PAY_TEST=$(docker exec "$PAY_C" sh -c 'case "$RAZORPAY_KEY_ID" in rzp_test_*) echo yes;; *) echo no;; esac' 2>/dev/null || echo no)

KEY=$(docker exec "$FOOD_C" printenv INTERNAL_SERVICE_KEY 2>/dev/null || true)
[ -n "$KEY" ] || die "food-service has no INTERNAL_SERVICE_KEY"

# ─── HTTP ───────────────────────────────────────────────────────────────────
# cq quotes a value for a curl config file.
cq() { local s=${1//\\/\\\\}; s=${s//\"/\\\"}; s=${s//$'\n'/\\n}; s=${s//$'\r'/\\r}; printf '"%s"' "$s"; }

CODE='' BODY='' REDIRECT=''
# http METHOD URL BODY [HEADER...] — everything goes to curl on stdin.
http() {
  local method=$1 url=$2 body=$3 cfg h out
  shift 3
  cfg=$'silent\nshow-error\nmax-time = 45\n'
  cfg+="request = $(cq "$method")"$'\n'"url = $(cq "$url")"$'\n'
  cfg+='write-out = "\n%{redirect_url}\n%{http_code}"'$'\n'
  for h in "$@"; do cfg+="header = $(cq "$h")"$'\n'; done
  [ -n "$body" ] && cfg+="data-binary = $(cq "$body")"$'\n'
  if ! out=$(printf '%s' "$cfg" | curl -K -); then
    CODE=000 BODY='' REDIRECT=''
    return 0
  fi
  CODE=${out##*$'\n'}
  out=${out%$'\n'*}
  REDIRECT=${out##*$'\n'}
  BODY=${out%$'\n'*}
  [ "$BODY" = "$REDIRECT" ] && BODY=''
  return 0
}

# food METHOD PATH USER BODY [admin] [idem]
food() {
  local method=$1 path=$2 user=$3 body=$4 flag
  shift 4
  local hdrs=("X-Internal-Service-Key: $KEY" "X-User-Id: $user" "Accept: application/json")
  [ -n "$body" ] && hdrs+=("Content-Type: application/json")
  for flag in "$@"; do
    case $flag in
      admin) hdrs+=("X-Scopes: admin") ;;
      idem) hdrs+=("Idempotency-Key: $(uuid)") ;;
    esac
  done
  http "$method" "$FOOD_URL$path" "$body" "${hdrs[@]}"
}

check() { local c; for c in "$@"; do [ "$CODE" = "$c" ] && return 0; done; return 1; }

# fail prints the status and the error envelope's code/message/details. The
# API's error messages never echo submitted identifiers.
fail() {
  local ec msg det
  ec=$(jget "$BODY" '{error,code}' 2>/dev/null || true)
  msg=$(jget "$BODY" '{error,message}' 2>/dev/null || true)
  det=$(jx "$BODY" "coalesce((j #> '{error,details}')::text, '')" 2>/dev/null || true)
  echo "FAILED: $* -> HTTP $CODE ${ec:-} ${msg:+- $msg}" >&2
  [ -n "$det" ] && echo "        details: $det" >&2
  exit 1
}

# ─── JSON (no jq/python on this host) ───────────────────────────────────────
# jx evaluates a SQL expression over the JSON document `j` using the stack's
# psql. No table is read; the document arrives on stdin (log_statement=none).
jx() {
  local json=${1//$'\n'/ } expr=$2 esc
  [ -n "$json" ] || json='null'
  esc=${json//\\/\\\\}
  esc=${esc//\'/\\\'}
  printf "\\\\set j '%s'\nselect %s from (select :'j'::jsonb as j) s;\n" "$esc" "$expr" |
    docker exec -i "$PG_C" psql -U postgres -d app -tAqX -v ON_ERROR_STOP=1
}
jget() { jx "$1" "j #>> '$2'"; }
# jarr '{data,items}' — the array at that path, or [] when it is null/absent.
jarr() { printf "(case jsonb_typeof(j #> '%s') when 'array' then j #> '%s' else '[]'::jsonb end)" "$1" "$1"; }
sqlq() { printf "'%s'" "${1//\'/\'\'}"; }

uuid() {
  local h v
  h=$(openssl rand -hex 16 2>/dev/null || printf '%04x%04x%04x%04x%04x%04x%04x%04x' $RANDOM $RANDOM $RANDOM $RANDOM $RANDOM $RANDOM $RANDOM $RANDOM)
  printf -v v '%x' $((8 + (0x${h:16:1} & 3)))
  printf '%s-%s-4%s-%s%s-%s\n' "${h:0:8}" "${h:8:4}" "${h:13:3}" "$v" "${h:17:3}" "${h:20:12}"
}

# hmac_sha256_hex KEY MESSAGE — RFC 2104 with bash builtins + sha256sum, so the
# key is never an argument to an external process.
hmac_sha256_hex() {
  local key=$1 msg=$2 i b ipad='' opad='' inner innerfmt=''
  ((${#key} <= 64)) || die "HMAC keys longer than 64 bytes are not supported"
  for ((i = 0; i < 64; i++)); do
    b=0
    if ((i < ${#key})); then printf -v b '%d' "'${key:i:1}"; fi
    ((b <= 255)) || die "HMAC key must be ASCII"
    printf -v ipad '%s\\x%02x' "$ipad" $((b ^ 0x36))
    printf -v opad '%s\\x%02x' "$opad" $((b ^ 0x5c))
  done
  inner=$({ printf "$ipad"; printf '%s' "$msg"; } | sha256sum | cut -c1-64)
  for ((i = 0; i < 64; i += 2)); do innerfmt+="\\x${inner:i:2}"; done
  { printf "$opad"; printf "$innerfmt"; } | sha256sum | cut -c1-64
}
# RFC 4231-style self-check so a broken HMAC fails here, not as a 401 later.
[ "$(hmac_sha256_hex key 'The quick brown fox jumps over the lazy dog')" = \
  f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8 ] || die "HMAC self-check failed"

# Read-only SQL: food-service has no route that lists a user's uploads (the
# media-service listing needs a user token, which this script never obtains).
# FSSAI and selfie routes only check the id is a UUID; we still point them at a
# real image row, preferring one the user uploaded.
pick_media() {
  docker exec "$PG_C" psql -U postgres -d app -tAqX -c \
    "select id from public.media_assets where file_type = 'image' and processing_status = 'ready' order by (uploader_id = '$1') desc, created_at limit 1"
}

# ─── Restaurant ─────────────────────────────────────────────────────────────
RID='' RSTATUS='' RACCEPTING=''
find_restaurant() {
  food GET /v1/food/partner/restaurants "$OWNER" ''
  check 200 || fail "list the owner's restaurants"
  RID=$(jx "$BODY" "(select e->>'id' from jsonb_array_elements($(jarr '{data,items}')) e where e->>'name' = $(sqlq "$RESTAURANT_NAME") order by e->>'created_at' limit 1)")
}
load_restaurant() {
  food GET "/v1/food/partner/restaurants/$RID" "$OWNER" ''
  check 200 || fail "get restaurant"
  RSTATUS=$(jget "$BODY" '{data,status}')
  RACCEPTING=$(jget "$BODY" '{data,is_accepting_orders}')
}

MENU=''
load_menu() {
  food GET "/v1/food/partner/restaurants/$RID/menu/categories" "$OWNER" ''
  check 200 || fail "list menu categories"
  MENU=$BODY
}
category_id() { jx "$MENU" "(select c->>'id' from jsonb_array_elements($(jarr '{data,items}')) c where c->>'name' = $(sqlq "$1") limit 1)"; }
item_id() {
  jx "$MENU" "(select i->>'id' from jsonb_array_elements($(jarr '{data,items}')) c, jsonb_array_elements(case jsonb_typeof(c->'items') when 'array' then c->'items' else '[]'::jsonb end) i where i->>'name' = $(sqlq "$1") limit 1)"
}

MUTATE=1
ensure_category() { # name sort
  local id
  id=$(category_id "$1")
  if [ -z "$id" ]; then
    [ "$MUTATE" = 1 ] || die "category '$1' is missing; run the full seed first"
    food POST "/v1/food/partner/restaurants/$RID/menu/categories" "$OWNER" "{\"name\":\"$1\",\"sort_order\":$2}"
    check 201 200 || fail "create category $1"
    load_menu
    id=$(category_id "$1")
    log "category $1 created"
  fi
  printf '%s' "$id"
}
ensure_item() { # category_id name food_type price prep recommended description
  local id
  id=$(item_id "$2")
  if [ -z "$id" ]; then
    [ "$MUTATE" = 1 ] || die "dish '$2' is missing; run the full seed first"
    food POST "/v1/food/partner/restaurants/$RID/menu/items" "$OWNER" \
      "{\"category_id\":\"$1\",\"name\":\"$2\",\"description\":\"$7\",\"food_type\":\"$3\",\"base_price\":$4,\"preparation_minutes\":$5,\"is_recommended\":$6,\"tax_percentage\":5}"
    check 201 200 || fail "create dish $2"
    load_menu
    id=$(item_id "$2")
    log "dish $2 created"
  fi
  printf '%s' "$id"
}
ensure_variant() { # item name paise sort
  local id
  food GET "/v1/food/partner/menu/items/$1/variants" "$OWNER" ''
  check 200 || fail "list variants"
  id=$(jx "$BODY" "(select v->>'id' from jsonb_array_elements($(jarr '{data,items}')) v where v->>'name' = $(sqlq "$2") limit 1)")
  if [ -z "$id" ]; then
    [ "$MUTATE" = 1 ] || die "variant '$2' is missing; run the full seed first"
    food POST "/v1/food/partner/menu/items/$1/variants" "$OWNER" "{\"name\":\"$2\",\"price_paise\":$3,\"is_available\":true,\"sort_order\":$4}"
    check 201 || fail "create variant $2"
    id=$(jget "$BODY" '{data,id}')
    log "variant $2 created"
  fi
  printf '%s' "$id"
}
ensure_addon() { # item group_name addon_name paise — prints the add-on id
  local gid aid groups
  food GET "/v1/food/partner/menu/items/$1/addon-groups" "$OWNER" ''
  check 200 || fail "list add-on groups"
  groups=$BODY
  gid=$(jx "$groups" "(select g->>'id' from jsonb_array_elements($(jarr '{data,items}')) g where g->>'name' = $(sqlq "$2") limit 1)")
  if [ -z "$gid" ]; then
    [ "$MUTATE" = 1 ] || die "add-on group '$2' is missing; run the full seed first"
    food POST "/v1/food/partner/menu/items/$1/addon-groups" "$OWNER" "{\"name\":\"$2\",\"min_select\":0,\"max_select\":2,\"is_required\":false,\"sort_order\":0}"
    check 201 || fail "create add-on group $2"
    gid=$(jget "$BODY" '{data,id}')
    groups='{"data":{"items":[]}}'
    log "add-on group $2 created"
  fi
  aid=$(jx "$groups" "(select a->>'id' from jsonb_array_elements($(jarr '{data,items}')) g, jsonb_array_elements(case jsonb_typeof(g->'addons') when 'array' then g->'addons' else '[]'::jsonb end) a where g->>'id' = $(sqlq "$gid") and a->>'name' = $(sqlq "$3") limit 1)")
  if [ -z "$aid" ]; then
    [ "$MUTATE" = 1 ] || die "add-on '$3' is missing; run the full seed first"
    food POST "/v1/food/partner/menu/items/$1/addon-groups/$gid/addons" "$OWNER" "{\"name\":\"$3\",\"price_paise\":$4,\"is_available\":true,\"sort_order\":0}"
    check 201 || fail "create add-on $3"
    aid=$(jget "$BODY" '{data,id}')
    log "add-on $3 created"
  fi
  printf '%s' "$aid"
}

ITEM_TIKKA='' ITEM_BIRYANI='' VAR_FULL='' ADDON_RAITA=''
ensure_menu() {
  local c_start c_main item_pbm
  load_menu
  c_start=$(ensure_category Starters 1)
  c_main=$(ensure_category Mains 2)
  ITEM_TIKKA=$(ensure_item "$c_start" "Paneer Tikka" VEG 220 15 false "Charred cottage cheese, mint chutney")
  ensure_item "$c_start" "Masala Fries" VEG 120 10 false "Crisp fries, house masala" >/dev/null
  ITEM_BIRYANI=$(ensure_item "$c_main" "Chicken Biryani" NON_VEG 280 25 true "Dum biryani; choose half or full")
  item_pbm=$(ensure_item "$c_main" "Paneer Butter Masala" VEG 260 20 false "Tomato-butter gravy")
  ensure_variant "$ITEM_BIRYANI" Half 18000 0 >/dev/null
  VAR_FULL=$(ensure_variant "$ITEM_BIRYANI" Full 28000 1)
  ADDON_RAITA=$(ensure_addon "$ITEM_BIRYANI" Sides Raita 3000)
  ensure_addon "$ITEM_BIRYANI" Sides "Mirchi Salan" 2500 >/dev/null
  ensure_addon "$item_pbm" Breads "Butter Naan" 4500 >/dev/null
  [ -n "$ITEM_TIKKA" ] && [ -n "$ITEM_BIRYANI" ] && [ -n "$VAR_FULL" ] && [ -n "$ADDON_RAITA" ] || die "menu lookup incomplete"
}

seed_restaurant() {
  step "Restaurant: $RESTAURANT_NAME (owner $OWNER)"
  find_restaurant
  if [ -z "$RID" ]; then
    food POST /v1/food/partner/restaurants "$OWNER" \
      "{\"name\":\"$RESTAURANT_NAME\",\"display_name\":\"$RESTAURANT_NAME\",\"legal_name\":\"Feast Test Kitchen Dev Seed\",\"slug\":\"feast-test-kitchen-dev-seed\",\"description\":\"Dev seed kitchen for Feast Kitchen and Feast Rider testing\",\"phone\":\"08040000123\",\"email\":\"feast-kitchen@example.test\",\"address_line1\":\"80 Feet Road, Koramangala 4th Block\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"postal_code\":\"560034\",\"min_order_amount\":0,\"packaging_fee\":10}"
    check 201 200 || fail "create restaurant"
    RID=$(jget "$BODY" '{data,id}')
    log "created restaurant $RID"
  else
    log "reusing restaurant $RID"
  fi
  load_restaurant
  log "status $RSTATUS"

  case "$RSTATUS" in
    DRAFT|REJECTED)
      food PUT "/v1/food/partner/restaurants/$RID/location" "$OWNER" \
        "{\"latitude\":$REST_LAT,\"longitude\":$REST_LNG,\"address_line1\":\"80 Feet Road, Koramangala 4th Block\",\"address_line2\":\"Dev seed\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"postal_code\":\"560034\",\"delivery_radius_km\":10}"
      check 200 || fail "put location"
      log "location set (10 km radius, service area $(jget "$BODY" '{data,service_area_id}'))"

      local windows='' d
      for d in 0 1 2 3 4 5 6; do windows+="{\"day_of_week\":$d,\"opens_at\":\"00:00\",\"closes_at\":\"23:59\",\"is_closed\":false},"; done
      food PUT "/v1/food/partner/restaurants/$RID/operating-hours" "$OWNER" "{\"windows\":[${windows%,}]}"
      check 200 || fail "put operating hours"
      log "hours set (00:00-23:59 every day, open now: $(jget "$BODY" '{data,is_open_now}'))"

      food PUT "/v1/food/partner/restaurants/$RID/compliance" "$OWNER" \
        "{\"tax_category\":\"RESTAURANT_STANDALONE\",\"legal_name\":\"Feast Test Kitchen Dev Seed\",\"pan\":\"$PAN\"}"
      check 200 || fail "put compliance"
      log "compliance set ($(jget "$BODY" '{data,tax_category}'), gst_liability $(jget "$BODY" '{data,gst_liability}'))"

      food GET "/v1/food/partner/restaurants/$RID/fssai" "$OWNER" ''
      local fstatus=''
      check 200 && fstatus=$(jget "$BODY" '{data,document,status}')
      if [ "$fstatus" != PENDING ] && [ "$fstatus" != APPROVED ]; then
        local media expires
        media=$(pick_media "$OWNER")
        [ -n "$media" ] || die "no ready image in public.media_assets to attach to the FSSAI document"
        expires=$(date -d '+1 year' +%F)
        food PUT "/v1/food/partner/restaurants/$RID/fssai" "$OWNER" \
          "{\"licence_number\":\"$FSSAI\",\"expires_at\":\"$expires\",\"media_id\":\"$media\"}"
        check 200 || fail "put fssai"
        log "FSSAI submitted (expires $expires)"
      else
        log "FSSAI document already $fstatus"
      fi

      food GET "/v1/food/partner/restaurants/$RID/payout-account" "$OWNER" ''
      if ! check 200; then
        food PUT "/v1/food/partner/restaurants/$RID/payout-account" "$OWNER" \
          '{"holder_name":"Feast Test Kitchen","account_number":"765432123456789","ifsc":"HDFC0000053"}'
        check 200 || fail "put restaurant payout account"
        log "payout account set"
      else
        log "payout account already on file"
      fi
      ;;
  esac

  ensure_menu
  log "menu ready (2 categories, 4 dishes, variants and add-ons)"

  if [ "$RSTATUS" = DRAFT ] || [ "$RSTATUS" = REJECTED ]; then
    food GET "/v1/food/partner/restaurants/$RID/readiness" "$OWNER" ''
    check 200 || fail "get readiness"
    log "readiness: ready=$(jget "$BODY" '{data,ready}') missing=$(jx "$BODY" "(j #> '{data,missing}')::text")"
    food POST "/v1/food/partner/restaurants/$RID/submit" "$OWNER" '{}'
    check 200 || fail "submit restaurant"
    log "submitted -> $(jget "$BODY" '{data,status}')"
    load_restaurant
  fi

  if [ "$RSTATUS" = PENDING_REVIEW ]; then
    food GET "/v1/food/partner/restaurants/$RID/fssai" "$OWNER" ''
    check 200 || fail "get fssai"
    local doc dstatus
    doc=$(jget "$BODY" '{data,document,id}')
    dstatus=$(jget "$BODY" '{data,document,status}')
    if [ "$dstatus" = PENDING ]; then
      food POST "/v1/food/admin/restaurants/$RID/documents/$doc/decide" "$ADMIN" '{"decision":"APPROVED"}' admin
      check 200 || fail "admin approve FSSAI document"
      log "admin: FSSAI document -> $(jget "$BODY" '{data,status}')"
    fi
    food POST "/v1/food/admin/restaurants/$RID/approve" "$ADMIN" '{}' admin
    check 200 || fail "admin approve restaurant"
    log "admin: restaurant approved"
    load_restaurant
  fi

  if [ "$RSTATUS" = ACTIVE ] && [ "$RACCEPTING" != true ]; then
    food PATCH "/v1/food/partner/restaurants/$RID/accepting" "$OWNER" '{"is_accepting_orders":true}'
    check 200 || fail "set accepting orders"
    load_restaurant
  fi
  [ "$RSTATUS" = ACTIVE ] || die "restaurant ended $RSTATUS, expected ACTIVE"
  log "restaurant $RSTATUS, accepting orders: $RACCEPTING"
}

# ─── Rider ──────────────────────────────────────────────────────────────────
PID='' PSTATUS='' PONLINE=''
load_partner() {
  food GET /v1/food/delivery/profile "$RIDER" ''
  if check 404; then PID='' PSTATUS='' PONLINE=''; return 0; fi
  check 200 || fail "get delivery profile"
  PID=$(jget "$BODY" '{data,id}')
  PSTATUS=$(jget "$BODY" '{data,status}')
  PONLINE=$(jget "$BODY" '{data,is_online}')
}

seed_rider() {
  step "Rider (user $RIDER)"
  load_partner
  if [ -z "$PID" ]; then
    food POST /v1/food/delivery/profile "$RIDER" \
      "{\"full_name\":\"Feast Test Rider\",\"phone\":\"+919000000102\",\"email\":\"feast-rider@example.test\",\"vehicle_type\":\"MOTORCYCLE\",\"vehicle_number\":\"$RC_NUMBER\",\"city\":\"Bengaluru\"}"
    check 200 201 || fail "create delivery profile"
    load_partner
    log "created partner $PID ($PSTATUS)"
  else
    log "reusing partner $PID ($PSTATUS)"
  fi

  case "$PSTATUS" in
    DRAFT|PENDING_REVIEW|REJECTED)
      local kyc media
      food GET /v1/food/delivery/kyc/status "$RIDER" ''
      check 200 || fail "get kyc status"
      kyc=$BODY
      local has_aadhaar
      has_aadhaar=$(jx "$kyc" "exists(select 1 from jsonb_array_elements($(jarr '{data,checks}')) c where c->>'kind' = 'AADHAAR')")
      if [ "$has_aadhaar" != t ]; then
        # DigiLocker dev mock: start -> dev authorize (302 with code) -> callback.
        local state code
        food POST /v1/food/delivery/kyc/digilocker/start "$RIDER" '{}'
        check 200 || fail "digilocker start"
        [[ $BODY =~ \"state\":\"([^\"]+)\" ]] || die "digilocker start returned no state"
        state=${BASH_REMATCH[1]}
        food GET "/v1/food/dev/digilocker/authorize?state=$state" "$RIDER" ''
        check 302 || fail "digilocker dev authorize"
        [[ $REDIRECT =~ [?\&]code=([^\&]+) ]] || die "dev authorize redirect carried no code"
        code=${BASH_REMATCH[1]}
        food POST /v1/food/delivery/kyc/digilocker/callback "$RIDER" "{\"code\":\"$code\",\"state\":\"$state\"}"
        check 200 || fail "digilocker callback"
        unset state code
        kyc=$BODY
        log "DigiLocker (mock) verified: $(jx "$kyc" "(select string_agg(c->>'kind', ', ') from jsonb_array_elements($(jarr '{data,checks}')) c)")"
      else
        log "DigiLocker checks already present"
      fi

      media=$(pick_media "$RIDER")
      [ -n "$media" ] || die "no ready image in public.media_assets for rider documents"
      local spec type number tail have
      for spec in "DRIVING_LICENCE $DL_NUMBER $DL_MASK_TAIL" "VEHICLE_RC $RC_NUMBER $RC_MASK_TAIL"; do
        set -- $spec
        type=$1 number=$2 tail=$3
        have=$(jx "$kyc" "exists(select 1 from jsonb_array_elements($(jarr '{data,documents}')) d where d->>'document_type' = '$type' and d->>'media_id' is not null and coalesce(d->>'number_masked','') like '%$tail' and d->>'status' in ('PENDING','APPROVED'))")
        if [ "$have" = t ]; then log "$type document already uploaded"; continue; fi
        food POST /v1/food/delivery/documents "$RIDER" "{\"document_type\":\"$type\",\"document_number\":\"$number\",\"media_id\":\"$media\"}"
        if check 409; then
          warn "$type upload refused: $(jget "$BODY" '{error,code}') (the DigiLocker copy still satisfies the step)"
        else
          check 201 200 || fail "upload $type"
          log "$type uploaded (masked $(jget "$BODY" '{data,number_masked}'))"
        fi
      done

      have=$(jx "$kyc" "exists(select 1 from jsonb_array_elements($(jarr '{data,documents}')) d where d->>'document_type' = 'SELFIE' and d->>'status' in ('PENDING','APPROVED'))")
      if [ "$have" != t ]; then
        food POST /v1/food/delivery/documents "$RIDER" "{\"document_type\":\"SELFIE\",\"media_id\":\"$media\"}"
        check 201 200 || fail "upload selfie"
        log "selfie uploaded"
      else
        log "selfie already uploaded"
      fi

      food GET /v1/food/delivery/payout-account "$RIDER" ''
      if ! check 200; then
        food PUT /v1/food/delivery/payout-account "$RIDER" \
          '{"holder_name":"Feast Test Rider","account_number":"123456789012345678","ifsc":"SBIN0001234"}'
        check 200 || fail "put rider payout account"
        log "payout account set"
      else
        log "payout account already on file"
      fi

      food GET "/v1/food/admin/delivery-partners/$PID/kyc" "$ADMIN" '' admin
      check 200 || fail "admin get partner kyc"
      local pending doc
      pending=$(jx "$BODY" "(select coalesce(string_agg(d->>'id', ' '), '') from jsonb_array_elements($(jarr '{data,documents}')) d where d->>'status' = 'PENDING')")
      for doc in $pending; do
        food POST "/v1/food/admin/delivery-partners/$PID/documents/$doc/decide" "$ADMIN" '{"decision":"APPROVED"}' admin
        check 200 || fail "admin approve rider document $doc"
        log "admin: $(jget "$BODY" '{data,document_type}') -> $(jget "$BODY" '{data,status}')"
      done

      food GET "/v1/food/admin/delivery-partners/$PID/kyc" "$ADMIN" '' admin
      check 200 || fail "admin get partner kyc"
      log "kyc missing before approval: $(jx "$BODY" "(j #> '{data,missing}')::text")"

      food POST "/v1/food/admin/delivery-partners/$PID/approve" "$ADMIN" '{}' admin
      check 200 || fail "admin approve delivery partner"
      log "admin: partner approved"
      load_partner
      ;;
  esac

  if [ "$PSTATUS" = APPROVED ] || [ "$PSTATUS" = OFFLINE ]; then
    food PATCH "/v1/food/admin/delivery-partners/$PID/status" "$ADMIN" '{"status":"ACTIVE"}' admin
    check 200 || fail "admin set partner ACTIVE"
    load_partner
  fi
  [ "$PSTATUS" = ACTIVE ] || die "rider ended $PSTATUS, expected ACTIVE"
  log "partner $PSTATUS, online: $PONLINE (not changed by this script)"
}

# ─── Order ──────────────────────────────────────────────────────────────────
OID='' ONUM='' OSTATUS='' OPAY='' OAMOUNT='' OBREACH=''
seed_order() {
  step "Order (customer $CUSTOMER)"
  if [ "$ORDER_ONLY" = 1 ]; then
    find_restaurant
    [ -n "$RID" ] || die "no '$RESTAURANT_NAME' for the owner; run the full seed first"
    load_restaurant
    [ "$RSTATUS" = ACTIVE ] && [ "$RACCEPTING" = true ] || die "restaurant is $RSTATUS (accepting=$RACCEPTING); run the full seed first"
    MUTATE=0
    ensure_menu
  fi

  local addr
  food GET /v1/food/addresses "$CUSTOMER" ''
  check 200 || fail "list addresses"
  addr=$(jx "$BODY" "(select a->>'id' from jsonb_array_elements(case jsonb_typeof(j->'data') when 'array' then j->'data' else $(jarr '{data,items}') end) a where a->>'label' = $(sqlq "$ADDRESS_LABEL") limit 1)")
  if [ -z "$addr" ]; then
    food POST /v1/food/addresses "$CUSTOMER" \
      "{\"label\":\"$ADDRESS_LABEL\",\"receiver_name\":\"Feast Test Customer\",\"phone\":\"+919000000103\",\"address_line1\":\"27th Main Road, HSR Layout Sector 1\",\"landmark\":\"Dev seed\",\"city\":\"Bengaluru\",\"state\":\"Karnataka\",\"country\":\"India\",\"postal_code\":\"560102\",\"latitude\":$ADDR_LAT,\"longitude\":$ADDR_LNG,\"is_default\":false}"
    check 201 200 || fail "create address"
    addr=$(jget "$BODY" '{data,id}')
    log "address created (~2.5 km from the restaurant)"
  else
    log "reusing address $addr"
  fi

  food DELETE /v1/food/cart "$CUSTOMER" ''
  check 200 204 404 || fail "clear cart"
  food POST /v1/food/cart/items "$CUSTOMER" \
    "{\"menu_item_id\":\"$ITEM_BIRYANI\",\"variant_id\":\"$VAR_FULL\",\"quantity\":1,\"clear_existing\":true,\"addons\":[{\"addon_id\":\"$ADDON_RAITA\",\"quantity\":1}]}"
  check 201 200 || fail "add biryani (Full + Raita) to cart"
  food POST /v1/food/cart/items "$CUSTOMER" "{\"menu_item_id\":\"$ITEM_TIKKA\",\"quantity\":1}"
  check 201 200 || fail "add paneer tikka to cart"
  log "cart: Chicken Biryani (Full + Raita), Paneer Tikka"

  food POST /v1/food/orders "$CUSTOMER" \
    "{\"address_id\":\"$addr\",\"payment_method\":\"upi\",\"customer_instruction\":\"Dev seed order\"}" idem
  check 201 200 || fail "place order"
  OID=$(jget "$BODY" '{data,id}')
  log "placed order $OID ($(jget "$BODY" '{data,status}'))"

  food POST "/v1/food/orders/$OID/payments/intents" "$CUSTOMER" '{"method":"upi"}' idem
  check 201 200 || fail "create payment intent"
  local rzp_order amount currency
  rzp_order=$(jget "$BODY" '{data,provider_order_id}')
  amount=$(jget "$BODY" '{data,payment_intent,amount_minor}')
  currency=$(jget "$BODY" '{data,payment_intent,currency}')
  [[ $rzp_order == order_* ]] || die "payment intent has no Razorpay order (provider_order_id='$rzp_order'); cannot drive a dev capture"
  log "payment intent created (Razorpay test order, $amount ${currency:-INR} paise)"

  # Dev payment success: a Razorpay-format payment.captured webhook, signed
  # with payments-service's TEST-mode webhook secret, sent to the real
  # webhook route. payments-service verifies it, settles the intent and writes
  # payment.succeeded to its outbox -> Kafka social.events.v1 -> food-service's
  # consumer marks the order paid and CONFIRMED. No status is written directly.
  [ "$PAY_TEST" = yes ] || die "refusing: payments-service is not on Razorpay test keys"
  local secret wbody sig evt
  secret=$(docker exec "$PAY_C" printenv RAZORPAY_WEBHOOK_SECRET 2>/dev/null || true)
  [ -n "$secret" ] || die "payments-service has no RAZORPAY_WEBHOOK_SECRET"
  evt="evt_devseed_$(openssl rand -hex 8)"
  wbody="{\"entity\":\"event\",\"account_id\":\"acc_devseed\",\"event\":\"payment.captured\",\"contains\":[\"payment\"],\"payload\":{\"payment\":{\"entity\":{\"id\":\"pay_$(openssl rand -hex 7)\",\"entity\":\"payment\",\"amount\":$amount,\"currency\":\"${currency:-INR}\",\"status\":\"captured\",\"order_id\":\"$rzp_order\",\"method\":\"upi\",\"captured\":true}}},\"created_at\":$(date +%s)}"
  sig=$(hmac_sha256_hex "$secret" "$wbody")
  unset secret
  http POST "$PAY_URL/v1/payments/webhook" "$wbody" "Content-Type: application/json" "X-Razorpay-Signature: $sig" "X-Razorpay-Event-Id: $evt"
  unset sig
  check 200 || fail "payments webhook (payment.captured)"
  log "payments-service accepted the signed payment.captured webhook"

  local deadline=$((SECONDS + 60)) pstatus=''
  while :; do
    food GET "/v1/food/orders/$OID/payment" "$CUSTOMER" ''
    check 200 || fail "get order payment"
    pstatus=$(jget "$BODY" '{data,status}')
    [ "$pstatus" = paid ] && break
    [ "$pstatus" = failed ] && die "payment status became failed"
    ((SECONDS < deadline)) || die "payment still '$pstatus' after 60 s (consumer did not apply payment.succeeded)"
    sleep 2
  done
  OPAY=$pstatus
  log "payment status: paid"

  food GET "/v1/food/orders/$OID" "$CUSTOMER" ''
  check 200 || fail "get order"
  ONUM=$(jget "$BODY" '{data,order_number}')
  OSTATUS=$(jget "$BODY" '{data,status}')
  OAMOUNT=$(jget "$BODY" '{data,money,totals_paise,final_amount_paise}')

  food GET "/v1/food/partner/restaurants/$RID/kitchen-queue" "$OWNER" ''
  check 200 || fail "get kitchen queue"
  OBREACH=$(jx "$BODY" "(select o->>'seconds_to_breach' from jsonb_array_elements($(jarr '{data,orders}')) o where o->>'id' = $(sqlq "$OID") limit 1)")
}

# ─── Main ───────────────────────────────────────────────────────────────────
echo "Feast dev seed — food-service $FOOD_URL (ENV=$FOOD_ENV), payments $PAY_URL (razorpay test keys: $PAY_TEST)"
if [ "$ORDER_ONLY" = 0 ]; then
  seed_restaurant
  seed_rider
else
  load_partner
fi
seed_order
unset KEY

step "Summary"
printf '   %-11s %s  id=%s  status=%s  accepting=%s\n' Restaurant "$RESTAURANT_NAME" "$RID" "$RSTATUS" "$RACCEPTING"
printf '   %-11s partner_id=%s  status=%s  online=%s\n' Rider "${PID:-none}" "${PSTATUS:-none}" "${PONLINE:-n/a}"
printf '   %-11s id=%s  number=%s  status=%s  payment=%s\n' Order "$OID" "$ONUM" "$OSTATUS" "$OPAY"
printf '   %-11s final_amount_paise=%s  seconds_to_breach=%s\n' '' "$OAMOUNT" "${OBREACH:-n/a}"
cat <<EOF

Next manual steps
   1. Feast Kitchen phone (call_a): open $RESTAURANT_NAME -> the order is in the
      kitchen queue. Accept it within ${OBREACH:-the SLA} s or it auto-rejects;
      re-run with --order-only for a fresh one.
   2. Feast Rider phone (call_b): go online, then take the delivery offer once
      the kitchen accepts and marks the order preparing/ready.
   3. Kitchen: mark preparing -> ready; verify the rider's pickup code. Rider:
      picked up -> arrived -> enter the customer's delivery code -> delivered.
EOF
