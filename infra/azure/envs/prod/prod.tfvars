# Azure prod — fill-ins for this subscription. Globally-unique names
# (registry_name, key_vault_name) carry a subscription-derived suffix so the
# first apply doesn't 409; override them here if you ever change subscription.
subscription_id = "454350d4-cc70-4bfa-b434-820b86f62f4d"
location        = "centralindia"
# registry_name / key_vault_name default to the suffixed names in variables.tf.
