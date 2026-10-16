#!/usr/bin/env bash
# Run the feed timeline repair pass against ONE environment.
#
#   deploy/jobs/timeline-maintenance/run.sh <qa|prod> <feed-service image> [--apply] [-- extra flags]
#
# Without --apply it is a DRY RUN: it reads every timeline row and logs what
# it would delete, rewrite and index. Read that log before running --apply.
# Extra flags after -- go to the binary, e.g. `-- -start 1200` to resume or
# `-- -truncate-legacy-index` for the last step below.
#
# Order of operations (docs: Architecture/services/feed-service, F-24/27/28):
#   1. apply the Scylla schema (deploy/jobs/scylla-schema/apply.sh <env>):
#      adds timeline_index_by_post_v2 and the 360-day TTLs
#   2. deploy feed-service (it writes v2 only, reads v2 + legacy)
#   3. run.sh <env> <image>            dry run, check the counts
#   4. run.sh <env> <image> --apply    repair
#   5. set FEED_INDEX_LEGACY_READ=false in values-<env>.yaml and deploy
#   6. run.sh <env> <image> --apply -- -truncate-legacy-index
set -euo pipefail

usage() { echo "usage: $0 <qa|prod> <feed-service image> [--apply] [-- flags]" >&2; exit 2; }
env="${1:-}"; image="${2:-}"
case "$env" in qa|prod) ;; *) usage ;; esac
[[ -n "$image" ]] || usage
shift 2
extra=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply) extra+=("-apply"); shift ;;
    --) shift; extra+=("$@"); break ;;
    *) usage ;;
  esac
done

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS=scylla
HOST="atpost-$env-client.scylla.svc.cluster.local"

job() {
  local args=""
  for a in "${extra[@]}"; do args+=", \"$a\""; done
  sed -e "s|FEED_SERVICE_IMAGE|$image|" \
      -e "s/atpost-prod-client\\.scylla/atpost-$env-client.scylla/g" \
      -e "s|args: \\[\\(.*\\)\\]|args: [\\1$args]|" \
      "$HERE/job.yaml"
}
job | grep -q "value: $HOST" || { echo "job.yaml: SCYLLA_HOSTS did not resolve to $HOST" >&2; exit 1; }
job | grep -q "image: $image" || { echo "job.yaml: image did not resolve" >&2; exit 1; }

kubectl -n "$NS" delete job timeline-maintenance --ignore-not-found
job | kubectl apply -f -
echo "following logs; the Job keeps running if this is interrupted"
kubectl -n "$NS" wait --for=condition=ready pod -l app.kubernetes.io/name=timeline-maintenance --timeout=10m
kubectl -n "$NS" logs -f job/timeline-maintenance
