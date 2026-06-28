#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
# docker-bringup.sh  —  AtPost / VChat full-stack Docker bring-up
#
# Run once per machine setup, or any time you need to rebuild from scratch.
# Handles IPv6 instability (common on Jio/Airtel ISPs), per-image retry pulls,
# sequential service builds, and health-check verification.
#
# Usage:
#   cd /path/to/modernsmapp
#   bash scripts/docker-bringup.sh [--skip-daemon-fix] [--infra-only] [--help]
#
# Options:
#   --skip-daemon-fix   Skip rewriting /etc/docker/daemon.json (already done)
#   --infra-only        Start only the 9 infra containers; no service builds
#   --rebuild           Force rebuild of all Go service images (slow)
#   --help              Show this message
#
# Prerequisites:
#   - User in the docker group:  sudo usermod -aG docker $USER && newgrp docker
#   - sudo access (for daemon.json only — skippable with --skip-daemon-fix)
#   - At least 16 GB RAM recommended (ScyllaDB + OpenSearch are hungry)
# ══════════════════════════════════════════════════════════════════════════════
set -euo pipefail

# ── Colours ─────────────────────────────────────────────────────────────────
RED='\033[0;31m'; YELLOW='\033[1;33m'; GREEN='\033[0;32m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

log()  { echo -e "${CYAN}[bringup]${RESET} $*"; }
ok()   { echo -e "${GREEN}[  OK  ]${RESET} $*"; }
warn() { echo -e "${YELLOW}[ WARN ]${RESET} $*"; }
fail() { echo -e "${RED}[ FAIL ]${RESET} $*" >&2; }
die()  { fail "$*"; exit 1; }

# ── Paths ────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_DIR="$REPO_ROOT/Architecture/docker"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
INFRA_COMPOSE_FILE="$COMPOSE_DIR/docker-compose.infra.yml"
ENV_FILE="$COMPOSE_DIR/local.env"

# ── Arg parsing ──────────────────────────────────────────────────────────────
SKIP_DAEMON_FIX=0
INFRA_ONLY=0
REBUILD=0
for arg in "$@"; do
  case "$arg" in
    --skip-daemon-fix) SKIP_DAEMON_FIX=1 ;;
    --infra-only)      INFRA_ONLY=1 ;;
    --rebuild)         REBUILD=1 ;;
    --help|-h)
      sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'
      exit 0 ;;
    *) die "Unknown argument: $arg  (try --help)" ;;
  esac
done

# ── Sanity checks ────────────────────────────────────────────────────────────
[[ -f "$COMPOSE_FILE" ]]       || die "Compose file not found: $COMPOSE_FILE"
[[ -f "$ENV_FILE" ]]           || die "Env file not found: $ENV_FILE"
command -v docker &>/dev/null  || die "'docker' not in PATH"
docker info &>/dev/null        || die "Docker socket not accessible. Run: sudo usermod -aG docker \$USER && newgrp docker"

# ── Step 1: Fix Docker daemon for IPv4 stability ─────────────────────────────
# Problem: IPv6 routes from Indian ISPs (Jio/Airtel) to US CloudFront
# (registry.docker.io / production.cloudfront.docker.com) are unreliable and
# reset mid-download.  Disabling ip6tables and pinning DNS to Google forces
# Docker to use IPv4 when available, which is far more stable.
fix_docker_daemon() {
  log "Fixing Docker daemon (force IPv4 + stable DNS)..."
  local daemon_json
  daemon_json=$(cat <<'JSON'
{
  "dns": ["8.8.8.8", "1.1.1.1"],
  "ip6tables": false
}
JSON
)
  if [[ -f /etc/docker/daemon.json ]]; then
    local current
    current=$(cat /etc/docker/daemon.json)
    if [[ "$current" == *'"ip6tables": false'* ]]; then
      ok "daemon.json already patched — skipping restart"
      return
    fi
    warn "Overwriting existing /etc/docker/daemon.json"
  fi

  echo "$daemon_json" | sudo tee /etc/docker/daemon.json > /dev/null
  ok "Written /etc/docker/daemon.json"

  log "Restarting Docker daemon..."
  sudo systemctl restart docker
  sleep 4  # give dockerd time to settle
  docker info &>/dev/null || die "Docker failed to start after daemon.json change"
  ok "Docker daemon restarted"
}

if [[ $SKIP_DAEMON_FIX -eq 0 ]]; then
  fix_docker_daemon
else
  warn "--skip-daemon-fix set; assuming daemon.json already correct"
fi

# ── Step 2: Pull infra images with retry ─────────────────────────────────────
# Each image is pulled individually so one failure doesn't abort the rest.
# Exponential backoff: 10s → 20s → 40s → 80s between retries.
INFRA_IMAGES=(
  "postgis/postgis:16-3.4"
  "redis:7"
  "scylladb/scylla:5.4"
  "jaegertracing/all-in-one:latest"
  "redpandadata/redpanda:latest"
  "redpandadata/console:latest"
  "minio/minio:latest"
  "opensearchproject/opensearch:2"
  "bluenviron/mediamtx:latest"
)

