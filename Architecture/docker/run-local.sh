#!/usr/bin/env bash
set -uo pipefail

# ── run-local.sh ─────────────────────────────────────────────
# Run one or more Go services locally (outside Docker).
# Infrastructure (Postgres, Redis, etc.) must be running via:
#   docker compose -f docker-compose.infra.yml up -d
#
# Usage:
#   ./run-local.sh <service> [service...]
#   ./run-local.sh api-gateway user-service post-service
#   ./run-local.sh all-core          # api-gw + identity + user + post + feed + media
#   ./run-local.sh list              # show available services
# ─────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Source local env (infra on localhost)
source "$SCRIPT_DIR/local.env"

# ── Service registry: name → module dir + port ──
declare -A SERVICE_DIR SERVICE_PORT SERVICE_DSN

# Architecture services
SERVICE_DIR[api-gateway]="Architecture/services/api-gateway"
SERVICE_PORT[api-gateway]=8080

SERVICE_DIR[user-service]="Architecture/services/user-service"
SERVICE_PORT[user-service]=8082

SERVICE_DIR[graph-service]="Architecture/services/graph-service"
SERVICE_PORT[graph-service]=8083

SERVICE_DIR[post-service]="Architecture/services/post-service"
SERVICE_PORT[post-service]=8084

SERVICE_DIR[feed-service]="Architecture/services/feed-service"
SERVICE_PORT[feed-service]=8086

SERVICE_DIR[media-service]="Architecture/services/media-service"
SERVICE_PORT[media-service]=8087

SERVICE_DIR[notification-service]="Architecture/services/notification-service"
SERVICE_PORT[notification-service]=8088

SERVICE_DIR[search-service]="Architecture/services/search-service"
SERVICE_PORT[search-service]=8089

SERVICE_DIR[group-service]="Architecture/services/group-service"
SERVICE_PORT[group-service]=8090

SERVICE_DIR[trust-safety-service]="Architecture/services/trust-safety-service"
SERVICE_PORT[trust-safety-service]=8091

SERVICE_DIR[reviewer-service]="Architecture/services/reviewer-service"
SERVICE_PORT[reviewer-service]=8120

SERVICE_DIR[monetization-service]="Architecture/services/monetization-service"
SERVICE_PORT[monetization-service]=8099

SERVICE_DIR[analytics-service]="Architecture/services/analytics-service"
SERVICE_PORT[analytics-service]=8094

SERVICE_DIR[feature-flag-service]="Architecture/services/feature-flag-service"
SERVICE_PORT[feature-flag-service]=8095

SERVICE_DIR[admin-service]="Architecture/services/admin-service"
SERVICE_PORT[admin-service]=8096

SERVICE_DIR[suggestion-service]="Architecture/services/suggestion-service"
SERVICE_PORT[suggestion-service]=8100

# Phase F1.4 — shop-service retired; /v1/commerce/* in commerce-service.

SERVICE_DIR[live-service]="Architecture/services/live-service"
SERVICE_PORT[live-service]=8103

SERVICE_DIR[memories-service]="Architecture/services/memories-service"
SERVICE_PORT[memories-service]=8104

SERVICE_DIR[channel-service]="Architecture/services/channel-service"
SERVICE_PORT[channel-service]=8106

SERVICE_DIR[community-service]="Architecture/services/community-service"
SERVICE_PORT[community-service]=8107

SERVICE_DIR[qa-service]="Architecture/services/qa-service"
SERVICE_PORT[qa-service]=8108

SERVICE_DIR[commerce-service]="Architecture/services/commerce-service"
SERVICE_PORT[commerce-service]=8109
SERVICE_DSN[commerce-service]="$COMMERCE_POSTGRES_DSN"

# Commerce services (v2.1)
# Phase F1.4 — orders-service retired; /v1/commerce/orders in commerce-service.

SERVICE_DIR[payments-service]="Architecture/services/payments-service"
SERVICE_PORT[payments-service]=8102
SERVICE_DSN[payments-service]="$COMMERCE_POSTGRES_DSN"

# Identity-platform services
SERVICE_DIR[identity-auth]="identity-platform/services/auth-service"
SERVICE_PORT[identity-auth]=8081
SERVICE_DSN[identity-auth]="$IDENTITY_POSTGRES_DSN"

SERVICE_DIR[identity-user]="identity-platform/services/user-service"
SERVICE_PORT[identity-user]=8097
SERVICE_DSN[identity-user]="$IDENTITY_POSTGRES_DSN"

