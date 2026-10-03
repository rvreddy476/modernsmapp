#!/usr/bin/env bash
# Replace every __TF_<OUTPUT_NAME>__ placeholder in ONE environment's values
# with the matching Terraform output of that environment (deploy/README.md,
# "Placeholders").
#
#   deploy/fill-tf-outputs.sh <qa|prod> [--check]
#
#   (no flag)  rewrite deploy/services/*/values-<env>.yaml and
#              deploy/web/*/values-<env>.yaml in place (no other file is
#              read or written); review with `git diff` and commit to the
#              environment's branch (prod: release/prod, qa: qa)
#   --check    list the placeholders still present and exit 1 if any
#
# The environment is REQUIRED: the QA and production accounts have different
# buckets and ARNs, and filling one environment's files from the other's
# state would point it at another account.
#
# The rule is mechanical: __TF_MEDIA_BUCKET_NAME__ ← `terraform output -raw
# media_bucket_name`. Every value filled this way is a NAME or an ARN, never a
# secret (connection strings and credentials reach the pods through Secrets
# Manager; tools/prodsecrets). TF_DIR defaults to infra/terraform/envs/<env>;
# run with the same AWS profile Terraform uses for that account.
set -euo pipefail

usage() { echo "usage: $0 <qa|prod> [--check]" >&2; exit 2; }
ENV_NAME="${1:-}"
case "$ENV_NAME" in qa|prod) ;; *) usage ;; esac
MODE="${2:-}"
case "$MODE" in ""|--check) ;; *) usage ;; esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF_DIR="${TF_DIR:-$ROOT/infra/terraform/envs/$ENV_NAME}"
mapfile -t files < <(ls "$ROOT"/deploy/services/*/values-"$ENV_NAME".yaml "$ROOT"/deploy/web/*/values-"$ENV_NAME".yaml)
[[ ${#files[@]} -gt 0 ]] || { echo "no values-$ENV_NAME.yaml files" >&2; exit 1; }

mapfile -t tokens < <(grep -ohE '__TF_[A-Z0-9_]+__' "${files[@]}" | sort -u)

if [[ "$MODE" == "--check" ]]; then
  if [[ ${#tokens[@]} -eq 0 ]]; then echo "no placeholders left in values-$ENV_NAME.yaml"; exit 0; fi
  for t in "${tokens[@]}"; do
    printf '%s\n' "$t"; grep -l -- "$t" "${files[@]}" | sed "s|^$ROOT/|    |"
  done
  exit 1
fi

[[ ${#tokens[@]} -eq 0 ]] && { echo "no placeholders left"; exit 0; }
command -v terraform >/dev/null || { echo "terraform not found" >&2; exit 1; }

for t in "${tokens[@]}"; do
  name="${t#__TF_}"; name="${name%__}"; name="$(tr 'A-Z' 'a-z' <<<"$name")"
  value="$(terraform -chdir="$TF_DIR" output -raw "$name")" || { echo "terraform output $name failed" >&2; exit 1; }
  [[ -n "$value" && "$value" != *$'\n'* ]] || { echo "terraform output $name is empty or multi-line" >&2; exit 1; }
  # Names and ARNs only: refuse anything that would break the sed below.
  [[ "$value" =~ ^[A-Za-z0-9:/._@+=,-]+$ ]] || { echo "terraform output $name has unexpected characters" >&2; exit 1; }
  for f in "${files[@]}"; do
    grep -q -- "$t" "$f" || continue
    sed -i "s|$t|$value|g" "$f"
    echo "filled $name in ${f#$ROOT/}"
  done
done
