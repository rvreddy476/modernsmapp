#!/usr/bin/env bash
# Replace every __TF_<OUTPUT_NAME>__ placeholder in the production values with
# the matching Terraform output (deploy/README.md, "Placeholders").
#
#   deploy/fill-tf-outputs.sh [--check]
#
#   (no flag)  rewrite deploy/services/*/values-prod.yaml and
#              deploy/web/*/values-prod.yaml in place; review with `git diff`
#              and commit to release/prod
#   --check    list the placeholders still present and exit 1 if any
#
# The rule is mechanical: __TF_MEDIA_BUCKET_NAME__ ← `terraform output -raw
# media_bucket_name`. Every value filled this way is a NAME or an ARN, never a
# secret (connection strings and credentials reach the pods through Secrets
# Manager; tools/prodsecrets). TF_DIR defaults to infra/terraform/envs/prod;
# run with the same AWS profile Terraform uses.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF_DIR="${TF_DIR:-$ROOT/infra/terraform/envs/prod}"
mapfile -t files < <(ls "$ROOT"/deploy/services/*/values-prod.yaml "$ROOT"/deploy/web/*/values-prod.yaml)

mapfile -t tokens < <(grep -ohE '__TF_[A-Z0-9_]+__' "${files[@]}" | sort -u)

if [[ "${1:-}" == "--check" ]]; then
  if [[ ${#tokens[@]} -eq 0 ]]; then echo "no placeholders left"; exit 0; fi
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
