#!/usr/bin/env bash
# Apply the Scylla schema of ONE environment to its EKS operator cluster.
#
#   deploy/jobs/scylla-schema/apply.sh <qa|prod>            build the ConfigMap, run the Job, wait
#   deploy/jobs/scylla-schema/apply.sh <qa|prod> --dry-run  client-side dry run of both objects
#
# The environment is REQUIRED (no default): the current kubectl context must
# be that environment's cluster, and the CQL differs between them:
#   prod  schema.prod.cql  NetworkTopologyStrategy, replication_factor 3
#   qa    schema.qa.cql    NetworkTopologyStrategy, replication_factor 1 (one node)
# Both are committed renders of the dev schema (Architecture/tools/scyllaschema
# -env <env>; its tests fail when either is stale).
#
# Run after Terraform pass 2 (the Scylla cluster is Ready:
# `kubectl -n scylla get scyllacluster`) and before the first sync of any
# Scylla-backed service. Re-running is safe (IF NOT EXISTS everywhere).
set -euo pipefail

usage() { echo "usage: $0 <qa|prod> [--dry-run]" >&2; exit 2; }
env="${1:-}"
case "$env" in
  prod) rf=3 ;;
  qa)   rf=1 ;;
  *)    usage ;;
esac
dry=()
case "${2:-}" in
  "") ;;
  --dry-run) dry=(--dry-run=client) ;;
  *) usage ;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/deploy/jobs/scylla-schema"
NS=scylla
CM="scylla-schema-$env"
# The operator-managed client Service of ScyllaCluster atpost-<env>
# (infra/terraform/modules/scylla).
HOST="atpost-$env-client.scylla.svc.cluster.local"

platform="$ROOT/Architecture/docker/scylla/schema.$env.cql"
chat="$ROOT/chat-service/services/message-service/scylla/schema.$env.cql"
for f in "$platform" "$chat"; do
  [[ -f "$f" ]] || { echo "missing $f" >&2; exit 1; }
  # Guard: only this environment's render (NetworkTopologyStrategy at its RF).
  if grep -q "SimpleStrategy" "$f"; then echo "$f uses SimpleStrategy; refusing" >&2; exit 1; fi
  if grep -o "'replication_factor': [0-9]*" "$f" | grep -vq "'replication_factor': $rf\$"; then
    echo "$f has a replication factor other than $rf; refusing" >&2; exit 1
  fi
done

# The Job manifest is written for prod; point it at this environment's
# ConfigMap and client Service.
job() {
  sed -e "s/scylla-schema-prod/$CM/g" -e "s/atpost-prod-client\\.scylla/atpost-$env-client.scylla/g" "$HERE/job.yaml"
}
job | grep -q "value: $HOST" || { echo "job.yaml: SCYLLA_HOST did not resolve to $HOST" >&2; exit 1; }
job | grep -q "name: $CM" || { echo "job.yaml: ConfigMap did not resolve to $CM" >&2; exit 1; }

kubectl -n "$NS" create configmap "$CM" \
  --from-file=10-platform.cql="$platform" \
  --from-file=20-chat-message.cql="$chat" \
  --dry-run=client -o yaml | kubectl apply "${dry[@]}" -f -

if [[ ${#dry[@]} -gt 0 ]]; then
  job | kubectl apply "${dry[@]}" -f -
  exit 0
fi

# A finished Job is immutable; replace it so a re-run picks up new CQL.
kubectl -n "$NS" delete job scylla-schema --ignore-not-found
job | kubectl apply -f -
kubectl -n "$NS" wait --for=condition=complete job/scylla-schema --timeout=30m
kubectl -n "$NS" logs job/scylla-schema | tail -40
