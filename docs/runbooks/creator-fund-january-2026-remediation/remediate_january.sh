#!/usr/bin/env bash
# January remediation, steps 3, 4a and 4b of 02_january_remediation.md:
# the 19 admin reversals through POST /v1/monetization/admin/creator-fund/earnings/:id/reverse.
#
# Written 12 September 2026 (reviewer correction E). v1 (sha256 883d2c6c…) executed on the
# development stack 11 Sep 2026 18:22 UTC, 19/19 ok — see EXECUTION-RECORD-dev-2026-09-11.md.
# v2 (this file): forced recreate + StartedAt log window; reversal logic unchanged.
#
# What it guarantees:
#   - set -euo pipefail: the first failing command stops the run.
#   - Every call checks the HTTP status AND the body against the embedded
#     expectation table (net_reversed_paise, fee_reversed_paise, fee_leg_absent)
#     for that id, and stops at the first mismatch.
#   - Every response is written to $EVID/exec/<id>.json before it is checked.
#   - --resume skips any id whose response file already shows "status":"reversed".
#   - A trap on ANY exit (success, failure, Ctrl-C) restores
#     MONETIZATION_MAINTENANCE=false, confirms MONETIZATION_PAYOUTS_ENABLED=false,
#     recreates the container, and verifies the admin route answers 503 again.
#
# What it needs in the environment (nothing is read from a file):
#   ADMIN_ID              the operator's user_id from identity_db (see runbook step 0.3)
#   INTERNAL_SERVICE_KEY  the value from Architecture/docker/.env — export it in this
#                         shell, e.g.  export $(grep -E '^INTERNAL_SERVICE_KEY=' Architecture/docker/.env)
#   EVID                  the phase0-evidence folder (defaults to this script's folder)
#   MON                   defaults to http://127.0.0.1:8099/v1/monetization
#   COMPOSE_DIR           defaults to /c/workspace/modernsmapp/Architecture/docker
#   EXPECTED_IMAGE_ID     the approved image id from runbook step 0.1; the script refuses
#                         to run if the running container's image differs.
#
# Requires: bash, curl, docker, docker compose, grep, sed. No jq, no python.

set -euo pipefail

# ---------------------------------------------------------------------------
# Expectation table: id | step | net_reversed_paise | fee_reversed_paise | fee_leg_absent
# Source: 02_january_remediation.md, per-row table (read-only query 2026-09-11)
# and the HEAD dry run (dryrun_january_remediation_3eb2e8b8.log).
# ---------------------------------------------------------------------------
EXPECT='
9517e3ab-f5c4-49d3-b25f-b560a10c4015|3|0|0|false
bd6fa147-55a8-4559-9bb8-b8c57fdc63ad|3|0|0|false
cb506055-60e1-44ad-afba-e269bcbbc549|4a|35000|15000|false
37feeea7-fb77-4b9c-b20f-30f35280b83c|4a|29852|12793|false
f875006e-ea59-4630-b55f-0b84be3c48c4|4a|29852|12793|false
1a595a21-7aa4-470a-85c6-274fd655913c|4a|43579|18676|false
05c25f5c-4852-4dbe-bc55-12c4981c2ab4|4a|35000|15000|false
0e8b1cbc-376e-4c81-b5de-56d047d6d00f|4a|35000|15000|false
f1cc0a32-af03-4b80-913a-4c1c12ffcc43|4a|43579|18676|false
e407b673-4bcf-41cc-b29e-b27ce3eaf270|4a|29852|12793|false
df3f1a4f-1541-4ca2-9df0-0df902839a17|4a|35000|15000|false
ec3efbf5-60a1-4d5a-bc66-ec86b292d5fa|4a|43579|18676|false
15cad66a-50a5-4548-b26e-4f6816e1989d|4a|29852|12793|false
14b8d184-9f81-4d44-adb6-2dfafc32c6a1|4a|43579|18676|false
920501ec-577b-439b-98ba-9c0e81481fc8|4a|17500|7500|false
8ebee831-d4d3-495b-b1a6-ba78dd183d67|4a|17500|7500|false
2e4232a3-3ea8-48e9-928c-5fd9beeaf4fe|4a|17500|7500|false
e51ecb3d-53e5-42b0-96f5-d750c5e551c4|4b|29852|0|true
00b74229-63af-4c41-8a6b-4bc3b848abc6|4b|17500|0|true
'
# Totals the table must add up to (reviewer-accepted arithmetic).
EXPECT_ROWS=19
EXPECT_NET_TOTAL=533576
EXPECT_FEE_TOTAL=208376

