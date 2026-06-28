# Azure staging — fill-ins for this subscription. Globally-unique names
# (registry_name, key_vault_name) carry a subscription-derived suffix so the
# first apply doesn't 409; override them here if you ever change subscription.
subscription_id = "454350d4-cc70-4bfa-b434-820b86f62f4d"
location        = "centralindia"
# registry_name / key_vault_name default to the suffixed names in variables.tf.

# --- AKS node sizing ---
# Fresh subscriptions often have 0 quota for the DSv5 family. Run
# scripts/azure-check-quota.sh centralindia to see which family has headroom,
# then either request a quota increase for DSv5, or switch family here, e.g.:
#   system_vm_size  = "Standard_D2as_v5"   # AMD (DAsv5) — commonly available
#   general_vm_size = "Standard_D4as_v5"
# Minimal footprint (cost/quota): system 1 node, general 1-4 nodes (defaults).
# system_vm_size  = "Standard_D2s_v5"
# general_vm_size = "Standard_D4s_v5"
