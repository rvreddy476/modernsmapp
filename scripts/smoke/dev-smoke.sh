#!/usr/bin/env bash
# Post-deploy dev smoke test. See docs/harness/DEV-SMOKE.md.
#   bash scripts/smoke/dev-smoke.sh [--api <url>] [--only a,b] [--skip a,b] [--keep]
set -euo pipefail
exec node "$(dirname "$0")/dev-smoke.mjs" "$@"