REASON_3='M-01: migration-017 artefact; no credit transaction and no ledger leg exist; reversed per plan Phase 2A'
REASON_4A='M-01: January 2026 accrual priced from analytics rows with no hourly events (integration-test fixture); reversed per plan Phase 2A'
REASON_4B='M-01: January 2026 accrual priced from analytics rows with no hourly events (integration-test fixture); credited 2026-09-06 before the fee leg existed, net reversed only; reversed per plan Phase 2A'

# ---------------------------------------------------------------------------
# Environment
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EVID="${EVID:-$SCRIPT_DIR}"
MON="${MON:-http://127.0.0.1:8099/v1/monetization}"
COMPOSE_DIR="${COMPOSE_DIR:-/c/workspace/modernsmapp/Architecture/docker}"
CONTAINER="${CONTAINER:-atpost_stack-monetization-service-1}"
RESUME=0
for arg in "$@"; do
  case "$arg" in
    --resume) RESUME=1 ;;
    *) echo "unknown argument: $arg (only --resume is accepted)" >&2; exit 2 ;;
  esac
done
: "${ADMIN_ID:?ADMIN_ID (operator user_id, UUID) must be exported}"
: "${INTERNAL_SERVICE_KEY:?INTERNAL_SERVICE_KEY must be exported in this shell (never written to a file)}"
: "${EXPECTED_IMAGE_ID:?EXPECTED_IMAGE_ID (from runbook step 0.1) must be exported}"
case "$ADMIN_ID" in
  [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]-*) ;;
  *) echo "ADMIN_ID does not look like a UUID: $ADMIN_ID" >&2; exit 2 ;;
esac
mkdir -p "$EVID/exec"
LOG="$EVID/exec/run.log"
log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG" >&2; }

# ---------------------------------------------------------------------------
# The trap: whatever happens, the boundary is closed again on the way out.
# ---------------------------------------------------------------------------
restore_boundary() {
  local rc=$?
  set +e
  log "EXIT (rc=$rc): restoring MONETIZATION_MAINTENANCE=false, MONETIZATION_PAYOUTS_ENABLED=false"
  ( cd "$COMPOSE_DIR" && MONETIZATION_MAINTENANCE=false MONETIZATION_WRITES_ENABLED=false MONETIZATION_PAYOUTS_ENABLED=false \
      docker compose up -d --no-deps monetization-service ) >>"$LOG" 2>&1
  local env_lines
  env_lines="$(docker inspect "$CONTAINER" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>>"$LOG" | grep -E '^MONETIZATION_(MAINTENANCE|PAYOUTS_ENABLED|WRITES_ENABLED)=')"
  log "container env after restore: $(echo "$env_lines" | tr '\n' ' ')"
  echo "$env_lines" | grep -qx 'MONETIZATION_PAYOUTS_ENABLED=false' || log "!! MONETIZATION_PAYOUTS_ENABLED is NOT false after restore"
  echo "$env_lines" | grep -qx 'MONETIZATION_MAINTENANCE=false'    || log "!! MONETIZATION_MAINTENANCE is NOT false after restore"
  # Wait for the recreated container, then prove the admin route is closed again.
  local i code
  for i in $(seq 1 30); do
    curl -sS -o /dev/null "${MON%/v1/monetization}/healthz" 2>/dev/null && break
    sleep 1
  done
  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$MON/admin/creator-fund/earnings/cb506055-60e1-44ad-afba-e269bcbbc549/reverse" \
     -H 'Content-Type: application/json' -H 'X-Scopes: admin' -H "X-User-Id: $ADMIN_ID" -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
     -d '{"reason":"boundary check after restore"}' 2>>"$LOG" || echo 'curl-failed')"
  if [ "$code" = "503" ]; then
    log "admin route answers 503 again: boundary closed"
  else
    log "!! admin route answered $code after restore, expected 503 — check the container by hand"
  fi
  exit "$rc"
}
trap restore_boundary EXIT

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
# field <file> <json key> -> the raw value (number, true/false or quoted string)
field() {
  grep -oE "\"$2\":(\"[^\"]*\"|-?[0-9]+|true|false|null)" "$1" | head -1 | sed -E "s/^\"$2\"://"
}
# earning_status <file> -> the "status" inside the "earning" object (first status after "earning":{)
earning_status() {
  grep -oE '"earning":\{[^}]*' "$1" | grep -oE '"status":"[a-z_]+"' | head -1 | sed -E 's/^"status":"//; s/"$//'
}

