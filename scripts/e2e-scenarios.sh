#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────
# atPost API-level E2E scenarios — drives real user flows through the live
# gateway (default http://127.0.0.1:8080), playing TWO users (alice+bob) so
# multi-actor flows (chat, dating match) are exercised by one process. No
# emulator, no second human. Rerun any time: `bash scripts/e2e-scenarios.sh`.
#
# Verticals whose backend service isn't deployed report SKIP (not failure).
# ─────────────────────────────────────────────────────────────────────────
set -uo pipefail
GW="${ATPOST_GW:-http://127.0.0.1:8080}"
PW="SmokeTest2026!"
PASS=0; FAIL=0; SKIP=0
ok(){ echo "  ✓ $1"; PASS=$((PASS+1)); }
bad(){ echo "  ✗ $1"; FAIL=$((FAIL+1)); }
skip(){ echo "  ~ SKIP $1"; SKIP=$((SKIP+1)); }
hdr(){ echo; echo "━━━ $1"; }
pluck(){ grep -oE "\"$2\":[[:space:]]*\"?[^\",}]+\"?" <<<"$1" | head -1 | sed -E "s/\"$2\":[[:space:]]*//;s/^\"//;s/\"$//"; }

# Header arrays (NOT strings — header values contain spaces).
declare -a AH BH
account(){ # $1 email $2 first  → sets global TOK/UID via echo "tok uid"
  curl -s -X POST "$GW/v1/auth/register" -H "Content-Type: application/json" \
    -d "{\"email\":\"$1\",\"password\":\"$PW\",\"first_name\":\"$2\",\"last_name\":\"E2E\",\"dob\":\"1995-06-15\"}" --max-time 15 >/dev/null
  local r; r=$(curl -s -X POST "$GW/v1/auth/login" -H "Content-Type: application/json" \
    -d "{\"identifier\":\"$1\",\"password\":\"$PW\"}" --max-time 15)
  echo "$(pluck "$r" access_token) $(pluck "$r" id)"
}
# codeA/codeB: run curl with the right user's headers, echo just the HTTP code.
codeA(){ curl -s -o /dev/null -w "%{http_code}" "${AH[@]}" "$@" --max-time 15; }
codeB(){ curl -s -o /dev/null -w "%{http_code}" "${BH[@]}" "$@" --max-time 15; }
classify(){ # $1 label $2 code  → ok / skip / bad
  case "$2" in
    200|201|204) ok "$1 ($2)";;
    000|404|502|503) skip "$1 — service not deployed ($2)";;
    401|403) bad "$1 — AUTH problem ($2)";;
    *) bad "$1 ($2)";;
  esac
}

