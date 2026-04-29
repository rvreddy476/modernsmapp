#!/usr/bin/env bash
# Run the atpost integration suite.
#
# Assumes infra (Postgres, Redis, Kafka) is already up via:
#   docker compose -f Architecture/docker/docker-compose.infra.yml up -d
# and the relevant services are running locally via:
#   Architecture/docker/run-local.sh post-service monetization-service user-service graph-service api-gateway
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKSPACE="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$WORKSPACE"

export ATPOST_RUN_INTEGRATION=1

# Health-probe every URL once before launching the test runner so a
# missing service shows up as a clear "infra not ready" message
# rather than a vague test failure deep in the logs.
for var in ATPOST_POST_URL ATPOST_MONETIZATION_URL ATPOST_USER_URL ATPOST_GRAPH_URL ATPOST_API_GATEWAY_URL; do
  url="${!var:-}"
  case $var in
    ATPOST_POST_URL) url="${url:-http://localhost:8084}" ;;
    ATPOST_MONETIZATION_URL) url="${url:-http://localhost:8099}" ;;
    ATPOST_USER_URL) url="${url:-http://localhost:8082}" ;;
    ATPOST_GRAPH_URL) url="${url:-http://localhost:8083}" ;;
    ATPOST_API_GATEWAY_URL) url="${url:-http://localhost:8080}" ;;
  esac
  if curl --silent --fail --max-time 2 "$url/health" > /dev/null 2>&1; then
    printf '  ✓ %-30s %s\n' "$var" "$url"
  else
    printf '  ✗ %-30s %s  (not reachable — tests will skip)\n' "$var" "$url"
  fi
done

echo
echo "Running integration tests..."
echo

go test -tags integration -v -count=1 ./tools/integration "$@"
