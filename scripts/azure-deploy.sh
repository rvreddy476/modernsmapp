#!/usr/bin/env bash
# azure-deploy.sh — one-shot Azure bring-up for an environment. Run it from
# YOUR machine (it needs network + your Azure login — it cannot run from the
# Claude sandbox). It just chains the steps in docs/DEPLOY-azure.md so you
# don't run them one at a time.
#
# Prerequisites (one time):
#   - az login          (and `az account set --subscription <id>` if you have many)
#   - terraform, kubectl, az, openssl on PATH
#
# Usage:  scripts/azure-deploy.sh staging      # or: prod
#
# Safe to re-run: terraform is declarative, seed-keyvault is idempotent, and
# kubectl apply is declarative. If a step fails, fix it and run again.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

ENV="${1:-staging}"
case "$ENV" in staging|prod) ;; *) echo "usage: $0 [staging|prod]" >&2; exit 1 ;; esac
SUB="454350d4-cc70-4bfa-b434-820b86f62f4d"
ENVDIR="infra/azure/envs/$ENV"
RG="atpost-$ENV"

say() { printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }

# 0. sanity
command -v az >/dev/null || { echo "az not found"; exit 1; }
command -v terraform >/dev/null || { echo "terraform not found"; exit 1; }
command -v kubectl >/dev/null || { echo "kubectl not found"; exit 1; }
az account show >/dev/null 2>&1 || { echo "run 'az login' first"; exit 1; }
az account set --subscription "$SUB"

# 1. remote state backend (Storage Account + container)
say "1/7 bootstrap remote state"
terraform -chdir=infra/azure/bootstrap init -input=false
terraform -chdir=infra/azure/bootstrap apply -auto-approve -var "subscription_id=$SUB"

# 2. init env with the azurerm backend
say "2/7 terraform init ($ENV)"
terraform -chdir="$ENVDIR" init -input=false

# 3. PASS 1 — cluster + registry + identity only (k8s/helm providers can't
#    plan until the AKS API exists, so we target the infra modules first).
say "3/7 apply PASS 1 — cluster/registry/identity"
terraform -chdir="$ENVDIR" apply -auto-approve -var-file="$ENV.tfvars" \
  -target=module.resource_group -target=module.network \
  -target=module.aks -target=module.acr -target=module.identity

# 4. kubeconfig for the new cluster
say "4/7 fetch kubeconfig"
AKS_NAME="$(terraform -chdir="$ENVDIR" output -raw aks_name)"
az aks get-credentials -g "$RG" -n "$AKS_NAME" --overwrite-existing

# 5. PASS 2 — full apply (platform: ESO/KeyVault, ingress-nginx, ArgoCD,
#    managed Postgres+Redis, self-hosted Scylla/Redpanda/MinIO).
say "5/7 apply PASS 2 — platform (full)"
terraform -chdir="$ENVDIR" apply -auto-approve -var-file="$ENV.tfvars"

# 6. seed per-service Key Vault secrets
say "6/7 seed Key Vault secrets"
"$ROOT/scripts/seed-keyvault.sh" "$ENV"

# 7. hand the ApplicationSets to ArgoCD
say "7/7 apply ArgoCD ApplicationSets"
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
