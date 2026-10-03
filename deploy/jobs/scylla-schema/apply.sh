#!/usr/bin/env bash
# Apply the production Scylla schema to the EKS operator cluster.
#
#   deploy/jobs/scylla-schema/apply.sh            build the ConfigMap, run the Job, wait
#   deploy/jobs/scylla-schema/apply.sh --dry-run  client-side dry run of both objects
#
# Run after Terraform pass 2 (the Scylla cluster is Ready:
# `kubectl -n scylla get scyllacluster`) and before the first sync of any
# Scylla-backed service. Re-running is safe (IF NOT EXISTS everywhere).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/deploy/jobs/scylla-schema"
NS=scylla
dry=()
[[ "${1:-}" == "--dry-run" ]] && dry=(--dry-run=client)

platform="$ROOT/Architecture/docker/scylla/schema.prod.cql"
chat="$ROOT/chat-service/services/message-service/scylla/schema.prod.cql"
for f in "$platform" "$chat"; do
  [[ -f "$f" ]] || { echo "missing $f" >&2; exit 1; }
  # Guard: only the production files (RF 3, NetworkTopologyStrategy) go here.
  if grep -q "SimpleStrategy" "$f"; then echo "$f uses SimpleStrategy; refusing" >&2; exit 1; fi
done

kubectl -n "$NS" create configmap scylla-schema-prod \
  --from-file=10-platform.cql="$platform" \
  --from-file=20-chat-message.cql="$chat" \
  --dry-run=client -o yaml | kubectl apply "${dry[@]}" -f -

if [[ ${#dry[@]} -gt 0 ]]; then
  kubectl apply "${dry[@]}" -f "$HERE/job.yaml"
  exit 0
fi

# A finished Job is immutable; replace it so a re-run picks up new CQL.
kubectl -n "$NS" delete job scylla-schema --ignore-not-found
kubectl apply -f "$HERE/job.yaml"
kubectl -n "$NS" wait --for=condition=complete job/scylla-schema --timeout=30m
kubectl -n "$NS" logs job/scylla-schema | tail -40
