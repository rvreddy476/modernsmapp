#!/usr/bin/env bash
# azure-register-providers.sh — register the Azure Resource Providers this
# stack uses, via az (one call each, with retry). Needed because the azurerm
# provider blocks set resource_provider_registrations = "none" (Terraform's
# bulk auto-registration fails on restricted subscriptions / flaky egress).
#
# Registration is async + idempotent; the script registers all, then polls
# until each is "Registered". Re-run anytime. Requires: az (logged in).
set -euo pipefail

az account show >/dev/null 2>&1 || { echo "run 'az login' first" >&2; exit 1; }

PROVIDERS=(
  Microsoft.ManagedIdentity      # user-assigned identities + federated creds
  Microsoft.Network              # VNet, subnets, DNS, private DNS
  Microsoft.Storage              # tfstate storage account
  Microsoft.Compute              # AKS node VMs / disks
  Microsoft.ContainerService     # AKS
  Microsoft.ContainerRegistry    # ACR
  Microsoft.KeyVault             # Key Vault
  Microsoft.DBforPostgreSQL      # Flexible Server
  Microsoft.Cache                # Azure Cache for Redis
  Microsoft.OperationalInsights  # Log Analytics (AKS deps)
  Microsoft.Insights             # diagnostics / monitoring
  Microsoft.Cdn                  # Front Door + WAF
  Microsoft.Authorization        # role assignments
)

reg() { # retry a few times — egress can be flaky
  local ns="$1" i
  for i in 1 2 3 4 5; do
    if az provider register --namespace "$ns" -o none 2>/dev/null; then return 0; fi
    echo "  retry $i registering $ns…"; sleep 5
  done
  echo "  ! could not kick off registration for $ns (check connectivity)"; return 1
}

echo "Requesting registration for ${#PROVIDERS[@]} providers…"
for ns in "${PROVIDERS[@]}"; do echo "  + $ns"; reg "$ns" || true; done

echo "Waiting for registration to complete (this can take a few minutes)…"
for ns in "${PROVIDERS[@]}"; do
  for i in $(seq 1 60); do
    state="$(az provider show --namespace "$ns" --query registrationState -o tsv 2>/dev/null || echo Unknown)"
    [ "$state" = "Registered" ] && { printf '  %-32s Registered\n' "$ns"; break; }
    sleep 10
    [ "$i" = 60 ] && printf '  %-32s %s (still pending — apply may need a retry)\n' "$ns" "$state"
  done
done
echo "done."