# ---------------------------------------------------------------------------
# Preconditions: right image, maintenance mode on, zero workers, key enforced.
# ---------------------------------------------------------------------------
log "start (resume=$RESUME) operator=$ADMIN_ID target=$MON"
running_image="$(docker inspect "$CONTAINER" --format '{{.Image}}')"
if [ "$running_image" != "$EXPECTED_IMAGE_ID" ]; then
  log "!! running image $running_image is not the approved $EXPECTED_IMAGE_ID; stop"
  exit 3
fi
log "image ok: $running_image"

# Put the container into maintenance mode: ALWAYS a recreate (--force-recreate).
# 11 Sep 2026, first dev run: the container was already in maintenance mode
# from runbook step 0.2, `docker compose up -d` left it running, no fresh boot
# line existed inside `docker logs --since 2m`, and pipefail ended the run
# before the key check (nothing sent). Forcing the recreate makes the boot
# line unconditional; the log window is now the container's own StartedAt.
( cd "$COMPOSE_DIR" && MONETIZATION_MAINTENANCE=true MONETIZATION_WRITES_ENABLED=false MONETIZATION_PAYOUTS_ENABLED=false \
    docker compose up -d --no-deps --force-recreate monetization-service ) >>"$LOG" 2>&1
for i in $(seq 1 30); do curl -sS -o /dev/null "${MON%/v1/monetization}/healthz" 2>/dev/null && break; sleep 1; done
running_image="$(docker inspect "$CONTAINER" --format '{{.Image}}')"
[ "$running_image" = "$EXPECTED_IMAGE_ID" ] || { log "!! image changed across the recreate: $running_image"; exit 3; }
started_at="$(docker inspect "$CONTAINER" --format '{{.State.StartedAt}}')"
boot="$(docker logs --since "$started_at" "$CONTAINER" 2>&1 | grep -E 'monetization run mode:' | tail -1)"
log "boot line: $boot"
echo "$boot" | grep -q 'maintenance=true '        || { log "!! boot line does not say maintenance=true"; exit 3; }
echo "$boot" | grep -q 'payouts_enabled=false '   || { log "!! boot line does not say payouts_enabled=false"; exit 3; }
echo "$boot" | grep -q 'workers=false '           || { log "!! boot line does not say workers=false"; exit 3; }
echo "$boot" | grep -q 'kafka_producer=false '    || { log "!! boot line does not say kafka_producer=false"; exit 3; }
if docker logs --since "$started_at" "$CONTAINER" 2>&1 | grep -qE 'starting monetization background workers|financial mutation workers enabled|worker started'; then
  log "!! a worker start line appeared in the boot log; stop"; exit 3
fi
log "zero workers started (no 'starting monetization background workers' line)"

# The key is enforced: scope without key must be 401; key without scope 403.
code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$MON/admin/creator-fund/earnings/cb506055-60e1-44ad-afba-e269bcbbc549/reverse" \
   -H 'Content-Type: application/json' -H 'X-Scopes: admin' -H "X-User-Id: $ADMIN_ID" -d '{"reason":"key check"}')"
[ "$code" = "401" ] || { log "!! admin route without key answered $code, expected 401"; exit 3; }
code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$MON/admin/creator-fund/earnings/cb506055-60e1-44ad-afba-e269bcbbc549/reverse" \
   -H 'Content-Type: application/json' -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: $ADMIN_ID" -d '{"reason":"scope check"}')"
[ "$code" = "403" ] || { log "!! admin route with key but no scope answered $code, expected 403"; exit 3; }
log "key enforced (401 without key, 403 without scope); no reversal has been sent yet"

# ---------------------------------------------------------------------------
# The table must be internally consistent before the first call.
# ---------------------------------------------------------------------------
rows=0; net_total=0; fee_total=0
while IFS='|' read -r id step net fee absent; do
  [ -z "$id" ] && continue
  rows=$((rows+1)); net_total=$((net_total+net)); fee_total=$((fee_total+fee))
