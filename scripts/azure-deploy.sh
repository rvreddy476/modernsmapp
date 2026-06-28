#!/usr/bin/env bash
# azure-deploy.sh — one-shot Azure bring-up for an environment. Run it from
# YOUR machine (it needs network + your Azure login — it cannot run from the
# Claude sandbox). It just chains the steps in docs/DEPLOY-azure.md so you
# don't run them one at a time.
#
# Each terraform step PLANS first and shows you the diff, then asks before
# applying (so you preview every change). Pass --yes to skip the prompts, or
# --plan-only to preview without applying anything.
#
# Prerequisites (one time):
#   - az login          (and `az account set --subscription <id>` if you have many)
#   - terraform, kubectl, az, openssl on PATH
#
# Usage:
#   scripts/azure-deploy.sh staging              # interactive (plan → confirm → apply)
#   scripts/azure-deploy.sh staging --plan-only  # preview only, no changes
#   scripts/azure-deploy.sh staging --yes        # non-interactive (CI)
#
# Safe to re-run: terraform is declarative, seed-keyvault is idempotent, and
# kubectl apply is declarative. If a step fails, fix it and run again.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

ENV="staging"; AUTO=0; PLAN_ONLY=0
for a in "$@"; do
  case "$a" in
    staging|prod) ENV="$a" ;;
    -y|--yes)     AUTO=1 ;;
    --plan-only)  PLAN_ONLY=1 ;;
    *) echo "usage: $0 [staging|prod] [--plan-only] [--yes]" >&2; exit 1 ;;
  esac
done
SUB="454350d4-cc70-4bfa-b434-820b86f62f4d"
ENVDIR="infra/azure/envs/$ENV"
RG="atpost-$ENV"
PLAN="tfplan.out"

say()  { printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }
note() { printf '\033[1;33m%s\033[0m\n' "$*"; }

confirm() { # confirm "question" — true to proceed
  [ "$AUTO" = 1 ] && return 0
  read -r -p "$1 [y/N] " ans
  [[ "$ans" =~ ^[Yy]$ ]]
}

# plan_apply <dir> <plan-label> [extra terraform args…] — plan to a file,
# show it, confirm, then apply that exact plan.
plan_apply() {
  local dir="$1" label="$2"; shift 2
  say "PLAN — $label"
  terraform -chdir="$dir" plan -input=false -out="$PLAN" "$@"
  if [ "$PLAN_ONLY" = 1 ]; then
    note "plan-only: skipping apply for $label"
    return 0
  fi
  if confirm "Apply this plan ($label)?"; then
    say "APPLY — $label"
    terraform -chdir="$dir" apply -input=false "$PLAN"
  else
    note "skipped $label"; return 1
  fi
}

# 0. sanity
command -v az >/dev/null || { echo "az not found"; exit 1; }
command -v terraform >/dev/null || { echo "terraform not found"; exit 1; }
command -v kubectl >/dev/null || { echo "kubectl not found"; exit 1; }
az account show >/dev/null 2>&1 || { echo "run 'az login' first"; exit 1; }
az account set --subscription "$SUB"

# 0.5 register resource providers (azurerm auto-registration is disabled)
say "register resource providers"
"$ROOT/scripts/azure-register-providers.sh"

# 1. remote state backend (Storage Account + container)
say "bootstrap remote state"
terraform -chdir=infra/azure/bootstrap init -input=false
plan_apply infra/azure/bootstrap "tfstate backend" -var "subscription_id=$SUB"

# 2. init env with the azurerm backend
say "terraform init ($ENV)"
terraform -chdir="$ENVDIR" init -input=false

# 3. PASS 1 — cluster + registry + identity only (k8s/helm providers can't
#    plan until the AKS API exists, so we target the infra modules first).
plan_apply "$ENVDIR" "PASS 1 — cluster/registry/identity" -var-file="$ENV.tfvars" \
  -target=module.resource_group -target=module.network \
  -target=module.aks -target=module.acr -target=module.identity

# In plan-only mode the cluster doesn't exist, so stop before the steps that
# need a live cluster (kubeconfig, platform plan, seeding, appsets).
if [ "$PLAN_ONLY" = 1 ]; then
  note "plan-only: stopping before cluster-dependent steps (PASS 2, seed, appsets)."
  exit 0
fi

# 4. kubeconfig for the new cluster
say "fetch kubeconfig"
AKS_NAME="$(terraform -chdir="$ENVDIR" output -raw aks_name)"
az aks get-credentials -g "$RG" -n "$AKS_NAME" --overwrite-existing

# 5. PASS 2 — full apply (platform: ESO/KeyVault, ingress-nginx, ArgoCD,
#    managed Postgres+Redis, self-hosted Scylla/Redpanda/MinIO).
plan_apply "$ENVDIR" "PASS 2 — platform (full)" -var-file="$ENV.tfvars"

# 6. seed per-service Key Vault secrets
say "seed Key Vault secrets"
"$ROOT/scripts/seed-keyvault.sh" "$ENV"

# 7. hand the ApplicationSets to ArgoCD
say "apply ArgoCD ApplicationSets"
kubectl apply -f deploy/azure-applicationset.yaml
kubectl apply -f deploy/web-azure-applicationset.yaml

say "DONE — $ENV core is up"
cat <<EOF

Outputs:
  Key Vault : $(terraform -chdir="$ENVDIR" output -raw key_vault_uri 2>/dev/null || echo n/a)
  ACR       : $(terraform -chdir="$ENVDIR" output -raw acr_login_server 2>/dev/null || echo n/a)
  CI client : $(terraform -chdir="$ENVDIR" output -raw ci_client_id 2>/dev/null || echo n/a)

Still to do (not automated — they need your inputs):
  • Build + push images to ACR (CI 'build-push-acr.yml' after you set the
    GitHub repo Variables, or local 'docker buildx ... --push'). Until images
    exist in ACR, pods will ImagePullBackOff — expected.
  • Edge: once 'kubectl -n ingress-nginx get svc' shows an EXTERNAL-IP, set
    enable_edge=true + edge_origin_host_name/edge_zone_name in $ENV.tfvars and
    re-run this script (or 'terraform apply') to create Front Door + WAF + DNS.
  • Fill any blank fields seed-keyvault.sh warned about (3rd-party API keys).
EOF