pull_with_retry() {
  local image="$1"
  local max_attempts=5
  local delay=10

  for attempt in $(seq 1 $max_attempts); do
    log "Pulling ${BOLD}$image${RESET} (attempt $attempt/$max_attempts)..."
    if docker pull "$image" 2>&1; then
      ok "Pulled $image"
      return 0
    fi
    if [[ $attempt -lt $max_attempts ]]; then
      warn "Pull failed. Retrying in ${delay}s..."
      sleep "$delay"
      delay=$((delay * 2))
    fi
  done

  fail "FAILED to pull $image after $max_attempts attempts"
  return 1
}

log "Pulling ${#INFRA_IMAGES[@]} infrastructure images..."
PULL_FAILURES=()
for img in "${INFRA_IMAGES[@]}"; do
  if ! pull_with_retry "$img"; then
    PULL_FAILURES+=("$img")
  fi
done

if [[ ${#PULL_FAILURES[@]} -gt 0 ]]; then
  fail "The following images could not be pulled:"
  for f in "${PULL_FAILURES[@]}"; do fail "  • $f"; done
  die "Fix network connectivity and rerun.  Add --skip-daemon-fix if daemon is already patched."
fi
ok "All infra images pulled"

# ── Step 3: Start infrastructure services ────────────────────────────────────
log "Starting infrastructure services (docker-compose.infra.yml)..."
docker compose \
  -f "$INFRA_COMPOSE_FILE" \
  --env-file "$ENV_FILE" \
  up -d --remove-orphans

# ── Step 4: Wait for infrastructure health ───────────────────────────────────
# Uses `docker compose ps` so we never have to guess container names.
# Falls back to polling HTTP for services without a healthcheck.

_compose_service_health() {
  # Returns the health status of a service in the infra compose project.
  # Outputs: healthy | unhealthy | starting | running | exited | unknown
  local service="$1"
  docker compose \
    -f "$INFRA_COMPOSE_FILE" \
    --env-file "$ENV_FILE" \
    ps --format '{{.Health}}' "$service" 2>/dev/null | head -1 | tr -d '[:space:]' || echo "unknown"
}

wait_healthy() {
  local service="$1"
  local max_wait="${2:-120}"
  local interval=5
  local elapsed=0

  log "Waiting for $service to be healthy (up to ${max_wait}s)..."
  while [[ $elapsed -lt $max_wait ]]; do
    local status
    status=$(_compose_service_health "$service")
    if [[ "$status" == "healthy" ]]; then
      ok "$service is healthy"
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done

  warn "$service did not become healthy within ${max_wait}s (status: ${status:-unknown})"
  return 1  # non-fatal; continue
}

poll_http() {
  local name="$1"
  local url="$2"
  local max_wait="${3:-90}"
  local elapsed=0
  log "Polling $name at $url ..."
  while [[ $elapsed -lt $max_wait ]]; do
    if curl -sf --max-time 3 "$url" &>/dev/null; then
      ok "$name is up"
      return 0
    fi
    sleep 5; elapsed=$((elapsed + 5))
  done
  warn "$name not responding at $url after ${max_wait}s — proceeding anyway"
}

wait_healthy "postgres"  60
wait_healthy "redis"     30
wait_healthy "scylla"    240  # ScyllaDB is slow to start

# redpanda has no healthcheck in compose — poll the Kafka port instead
log "Waiting for redpanda port 9092..."
_elapsed=0
while [[ $_elapsed -lt 90 ]]; do
  if nc -z localhost 9092 2>/dev/null; then
    ok "redpanda is accepting connections on :9092"
    break
  fi
  sleep 5; _elapsed=$((_elapsed + 5))
done
if [[ $_elapsed -ge 90 ]]; then
  warn "redpanda not reachable on :9092 after 90s — proceeding anyway"
fi

poll_http "OpenSearch" "http://localhost:9200/_cluster/health" 120
poll_http "MinIO"      "http://localhost:9000/minio/health/live" 60

ok "Infrastructure is up"

if [[ $INFRA_ONLY -eq 1 ]]; then
  log "--infra-only set; stopping before service builds."
  echo -e "\n${GREEN}${BOLD}Infrastructure ready.${RESET}"
  echo "Run Go services locally: bash Architecture/docker/run-local.sh all-core"
  exit 0
fi

# ── Step 5: Build Go services one at a time ───────────────────────────────────
# Building all services in parallel causes intermittent EOF errors (Docker
# daemon can't sustain that many concurrent layer downloads on a single machine).
# Known-broken services due to port 8117 collision are skipped entirely.
#
#   feature-flag-service  — port 8117 collides with ai-service
#   live-service-v2       — also bound to 8117; use live-service instead
#
SKIP_SERVICES=("feature-flag-service" "live-service-v2")

ALL_BUILD_SERVICES=(
  api-gateway
  identity-auth
  identity-user
  identity-profile
  user-service
  graph-service
  group-service
  post-service
  feed-service
  media-service
  media-worker
  notification-service
  search-service
  trust-safety-service
  chat-message-service
  chat-ws-gateway
  call-service
  monetization-service
  analytics-service
  ai-service
  admin-service
  payments-service
  suggestion-service
  commerce-service
  food-service
  wallet-service
  bill-pay-service
  rider-service
  live-service
  memories-service
  channel-service
  community-service
  qa-service
  dating-service
)

is_skipped() {
  local svc="$1"
  for s in "${SKIP_SERVICES[@]}"; do [[ "$s" == "$svc" ]] && return 0; done
  return 1
}

BUILD_FAILURES=()
BUILD_FLAGS=()
[[ $REBUILD -eq 1 ]] && BUILD_FLAGS=("--no-cache")

total=${#ALL_BUILD_SERVICES[@]}
idx=0
for svc in "${ALL_BUILD_SERVICES[@]}"; do
  idx=$((idx + 1))
  if is_skipped "$svc"; then
    warn "[$idx/$total] Skipping $svc (port 8117 collision)"
    continue
  fi

  log "[$idx/$total] Building $svc ..."
  if ! docker compose \
      -f "$COMPOSE_FILE" \
      --env-file "$ENV_FILE" \
      build "${BUILD_FLAGS[@]}" "$svc" 2>&1; then
    fail "Build failed: $svc"
    BUILD_FAILURES+=("$svc")
  else
    ok "Built $svc"
  fi
done

if [[ ${#BUILD_FAILURES[@]} -gt 0 ]]; then
  warn "The following services failed to build (they will be absent at runtime):"
  for f in "${BUILD_FAILURES[@]}"; do warn "  • $f"; done
fi

# ── Step 6: Start all services ───────────────────────────────────────────────
# Build list of --scale svc=0 flags for skipped services so compose doesn't
# try to start them.
SCALE_FLAGS=()
for svc in "${SKIP_SERVICES[@]}"; do
  SCALE_FLAGS+=("--scale" "${svc}=0")
done

log "Starting all services..."
docker compose \
  -f "$COMPOSE_FILE" \
  --env-file "$ENV_FILE" \
  up -d --remove-orphans \
  "${SCALE_FLAGS[@]}"

# ── Step 7: Health verification ──────────────────────────────────────────────
log "Verifying stack health..."
sleep 8  # let containers settle

check_endpoint() {
  local name="$1"
  local url="$2"
  local max_wait="${3:-60}"
  local elapsed=0
  while [[ $elapsed -lt $max_wait ]]; do
    local code
    code=$(curl -sf -o /dev/null -w '%{http_code}' --max-time 4 "$url" 2>/dev/null || echo "000")
    if [[ "$code" =~ ^2 ]]; then
      ok "$name → $url  [HTTP $code]"
      return 0
    fi
    sleep 5; elapsed=$((elapsed + 5))
  done
  warn "$name not healthy at $url after ${max_wait}s  (last code: $code)"
}

check_endpoint "api-gateway"         "http://localhost:8080/health"  90
check_endpoint "identity-auth"       "http://localhost:8081/health"  60
check_endpoint "user-service"        "http://localhost:8082/health"  60
check_endpoint "group-service"       "http://localhost:8090/health"  60
check_endpoint "chat-ws-gateway"     "http://localhost:8093/health"  60
check_endpoint "redpanda-console"    "http://localhost:8085"         60
check_endpoint "minio-console"       "http://localhost:9001"         30
check_endpoint "jaeger-ui"           "http://localhost:16686"        30

# ── Summary ──────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}${BOLD}╔══════════════════════════════════════════════════╗${RESET}"
echo -e "${GREEN}${BOLD}║          AtPost / VChat stack is up!             ║${RESET}"
echo -e "${GREEN}${BOLD}╚══════════════════════════════════════════════════╝${RESET}"
echo ""
echo -e "  ${BOLD}API Gateway${RESET}        http://localhost:8080"
echo -e "  ${BOLD}Redpanda Console${RESET}   http://localhost:8085"
echo -e "  ${BOLD}MinIO Console${RESET}      http://localhost:9001  (admin/password123)"
echo -e "  ${BOLD}Jaeger UI${RESET}          http://localhost:16686"
echo -e "  ${BOLD}OpenSearch${RESET}         http://localhost:9200"
echo ""

if [[ ${#BUILD_FAILURES[@]} -gt 0 ]]; then
  echo -e "${YELLOW}${BOLD}Services that failed to build:${RESET}"
  for f in "${BUILD_FAILURES[@]}"; do echo -e "  ${YELLOW}• $f${RESET}"; done
  echo ""
fi

echo -e "${YELLOW}${BOLD}Skipped services (port 8117 collision):${RESET}"
for s in "${SKIP_SERVICES[@]}"; do echo -e "  ${YELLOW}• $s${RESET}"; done
echo ""
echo "To run the Next.js web UI:"
echo "  cd postbook-ui && npm install && npm run dev"
echo ""
echo "To stop everything:"
echo "  docker compose -f Architecture/docker/docker-compose.yml down"
echo "  docker compose -f Architecture/docker/docker-compose.infra.yml down"
