#!/usr/bin/env bash
# Copies the canonical playback fixtures from the web repository into this
# directory. Run from anywhere; expects the web checkout beside this one.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
src="${ATPOST_WEB_DIR:-$here/../../../../../../atpost-web}/packages/analytics/fixtures/playback"
if [ ! -d "$src" ]; then
  echo "web fixtures not found at $src (set ATPOST_WEB_DIR)" >&2
  exit 1
fi
changed=0
for f in "$src"/*.json; do
  name="$(basename "$f")"
  if ! cmp -s "$f" "$here/$name"; then
    cp "$f" "$here/$name"
    echo "updated $name"
    changed=1
  fi
done
for f in "$here"/*.json; do
  name="$(basename "$f")"
  [ -f "$src/$name" ] || { rm "$f"; echo "removed $name (gone from the web repo)"; changed=1; }
done
[ "$changed" = 1 ] || echo "fixtures already in sync"
