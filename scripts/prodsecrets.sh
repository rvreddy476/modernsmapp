#!/usr/bin/env bash
# Lead-side wrapper around tools/prodsecrets (W3, docs/runbooks/aws-first-production-deployment.md).
# The Go tool has no AWS SDK and never calls AWS; this script is the only
# place that does, with the caller's own credentials (aws sso login). It
# never prints a secret value.
#
#   scripts/prodsecrets.sh export-outputs   terraform output -json -> $OUT/tf-outputs.json
#   scripts/prodsecrets.sh plan [flags]     fetch, dry run (no values printed), clean up
#   scripts/prodsecrets.sh apply [flags]    fetch, generate/prompt/copy, write payloads, clean up
#                                           (flags: --rotate shared/x, --verbose, ...)
#   scripts/prodsecrets.sh roles            run $OUT/roles.sql on Aurora from inside the cluster
#   scripts/prodsecrets.sh push             put-secret-value for every payload, then delete it
#   scripts/prodsecrets.sh list             the secrets this tool fills
#
# Every command works on ONE environment: --env prod (the default) or
# --env qa, anywhere on the line (scripts/prodsecrets.sh --env qa plan), or
# PRODSECRETS_ENV=qa. Secrets are atpost/<env>/*, and each environment has
# its own working directory and Terraform directory, so QA and production
# inputs and payloads never mix. Use the AWS profile of THAT environment's
# account.
#
# Order on the first deployment (after Terraform pass 2):
#   export-outputs -> plan -> apply -> roles -> push -> plan (every key: keep)
#
# Environment: PRODSECRETS_ENV (default prod), OUT (default
# ~/.atpost/prodsecrets/<env>), TF_DIR (default infra/terraform/envs/<env>),
# AWS_REGION (default ap-south-1), AWS_PROFILE.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOOL_DIR="$ROOT/tools/prodsecrets"

# --env <name> / --env=<name> may appear anywhere; it is taken out of the
# arguments here and handed to the tool by tool() below, so the working
# directory, the Terraform directory and the tool always agree.
ENV_NAME="${PRODSECRETS_ENV:-}"
ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env)   [[ $# -ge 2 ]] || { echo "--env needs a value (prod or qa)" >&2; exit 2; }
             flag_env="$2"; shift 2 ;;
    --env=*) flag_env="${1#--env=}"; shift ;;
    *)       ARGS+=("$1"); shift; continue ;;
  esac
  if [[ -n "$ENV_NAME" && "$ENV_NAME" != "$flag_env" ]]; then
    echo "conflicting environments: PRODSECRETS_ENV/--env say '$ENV_NAME' and '$flag_env'" >&2
    exit 2
  fi
  ENV_NAME="$flag_env"
done
set -- ${ARGS[@]+"${ARGS[@]}"}
ENV_NAME="${ENV_NAME:-prod}"
case "$ENV_NAME" in
  prod|qa) ;;
  *) echo "unknown environment '$ENV_NAME' (use prod or qa)" >&2; exit 2 ;;
esac

OUT="${OUT:-$HOME/.atpost/prodsecrets/$ENV_NAME}"
TF_DIR="${TF_DIR:-$ROOT/infra/terraform/envs/$ENV_NAME}"
REGION="${AWS_REGION:-ap-south-1}"
umask 077
# Git Bash on Windows: hand native tools (go, aws) C:/... paths.
if command -v cygpath >/dev/null 2>&1; then OUT="$(cygpath -m "$OUT")"; fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }

tool() { (cd "$TOOL_DIR" && go run . --manifest manifest.yaml --env "$ENV_NAME" --out "$OUT" "$@"); }

