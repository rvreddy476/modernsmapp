#!/bin/sh
# Runs the message-service Postgres integration suite against a DISPOSABLE
# scratch database (re-verification P0-5).
#
# WHY THIS SCRIPT EXISTS: the integration package contains a pre-existing
# test that executes `DROP SCHEMA chat CASCADE` before bootstrapping. Pointed
# at a DSN holding data anyone cares about, a test run erases it — that
# happened once against the local dev stack. This script is the only
# sanctioned way to run the suite: it creates a fresh postgres:16-alpine
# container on a non-default port, runs the tests inside it, and destroys it
# afterwards no matter what. NEVER export CHAT_POSTGRES_DSN pointing at a
# shared database.
#
# Usage (from chat-service/services/message-service):
#   sh scripts/run-pg-integration-scratch.sh [extra go test args...]
set -eu

CONTAINER="chatpg-scratch-$$"
PORT="${SCRATCH_PG_PORT:-55432}"

docker run -d --rm --name "$CONTAINER" \
  -e POSTGRES_PASSWORD=scratch -e POSTGRES_DB=chat_scratch \
  -p "127.0.0.1:${PORT}:5432" postgres:16-alpine > /dev/null

cleanup() { docker rm -f "$CONTAINER" > /dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

i=0
until docker exec "$CONTAINER" pg_isready -U postgres -d chat_scratch > /dev/null 2>&1; do
  i=$((i+1))
  if [ "$i" -gt 60 ]; then echo "scratch postgres did not come up" >&2; exit 1; fi
  sleep 1
done

export CHAT_POSTGRES_DSN="postgres://postgres:scratch@127.0.0.1:${PORT}/chat_scratch?sslmode=disable"
echo "scratch DSN: $CHAT_POSTGRES_DSN"
go test ./internal/store/postgres -count=1 -tags integration -v "$@"
