# Key Vault — Azure twin of AWS Secrets Manager. Holds one JSON secret
# per service (mirroring the atpost/<env>/<svc> layout); the Azure
# ClusterSecretStore (external-secrets module) reads from here and the
# postgres/redis/data-platform modules write managed-store creds into it.
#
# RBAC authorization (not access policies): cleaner, role-assignment
# based, matches the Workload-Identity model. The deploying principal
# gets Secrets Officer so the postgres/redis modules can write secrets;
# ESO gets Secrets User (read-only) via the external-secrets module.
#
# NOTE: Key Vault secret names allow only [0-9a-zA-Z-]. The AWS layout
# uses slashes (atpost/<env>/<svc>) — the gen-azure-values transform
# rewrites those to atpost-<env>-<svc>. Keep that mapping in mind when
# seeding secrets here.

data "azurerm_client_config" "current" {}

resource "azurerm_key_vault" "this" {
  name                = var.key_vault_name
  resource_group_name = var.resource_group_name
  location            = var.location
  tenant_id           = data.azurerm_client_config.current.tenant_id
  sku_name            = "standard"

  enable_rbac_authorization  = true
  purge_protection_enabled   = var.purge_protection_enabled
  soft_delete_retention_days = 7

  # Network: default deny + allow Azure services (AKS reaches it through
  # the platform; tighten to a private endpoint in a follow-up if the
  # threat model requires it).
  public_network_access_enabled = true
  network_acls {
    bypass         = "AzureServices"
    default_action = var.network_default_action
    ip_rules       = var.allowed_ip_rules
  }

  tags = {
    environment = var.environment
    managed-by  = "terraform"
  }
}

# Let whoever runs `terraform apply` write secrets into the vault
# (postgres/redis modules create azurerm_key_vault_secret resources).
resource "azurerm_role_assignment" "deployer_secrets_officer" {
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}
