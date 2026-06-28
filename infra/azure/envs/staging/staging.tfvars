# Azure staging — fill-ins for this subscription. Globally-unique names
# (registry_name, key_vault_name) carry a subscription-derived suffix so the
# first apply doesn't 409; override them here if you ever change subscription.
subscription_id = "454350d4-cc70-4bfa-b434-820b86f62f4d"
location        = "centralindia"
# registry_name / key_vault_name default to the suffixed names in variables.tf.

# --- AKS node sizing ---
# DSv5 family has 0 quota on this subscription; using DSv4 (already enabled,
# same core counts). After the quota bump (Total Regional + DSv4) lands, these
# work as-is. Run scripts/azure-check-quota.sh centralindia to verify.
system_vm_size  = "Standard_D4s_v4" # 4 vCPU — hosts platform tooling (ESO/ArgoCD/ingress/operator)
general_vm_size = "Standard_D4s_v4" # 4 vCPU — app + data-platform pods
# Switch back to v5 if/when you raise DSv5 quota:
#   system_vm_size  = "Standard_D2s_v5"
#   general_vm_size = "Standard_D4s_v5"
