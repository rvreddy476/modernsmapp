#!/usr/bin/env bash
# bump-service-tags.sh — set the image tags in ONE service values file after a
# build-push run, each key from the image that was actually pushed.
#
#   bump-service-tags.sh SERVICE VALUES_FILE SHA
#
# Rules (copyright-match-plan P-17):
#   image.tag         := SHA  (the server image was pushed at SHA)
#   worker.image.tag  := SHA  when the service has a Dockerfile.worker, because
#                       build-push builds and pushes <name>-worker:SHA in the
#                       same job (media-service); or when worker.image.repository
#                       is the SERVER repository, because then the worker is a
#                       binary inside the server image (dating-service's
#                       data-exporter) and the two tags must stay equal.
#                       Otherwise it is left alone with a warning: writing a SHA
#                       that no image carries is exactly the bug this replaces.
# A values file with no worker block is bumped on image.tag only.
#
# SERVICE may be the source dir name (media-service) or the deploy name used by
# build-push-acr (identity-auth-service, chat-message-service); both resolve.
set -euo pipefail

svc="${1:-}"; file="${2:-}"; sha="${3:-}"
[[ -n "$svc" && -n "$file" && -n "$sha" ]] || { echo "usage: $0 SERVICE VALUES_FILE SHA" >&2; exit 2; }
[[ -f "$file" ]] || { echo "$0: no such file: $file" >&2; exit 2; }

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
vt="$here/values-tag.sh"
# Repo root: scripts/ci/../..
root="$(cd "$here/../.." && pwd)"

# Source dir for the service: the three Go workspaces, by raw name and by the
# deploy-name prefixes the ACR workflow uses.
src=""
for cand in \
  "Architecture/services/$svc" \
  "identity-platform/services/$svc" \
  "identity-platform/services/${svc#identity-}" \
  "chat-service/services/$svc" \
  "chat-service/services/${svc#chat-}"; do
  if [[ -d "$root/$cand" ]]; then src="$root/$cand"; break; fi
done

bash "$vt" set "$file" image.tag "$sha"
echo "$file: image.tag -> $sha"

# No worker block: done. (exit 3 = key not found; anything else is a real error.)
if worker_tag="$(bash "$vt" get "$file" worker.image.tag 2>/dev/null)"; then
  :
else
  rc=$?
  if [[ $rc -eq 3 ]]; then exit 0; fi
  echo "$0: reading worker.image.tag from $file failed (exit $rc)" >&2
  exit "$rc"
fi

if [[ -n "$src" && -f "$src/Dockerfile.worker" ]]; then
  bash "$vt" set "$file" worker.image.tag "$sha"
  echo "$file: worker.image.tag -> $sha (Dockerfile.worker: worker image pushed at this sha)"
  exit 0
fi

server_repo="$(bash "$vt" get "$file" image.repository)"
worker_repo="$(bash "$vt" get "$file" worker.image.repository)"
if [[ "$server_repo" == "$worker_repo" ]]; then
  bash "$vt" set "$file" worker.image.tag "$sha"
  echo "$file: worker.image.tag -> $sha (worker runs from the server image)"
  exit 0
fi

echo "::warning ::$file: worker.image.repository ($worker_repo) is not the server image and $svc has no Dockerfile.worker; leaving worker.image.tag at $worker_tag"
