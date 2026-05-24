#!/usr/bin/env bash
# Smoke test for FiGo + Mopedu critical paths.
#
# Exercises the new endpoints shipped across Waves A→F + the cf57a26
# post-wave fixes. Run AGAINST a running `docker compose up` stack —
# this is the closest substitute for a full end-to-end manual walk.
#
# Usage:
#   cd Architecture/docker
#   docker compose up -d --build
#   bash smoke-figo-mopedu.sh
#
# Environment:
#   GATEWAY_URL       — default http://localhost:8080
#   INTERNAL_KEY      — must match INTERNAL_SERVICE_KEY in local.env;
#                       defaults to "dev-internal-key" for the local
#                       docker-compose stack.
#   CUSTOMER_USER_ID  — UUID to forge X-User-Id with; defaults to a
#                       randomly generated one (fresh customer).
#
# Exit code 0 = every probed endpoint returned a 2xx OR an expected
# 4xx (e.g. 401 from /verify-delivery without prior pickup verify).
# Any other status fails the whole script.

set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
INTERNAL_KEY="${INTERNAL_KEY:-dev-internal-key}"
CUSTOMER_USER_ID="${CUSTOMER_USER_ID:-$(python -c "import uuid; print(uuid.uuid4())")}"

PASS=0
FAIL=0

probe() {
  local label="$1" want_pattern="$2"
  shift 2
  local resp http_code
  resp="$(curl -sS -o /tmp/smoke_body.json -w "%{http_code}" "$@" || echo "000")"
  http_code="$resp"
  if [[ "$http_code" =~ $want_pattern ]]; then
    echo "  PASS  $label  (HTTP $http_code)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL  $label  (HTTP $http_code, wanted $want_pattern)"
    head -c 500 /tmp/smoke_body.json
    echo
    FAIL=$((FAIL + 1))
  fi
}

H_AUTH=(-H "X-Internal-Service-Key: $INTERNAL_KEY" -H "X-User-Id: $CUSTOMER_USER_ID")

echo
echo "═══ FiGo + Mopedu smoke ══════════════════════════════════════════"
echo "  gateway          : $GATEWAY_URL"
echo "  customer_user_id : $CUSTOMER_USER_ID"
echo

echo "── Realtime gateway ─────────────────────────────────────────────"
# Token-issuance endpoints from rider + food.
probe "POST /v1/rider/realtime/token"      '^(20[01])$'  -X POST "${H_AUTH[@]}" "$GATEWAY_URL/v1/rider/realtime/token"
probe "POST /v1/food/realtime/token"       '^(20[01])$'  -X POST "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/realtime/token"
# SSE requires a valid token — without one, the gateway returns 401.
probe "GET /v1/realtime/sse (no token)"    '^401$'       "${H_AUTH[@]}" "$GATEWAY_URL/v1/realtime/sse"

echo
echo "── FiGo capabilities + role gating (P0.5 + A3) ──────────────────"
probe "GET /v1/food/me/capabilities"       '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/me/capabilities"

echo
echo "── FiGo customer read paths ─────────────────────────────────────"
probe "GET /v1/food/home"                  '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/home"
probe "GET /v1/food/restaurants"           '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/restaurants"
probe "GET /v1/food/cart"                  '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/cart"
probe "GET /v1/food/orders"                '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/orders"
probe "GET /v1/food/addresses"             '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/addresses"
# Customer-facing public read for item reviews (B7) — empty UUID → 400.
probe "GET item reviews on bad id"         '^(400|404)$' "$GATEWAY_URL/v1/food/menu-items/00000000-0000-0000-0000-000000000000/reviews"

echo
echo "── FiGo support / refunds (B6) ──────────────────────────────────"
probe "GET /v1/food/support/tickets/me"    '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/food/support/tickets/me"

echo
echo "── FiGo admin reports (D1) ──────────────────────────────────────"
H_ADMIN=("${H_AUTH[@]}" -H "X-Scopes: admin")
probe "GET admin compliance"               '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/reports/compliance"
probe "GET admin restaurant-sla"           '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/reports/restaurant-sla"
probe "GET admin payment-recon"            '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/reports/payment-recon"
probe "GET admin refunds"                  '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/reports/refunds"
probe "GET admin coupon-abuse"             '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/reports/coupon-abuse"
probe "GET admin moderation/queue"         '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/moderation/queue"
probe "GET admin fraud/top"                '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/food/admin/fraud/top"

echo
echo "── Mopedu customer read paths ───────────────────────────────────"
probe "GET /v1/rider/cities"               '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/rider/cities"
probe "GET /v1/rider/rides/me"             '^(20[01])$'  "${H_AUTH[@]}" "$GATEWAY_URL/v1/rider/rides/me"

echo
echo "── Mopedu admin reports (D2) ────────────────────────────────────"
probe "GET admin matching-health"          '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/rider/admin/reports/matching-health"
probe "GET admin partner-quality"          '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/rider/admin/reports/partner-quality"
probe "GET admin supply-demand"            '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/rider/admin/reports/supply-demand"
probe "GET admin safety"                   '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/rider/admin/reports/safety"
probe "GET admin compliance"               '^(20[01])$'  "${H_ADMIN[@]}" "$GATEWAY_URL/v1/rider/admin/reports/compliance"

echo
echo "── Result ────────────────────────────────────────────────────────"
echo "  PASS: $PASS    FAIL: $FAIL"
echo

if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
exit 0