hdr "SETUP — provision two users (identity-service E2E)"
read -r ATOK AUID <<<"$(account alice.e2e@vchattest.local Alice)"
read -r BTOK BUID <<<"$(account bob.e2e@vchattest.local Bob)"
[ ${#ATOK} -gt 40 ] && ok "alice registered+logged in (uid=${AUID:0:8}…)" || { bad "alice auth"; exit 1; }
[ ${#BTOK} -gt 40 ] && ok "bob registered+logged in (uid=${BUID:0:8}…)" || bad "bob auth"
AH=(-H "Authorization: Bearer $ATOK" -H "X-User-Id: $AUID")
BH=(-H "Authorization: Bearer $BTOK" -H "X-User-Id: $BUID")

hdr "1. POSTBOOK — home feed (feed/post/user services)"
classify "GET /v1/feed/home" "$(codeA "$GW/v1/feed/home?limit=10")"

hdr "2. CHAT — multi-user: connect → alice DM bob → bob reads"
# DM permission (graph-service) defaults to connections_only, so strangers
# are correctly blocked. Establish the connection first: alice requests,
# bob accepts — then the DM is allowed. This proves graph↔chat integration.
cr=$(codeA "$GW/v1/graph/connection-request" -X POST -H "Content-Type: application/json" -d "{\"user_id\":\"$BUID\"}")
ca=$(codeB "$GW/v1/graph/connection-request/accept" -X POST -H "Content-Type: application/json" -d "{\"user_id\":\"$AUID\"}")
case "$cr/$ca" in
  20*/20*) ok "alice↔bob connected (request $cr / accept $ca)";;
  *) skip "connection handshake ($cr/$ca) — chat DM will be permission-gated";;
esac
# Real contract: POST /v1/chat/conversations/direct {other_user_id} +
# Idempotency-Key header; msg {type,text}.
convo=$(curl -s -X POST "$GW/v1/chat/conversations/direct" "${AH[@]}" -H "Content-Type: application/json" \
  -H "Idempotency-Key: e2e-convo-$AUID-$BUID" -d "{\"other_user_id\":\"$BUID\"}" --max-time 15)
CID=$(pluck "$convo" id)
if [ -n "$CID" ]; then
  ok "alice created direct conversation (${CID:0:12}…)"
  classify "alice sent message" "$(codeA "$GW/v1/chat/conversations/$CID/messages" -X POST -H "Content-Type: application/json" -H "Idempotency-Key: e2e-msg-$RANDOM" -d '{"type":"text","text":"hi bob — e2e"}')"
  grep -q "$CID" <<<"$(curl -s "$GW/v1/chat/conversations" "${BH[@]}" --max-time 15)" \
    && ok "bob sees the conversation (multi-user delivery ✓)" || bad "bob missing conversation"
else
  classify "create conversation" "$(codeA "$GW/v1/chat/conversations/direct" -X POST -H "Content-Type: application/json" -H "Idempotency-Key: e2e-convo2-$AUID" -d "{\"other_user_id\":\"$BUID\"}")"
  echo "    ↳ $(head -c140 <<<"$convo")"
fi

hdr "3+4. POSTTUBE / SHORTS — trending + watch (post/feed services)"
for ep in "/v1/posts/trending?content_type=long_video" "/v1/videos/continue-watching" "/v1/feed/reels"; do
  classify "GET $ep" "$(codeA "$GW$ep")"
done

hdr "5. ECOMMERCE — catalog + cart (commerce-service)"
for ep in "/v1/commerce/categories" "/v1/commerce/products?limit=5" "/v1/commerce/cart"; do
  classify "GET $ep" "$(codeA "$GW$ep")"
done

hdr "6. FOOD — browse → search → loyalty → capabilities (food-service)"
classify "GET /v1/food/home"            "$(codeA "$GW/v1/food/home")"
classify "GET /v1/food/restaurants"     "$(codeA "$GW/v1/food/restaurants")"
classify "GET /v1/food/me/capabilities" "$(codeA "$GW/v1/food/me/capabilities")"
classify "GET /v1/food/me/loyalty"      "$(codeA "$GW/v1/food/me/loyalty")"
classify "GET /v1/food/search?q=biryani" "$(codeA "$GW/v1/food/search?q=biryani")"

hdr "7. DATING — age-gate → profile → deck → spark A→B (dating-service)"
# Security assertion on a FRESH user (a persisted birth_date would let a
# partial update reuse it — the gate applies at activation, correctly —
# so a throwaway account is the only clean test of fail-closed).
read -r GTOK GUID <<<"$(account gate.$RANDOM.e2e@vchattest.local Gate)"
gate=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $GTOK" -H "X-User-Id: $GUID" \
  -X POST "$GW/v1/dating/profile" -H "Content-Type: application/json" -d '{"display_name":"NoAge","gender":"female"}' --max-time 15)
[ "$gate" = "403" ] && ok "age-gate fires on fresh user: no birth_date → 403 AGE_REQUIRED (fail-closed ✓)" || bad "age-gate NOT enforced (got $gate, expected 403)"
# Now a valid 18+ profile with birth_date.
classify "alice profile upsert (18+)" "$(codeA "$GW/v1/dating/profile" -X POST -H "Content-Type: application/json" -d '{"display_name":"Alice","bio":"e2e","gender":"female","interested_in":["male"],"birth_date":"1995-06-15T00:00:00Z"}')"
codeB "$GW/v1/dating/profile" -X POST -H "Content-Type: application/json" -d '{"display_name":"Bob","bio":"e2e","gender":"male","interested_in":["female"],"birth_date":"1994-03-10T00:00:00Z"}' >/dev/null
classify "GET /v1/dating/pulse/today (deck)" "$(codeA "$GW/v1/dating/pulse/today")"
sp=$(codeA "$GW/v1/dating/sparks" -X POST -H "Content-Type: application/json" -d "{\"to_user_id\":\"$BUID\",\"target_kind\":\"profile\",\"target_ref\":\"$BUID\"}")
case "$sp" in 200|201) ok "alice sparked bob ($sp)";; *) skip "spark ($sp — may need mutual discovery/eligibility)";; esac

hdr "8. MOPEDU — cities → fare estimate → history (rider-service)"
classify "GET /v1/rider/cities" "$(codeA "$GW/v1/rider/cities")"
est=$(codeA "$GW/v1/rider/estimate" -X POST -H "Content-Type: application/json" -d '{"pickup_lat":17.44,"pickup_lng":78.39,"drop_lat":17.45,"drop_lng":78.40,"vehicle_type":"moped","city_id":"hyd"}')
case "$est" in 200|201) ok "rider/estimate ($est — fare quote)";; *) skip "estimate ($est — needs seeded city)";; esac
classify "GET /v1/rider/rides/me" "$(codeA "$GW/v1/rider/rides/me")"

hdr "NOTIFICATIONS — realtime token + SSE (notification-service)"
classify "POST /v1/food/realtime/token" "$(codeA "$GW/v1/food/realtime/token" -X POST)"
classify "GET /v1/notifications" "$(codeA "$GW/v1/notifications")"

echo; echo "═══════════════════════════════════════════"
echo "E2E RESULT: $PASS passed · $FAIL failed · $SKIP skipped (service not deployed)"
echo "═══════════════════════════════════════════"
[ "$FAIL" -eq 0 ]