# fetch_secret <secret-id> <file>: writes the current SecretString to file.
#   - a value                     -> the JSON as Secrets Manager holds it
#   - an empty shell (no version) -> an EMPTY file (the tool pushes a first version)
#   - anything else (missing secret, access denied, throttling) -> abort:
#     treating an unreadable secret as empty would overwrite it.
fetch_secret() {
  local id="$1" file="$2" err
  err="$(mktemp)"
  if aws secretsmanager get-secret-value --region "$REGION" --secret-id "$id" \
       --query SecretString --output text >"$file" 2>"$err"; then
    rm -f "$err"
    return 0
  fi
  if grep -q "can't find the specified secret value for staging label" "$err"; then
    : >"$file"
    rm -f "$err"
    return 0
  fi
  echo "cannot read $id:" >&2
  sed -n '1,3p' "$err" >&2   # AWS error text only; a failed read carries no value
  rm -f "$err" "$file"
  exit 1
}

cleanup_values() { rm -rf "$OUT/current" "$OUT/sources"; }

cmd_export_outputs() {
  need terraform
  mkdir -p "$OUT"
  terraform -chdir="$TF_DIR" output -json >"$OUT/tf-outputs.json"
  chmod 600 "$OUT/tf-outputs.json"
  echo "wrote $OUT/tf-outputs.json from envs/$ENV_NAME (no output there is sensitive)"
}

cmd_fetch() {
  need aws; need go
  [[ -f "$OUT/tf-outputs.json" ]] || { echo "run: scripts/prodsecrets.sh --env $ENV_NAME export-outputs" >&2; exit 1; }
  cleanup_values
  mkdir -p "$OUT/current" "$OUT/sources"
  local n=0 s=0 short full
  while read -r short full; do
    [[ -z "$short" ]] && continue
    fetch_secret "$full" "$OUT/current/$short.json"
    n=$((n + 1))
  done < <(tool --list)
  while read -r short full; do
    [[ -z "$short" ]] && continue
    fetch_secret "$full" "$OUT/sources/$short.json"
    s=$((s + 1))
  done < <(tool --list-sources)
  echo "[$ENV_NAME] fetched $n secrets to fill and $s Terraform sources (on disk only for this run)"
}

cmd_roles() {
  need kubectl
  local sql="$OUT/roles.sql"
  [[ -f "$sql" ]] || { echo "no $sql: run apply first" >&2; exit 1; }
  # Aurora is in isolated subnets: run psql in the aurora-bootstrap namespace
  # with the master credentials from the aurora-master Secret (External
  # Secrets mirrors atpost/<env>/aurora/master there). The SQL goes over stdin.
  # The current kubectl context must be THIS environment's cluster.
  echo "[$ENV_NAME] running roles.sql through kubectl context $(kubectl config current-context)"
  kubectl -n aurora-bootstrap run "prodsecrets-roles-$(date +%s)" --rm -i --quiet --restart=Never \
    --image=postgres:16-alpine \
    --overrides='{"spec":{"containers":[{"name":"psql","image":"postgres:16-alpine","stdin":true,"stdinOnce":true,"envFrom":[{"secretRef":{"name":"aurora-master"}}],"command":["sh","-c","PGPASSWORD=\"$password\" PGSSLMODE=require psql -h \"$host\" -p \"$port\" -U \"$username\" -d postgres -v ON_ERROR_STOP=1 -q -f -"]}]}}' \
    <"$sql"
  rm -f "$sql"
  echo "roles applied; $sql deleted"
}

cmd_push() {
  need aws
  local push="$OUT/put-secret-values.sh"
  [[ -x "$push" ]] || { echo "run apply first: $push missing" >&2; exit 1; }
  # The generated script must target this environment's secrets only.
  if grep -E '^put ' "$push" | grep -vq "^put atpost/$ENV_NAME/"; then
    echo "refusing: $push writes secrets outside atpost/$ENV_NAME/" >&2
    exit 1
  fi
  echo "[$ENV_NAME] pushing to atpost/$ENV_NAME/* in $REGION"
  AWS_REGION="$REGION" bash "$push"
}

case "${1:-}" in
  export-outputs) cmd_export_outputs ;;
  plan)  shift; trap cleanup_values EXIT; cmd_fetch; tool --plan "$@" ;;
  apply) shift; trap cleanup_values EXIT; cmd_fetch; tool --apply "$@" ;;
  roles) cmd_roles ;;
  push)  cmd_push ;;
  list)  tool --list ;;
  *)
    sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'
    exit 2 ;;
esac