done <<<"$EXPECT"
[ "$rows" = "$EXPECT_ROWS" ] || { log "!! table has $rows rows, expected $EXPECT_ROWS"; exit 4; }
[ "$net_total" = "$EXPECT_NET_TOTAL" ] || { log "!! table net total $net_total, expected $EXPECT_NET_TOTAL"; exit 4; }
[ "$fee_total" = "$EXPECT_FEE_TOTAL" ] || { log "!! table fee total $fee_total, expected $EXPECT_FEE_TOTAL"; exit 4; }
log "table ok: $rows rows, net $net_total, fee $fee_total"

# ---------------------------------------------------------------------------
# The calls, in table order (step 3, then 4a, then 4b).
# ---------------------------------------------------------------------------
done_net=0; done_fee=0; done_rows=0; skipped=0
while IFS='|' read -r id step net fee absent; do
  [ -z "$id" ] && continue
  out="$EVID/exec/$id.json"
  if [ "$RESUME" = "1" ] && [ -s "$out" ] && [ "$(earning_status "$out")" = "reversed" ]; then
    log "step $step $id: already reversed in $out (resume) — skipped"
    skipped=$((skipped+1)); done_rows=$((done_rows+1))
    done_net=$((done_net + $(field "$out" net_reversed_paise))); done_fee=$((done_fee + $(field "$out" fee_reversed_paise)))
    continue
  fi
  case "$step" in
    3)  reason="$REASON_3" ;;
    4a) reason="$REASON_4A" ;;
    4b) reason="$REASON_4B" ;;
    *)  log "!! unknown step $step"; exit 4 ;;
  esac
  log "step $step $id: POST reverse (expect net=$net fee=$fee fee_leg_absent=$absent)"
  code="$(curl -sS -o "$out" -w '%{http_code}' -X POST "$MON/admin/creator-fund/earnings/$id/reverse" \
     -H 'Content-Type: application/json' -H 'X-Scopes: admin' -H "X-User-Id: $ADMIN_ID" -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
     -d "{\"reason\":\"$reason\"}")"
  printf '\n' >>"$out"
  if [ "$code" != "200" ]; then
    log "!! step $step $id: HTTP $code (body in $out); stopping"
    exit 5
  fi
  got_status="$(earning_status "$out")"
  got_already="$(field "$out" already_reversed)"
  got_net="$(field "$out" net_reversed_paise)"
  got_fee="$(field "$out" fee_reversed_paise)"
  got_absent="$(field "$out" fee_leg_absent)"
  got_frozen="$(field "$out" ledger_frozen)"
  got_balance="$(field "$out" balance_after_paise)"
  if [ "$got_already" = "true" ]; then
    # Only acceptable when resuming: the first pass reversed it and the file was lost.
    if [ "$RESUME" = "1" ]; then
      log "step $step $id: already_reversed=true on resume; recorded, counted as done with the table's expected amounts"
      got_net="$net"; got_fee="$fee"; got_absent="$absent"
    else
      log "!! step $step $id: already_reversed=true on a fresh run — the row was reversed outside this script; stopping"
      exit 5
    fi
  fi
  if [ "$got_status" != "reversed" ] || [ "$got_net" != "$net" ] || [ "$got_fee" != "$fee" ] || [ "$got_absent" != "$absent" ] || [ "$got_frozen" != "false" ] || [ "$got_balance" != "0" ]; then
    log "!! step $step $id: MISMATCH status=$got_status net=$got_net fee=$got_fee fee_leg_absent=$got_absent ledger_frozen=$got_frozen balance_after=$got_balance (expected reversed/$net/$fee/$absent/false/0); stopping"
    exit 5
  fi
  log "step $step $id: ok net=$got_net fee=$got_fee fee_leg_absent=$got_absent balance_after=0 frozen=false"
  done_rows=$((done_rows+1)); done_net=$((done_net+net)); done_fee=$((done_fee+fee))
done <<<"$EXPECT"

log "complete: rows=$done_rows (skipped on resume: $skipped) net_reversed=$done_net fee_reversed=$done_fee"
[ "$done_rows" = "$EXPECT_ROWS" ] && [ "$done_net" = "$EXPECT_NET_TOTAL" ] && [ "$done_fee" = "$EXPECT_FEE_TOTAL" ] \
  || { log "!! totals do not match the accepted arithmetic"; exit 6; }
sha256sum "$EVID"/exec/*.json | tee -a "$LOG" >/dev/null
log "responses hashed into $LOG; continue with runbook step 5 (SQL). The trap now closes the boundary."
exit 0
