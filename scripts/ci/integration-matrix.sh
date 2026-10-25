#!/usr/bin/env bash
# Print the integration-all.yml job matrix as one line of JSON.
#
#   scripts/ci/integration-matrix.sh [all|1|2]
#
# One include per manifest module in the chosen wave:
#   module     the module dir
#   wave       1 or 2
#   workspace  the go.work directory (Architecture, chat-service, identity-platform)
#   pg_image   the postgres service image
#   redis      "redis:7" when the entry needs or optionally uses redis, else ""
#              (an empty service image means GitHub starts no container)
#   blocked    the needs this workflow cannot provide yet (kafka, opensearch,
#              scylla, minio), space-separated; the job skips when non-empty

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
source "$HERE/integration-manifest-lib.sh"

WAVE="${1:-all}"
case "$WAVE" in all | 1 | 2) ;; *) echo "wave must be all, 1 or 2" >&2; exit 2 ;; esac

mf_load || exit 2

json_str() { printf '"%s"' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g')"; }

out=""
while IFS= read -r m; do
  [ -n "$m" ] || continue
  i="$(mf_index "$m")"
  w="$(mf_get "$i" wave)"
  [ "$WAVE" = all ] || [ "$WAVE" = "$w" ] || continue
  ws="${m%%/*}"
  img="$(mf_get "$i" pg_image)"
  [ -n "$img" ] || img="postgres:16"
  deps="$(mf_get "$i" needs; mf_get "$i" optional)"
  redis=""
  grep -qx redis <<<"$deps" && redis="redis:7"
  blocked="$(mf_get "$i" needs | grep -xE 'kafka|opensearch|scylla|minio' | tr '\n' ' ' | sed 's/ $//')"
  entry="{\"module\":$(json_str "$m"),\"wave\":$(json_str "$w"),\"workspace\":$(json_str "$ws"),\"pg_image\":$(json_str "$img"),\"redis\":$(json_str "$redis"),\"blocked\":$(json_str "$blocked")}"
  out="${out:+$out,}$entry"
done < <(mf_modules)

printf '{"include":[%s]}\n' "$out"