SERVICE_DIR[identity-profile]="identity-platform/services/profile-service"
SERVICE_PORT[identity-profile]=8098
SERVICE_DSN[identity-profile]="$IDENTITY_POSTGRES_DSN"

# Chat services
SERVICE_DIR[chat-message-service]="chat-service/services/message-service"
SERVICE_PORT[chat-message-service]=8092
SERVICE_DSN[chat-message-service]="$CHAT_POSTGRES_DSN"

SERVICE_DIR[chat-ws-gateway]="chat-service/services/ws-gateway"
SERVICE_PORT[chat-ws-gateway]=8093

# ── Preset groups ──
CORE_SERVICES="api-gateway identity-auth identity-profile user-service post-service feed-service media-service"

# ── Commands ──
if [[ "${1:-}" == "list" ]]; then
    echo "Available services:"
    for svc in $(echo "${!SERVICE_DIR[@]}" | tr ' ' '\n' | sort); do
        printf "  %-25s  port %-5s  %s\n" "$svc" "${SERVICE_PORT[$svc]}" "${SERVICE_DIR[$svc]}"
    done
    echo ""
    echo "Presets:"
    echo "  all-core  →  $CORE_SERVICES"
    exit 0
fi

if [[ $# -eq 0 ]]; then
    echo "Usage: $0 <service> [service...]"
    echo "       $0 all-core"
    echo "       $0 list"
    exit 1
fi

# Expand presets
SERVICES=()
for arg in "$@"; do
    case "$arg" in
        all-core) SERVICES+=($CORE_SERVICES) ;;
        *)        SERVICES+=("$arg") ;;
    esac
done

# ── Launch each service in background ──
PIDS=()
cleanup() {
    echo ""
    echo "Stopping all services..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null
    echo "All services stopped."
}
trap cleanup EXIT INT TERM

for svc in "${SERVICES[@]}"; do
    dir="${SERVICE_DIR[$svc]:-}"
    if [[ -z "$dir" ]]; then
        echo "Unknown service: $svc (run '$0 list' to see available services)"
        exit 1
    fi

    port="${SERVICE_PORT[$svc]}"
    dsn="${SERVICE_DSN[$svc]:-$POSTGRES_DSN}"
    full_dir="$REPO_ROOT/$dir"

    if [[ ! -d "$full_dir" ]]; then
        echo "Directory not found: $full_dir — skipping $svc"
        continue
    fi

    echo "Starting $svc on :$port ($(basename "$dir"))..."

    # Determine the Go module root and the relative cmd path.
    # For Architecture services: module root is Architecture/, relative path is services/xxx/cmd/server/main.go
    # For identity-platform/chat-service: module root is identity-platform/ or chat-service/, relative is services/xxx/cmd/server/main.go
    module_root=""
    cmd_path=""
    case "$dir" in
        Architecture/*)
            module_root="$REPO_ROOT/Architecture"
            cmd_path="./${dir#Architecture/}/cmd/server/main.go"
            ;;
        identity-platform/*)
            module_root="$REPO_ROOT/identity-platform"
            cmd_path="./${dir#identity-platform/}/cmd/server/main.go"
            ;;
        chat-service/*)
            module_root="$REPO_ROOT/chat-service"
            cmd_path="./${dir#chat-service/}/cmd/server/main.go"
            ;;
        *)
            module_root="$full_dir"
            cmd_path="./cmd/server/main.go"
            ;;
    esac

    # Override per-service env, run from module root
    case "$svc" in
        chat-*)
            (cd "$module_root" && \
            HTTP_PORT="$port" \
            POSTGRES_DSN="$dsn" \
            ALLOWED_ORIGINS="$CORS_ORIGINS" \
            SCYLLA_KEYSPACE="chatservice" \
            KAFKA_TOPIC="chat.events.v1" \
                go run "$cmd_path" 2>&1 | sed "s/^/[$svc] /") &
            ;;
        *)
            (cd "$module_root" && \
            HTTP_PORT="$port" \
            POSTGRES_DSN="$dsn" \
            ALLOWED_ORIGINS="$CORS_ORIGINS" \
                go run "$cmd_path" 2>&1 | sed "s/^/[$svc] /") &
            ;;
    esac

    PIDS+=($!)
done

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ${#SERVICES[@]} service(s) running. Press Ctrl+C to stop all."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Wait for all background processes; don't exit on individual failures
wait || true
