#!/usr/bin/env bash
# azure-check-quota.sh — show your compute vCPU quota for the region so you
# can see which VM family has headroom for AKS (the fresh-subscription
# default is often 0 for newer families like DSv5). Run from YOUR machine.
#
# Usage: scripts/azure-check-quota.sh [region]   (default: centralindia)
set -euo pipefail
REGION="${1:-centralindia}"
az account show >/dev/null 2>&1 || { echo "run 'az login' first" >&2; exit 1; }

echo "vCPU quota in $REGION (CurrentValue / Limit) — families with Limit>0 and headroom are usable:"
az vm list-usage --location "$REGION" -o table \
  | awk 'NR<=2 || /vCPU|Total Regional/' || true

echo
echo "To raise quota: Azure Portal → Quotas → Compute → region=$REGION → pick the"
echo "family (e.g. 'Standard DSv5 Family vCPUs') → Increase. Small bumps auto-approve."
echo
echo "To use a family you already have, set it in infra/azure/envs/<env>/<env>.tfvars:"
echo '  system_vm_size  = "Standard_D2as_v5"   # AMD DAsv5 — often has default quota'
echo '  general_vm_size = "Standard_D4as_v5"'
