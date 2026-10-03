#!/usr/bin/env bash
# Apply the production ApplicationSets by hand, filling the AWS account id.
#
#   deploy/argocd/apply-prod-applicationsets.sh <12-digit account id> [--dry-run]
#
# Only for when Terraform does NOT apply deploy/argocd/applicationset.yaml
# (or before its argocd module renders the file with templatefile; see
# deploy/README.md "AWS account id"). One owner per object: do not run this
# and let Terraform manage the same ApplicationSets.
#
# The account id is not a secret, but it is never committed: it is substituted
# into the stream piped to kubectl and nothing is written to disk.
set -euo pipefail

acct="${1:-}"
[[ "$acct" =~ ^[0-9]{12}$ ]] || { echo "usage: $0 <12-digit AWS account id> [--dry-run]" >&2; exit 2; }
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
file="$here/applicationset.yaml"

# Exactly the Terraform token, nothing broader.
token='${aws_account_id}'
grep -qF "$token" "$file" || { echo "$file: token not found (already filled?)" >&2; exit 1; }

rendered="$(sed 's/\$[{]aws_account_id[}]/'"$acct"'/g' "$file")"
if grep -qF "$token" <<<"$rendered"; then
  echo "substitution failed" >&2; exit 1
fi

if [[ "${2:-}" == "--dry-run" ]]; then
  kubectl apply --dry-run=client -f - <<<"$rendered"
else
  kubectl apply -f - <<<"$rendered"
fi
