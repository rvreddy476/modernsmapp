#!/usr/bin/env bash
set -euo pipefail

# =============================================================================
# Setup all databases for AtPost platform
# Run this AFTER docker-compose.infra.yml is up and Postgres is healthy.
#
# Usage:
#   ./setup-all.sh                    # uses default container name
#   ./setup-all.sh my-postgres-1      # custom container name
# =============================================================================

CONTAINER="${1:-atpost_infra-postgres-1}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Setting up AtPost databases ==="
echo "Container: $CONTAINER"
echo ""

# Check container is running
if ! docker exec "$CONTAINER" pg_isready -U postgres > /dev/null 2>&1; then
    echo "ERROR: Postgres container '$CONTAINER' is not ready."
    echo "Make sure infrastructure is running: docker compose -f docker-compose.infra.yml up -d"
    exit 1
fi

# Create databases if they don't exist
echo "[1/4] Creating databases..."
docker exec "$CONTAINER" psql -U postgres -c "SELECT 'CREATE DATABASE identity_db' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='identity_db')\gexec" 2>/dev/null || true
docker exec "$CONTAINER" psql -U postgres -c "SELECT 'CREATE DATABASE chat_db' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname='chat_db')\gexec" 2>/dev/null || true
echo "  Databases: app, identity_db, chat_db"

# Run identity_db schema
echo "[2/4] Setting up identity_db..."
docker exec -i "$CONTAINER" psql -U postgres -f- < "$SCRIPT_DIR/01-identity-db.sql"
echo "  identity_db ready."

# Run app db schema
echo "[3/4] Setting up app db..."
docker exec -i "$CONTAINER" psql -U postgres -f- < "$SCRIPT_DIR/02-app-db.sql"
echo "  app db ready."

# Run chat_db schema
echo "[4/4] Setting up chat_db..."
docker exec -i "$CONTAINER" psql -U postgres -f- < "$SCRIPT_DIR/03-chat-db.sql"
echo "  chat_db ready."

echo ""
echo "=== All databases set up successfully ==="
echo ""
echo "Test users (all passwords: password123):"
echo "  user1@example.com  (johndoe)"
echo "  user2@example.com  (janedoe)"
echo "  user3@example.com  (bobsmith)"
