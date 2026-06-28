#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
# stack.sh — the ONE blessed way to run the VChat backend (atpost_stack).
#
# Why this exists: the environment kept being inconsistent because there was no
# single, deterministic startup. This script enforces the invariants every time:
#   1. Stops the duplicate sub-stacks that steal ports (chat-service, identity-platform)
#   2. Brings atpost_stack up
#   3. Applies restart=unless-stopped so a host reboot self-heals
#   4. Waits for the critical services to be healthy
#   5. Smoke-tests the login path so you KNOW it works before you start
#
# Usage:
#   ./stack.sh up       # start everything (default)
#   ./stack.sh down     # stop atpost_stack (keeps data volumes)
#   ./stack.sh smoke    # just run the health/login smoke check
#   ./stack.sh status   # show service status
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

cd "$(dirname "$0")"

PROJECT="atpost_stack"
GW="http://localhost:8080"
DUPLICATES=("chat-service" "identity-platform")

green() { printf '\033[32m%s\033[0m\n' "$1"; }
yellow() { printf '\033[33m%s\033[0m\n' "$1"; }
red() { printf '\033[31m%s\033[0m\n' "$1"; }

stop_duplicates() {
  for proj in "${DUPLICATES[@]}"; do
    ids=$(docker ps -q --filter "label=com.docker.compose.project=$proj" 2>/dev/null || true)
    if [ -n "$ids" ]; then
      yellow "Stopping duplicate stack '$proj' (it steals atpost_stack's ports)…"
      docker update --restart=no $ids >/dev/null 2>&1 || true
      docker stop $ids >/dev/null 2>&1 || true
    fi
  done
}

apply_restart_policy() {
  ids=$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT" 2>/dev/null || true)
  [ -n "$ids" ] && docker update --restart=unless-stopped $ids >/dev/null 2>&1 || true
}

wait_healthy() {
  local name="$1" tries="${2:-40}"
  printf 'waiting for %s' "$name"
  for _ in $(seq 1 "$tries"); do
    local h
    h=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$name" 2>/dev/null || echo "missing")
    if [ "$h" = "healthy" ] || [ "$h" = "running" ]; then printf ' ✓ (%s)\n' "$h"; return 0; fi
    printf '.'; sleep 5
  done
  printf ' ✗ (still not ready)\n'; return 1
}

smoke() {
  echo "── Smoke check ─────────────────────────────"
  local ts email reg login
  ts=$(date +%s); email="smoke_${ts}@example.com"
  reg=$(curl -s -o /dev/null -m 10 -w "%{http_code}" -X POST "$GW/v1/auth/register" -H "Content-Type: application/json" \
    -d "{\"first_name\":\"S\",\"last_name\":\"K\",\"gender\":\"male\",\"dob\":\"1991-05-15\",\"email\":\"$email\",\"password\":\"TestPass123!\"}" 2>/dev/null || echo 000)
  login=$(curl -s -o /dev/null -m 10 -w "%{http_code}" -X POST "$GW/v1/auth/login" -H "Content-Type: application/json" \
    -d "{\"identifier\":\"$email\",\"password\":\"TestPass123!\",\"device_id\":\"smoke\",\"platform\":\"web\"}" 2>/dev/null || echo 000)
  echo "  register -> $reg"
  echo "  login    -> $login"
  if [ "$reg" = "201" ] && [ "$login" = "200" ]; then
    green "✅ Backend healthy — login works."
    return 0
  fi
  red "❌ Smoke check FAILED. Run './stack.sh status' and check 'docker logs atpost_stack-api-gateway-1'."
  return 1
}

cmd="${1:-up}"
case "$cmd" in
  up)
    stop_duplicates
    yellow "Starting $PROJECT…"
    docker compose up -d
    wait_healthy "${PROJECT}-postgres-1" || true
    wait_healthy "${PROJECT}-scylla-1" || true
    wait_healthy "${PROJECT}-api-gateway-1" || true
    apply_restart_policy
    green "restart=unless-stopped applied (survives reboot)."
    smoke || exit 1
    ;;
  down)
    yellow "Stopping $PROJECT (data volumes kept)…"
    docker compose stop
    green "Stopped."
    ;;
  smoke)
    smoke || exit 1
    ;;
  status)
    docker compose ps
    ;;
  *)
    echo "Usage: ./stack.sh [up|down|smoke|status]"; exit 2
    ;;
esac
