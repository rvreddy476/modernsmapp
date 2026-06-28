# Azure Database for PostgreSQL — Flexible Server. Wire-compatible
# managed Postgres (the AWS twin is Aurora PostgreSQL). Services read it
# via POSTGRES_DSN / host+user+password env, sourced from a Key Vault
# JSON secret that mirrors the AWS Secrets Manager shape
# {username, password, host, port} — so the per-service ExternalSecret
# data: mappings are reused unchanged.
#
# Networking: VNet-integrated (delegated `data` subnet from the network
# module) + a private DNS zone, so the server is reachable only from the
# VNet — no public endpoint.

resource "random_password" "admin" {
  length  = 32
  special = false # keep the DSN URL-safe
}

# Private DNS zone for the flexible server's private FQDN.
resource "azurerm_private_dns_zone" "postgres" {
  name                = "atpost-${var.environment}-pg.private.postgres.database.azure.com"
  resource_group_name = var.resource_group_name
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                  = "atpost-${var.environment}-pg-link"
  resource_group_name   = var.resource_group_name
  private_dns_zone_name = azurerm_private_dns_zone.postgres.name
  virtual_network_id    = var.vnet_id
  registration_enabled  = false
}

resource "azurerm_postgresql_flexible_server" "this" {
  name                = "atpost-${var.environment}-pg"
  resource_group_name = var.resource_group_name
  location            = var.location

  version                       = var.postgres_version
  administrator_login           = var.admin_username
  administrator_password        = random_password.admin.result
  sku_name                      = var.sku_name
  storage_mb                    = var.storage_mb
  auto_grow_enabled             = true
  zone                          = "1"
  public_network_access_enabled = false

  delegated_subnet_id = var.data_subnet_id
  private_dns_zone_id = azurerm_private_dns_zone.postgres.id

  dynamic "high_availability" {
    for_each = var.ha_enabled ? [1] : []
    content {
      mode                      = "ZoneRedundant"
      standby_availability_zone = "2"
    }
  }

  backup_retention_days = var.backup_retention_days

  tags = {
    environment = var.environment
    managed-by  = "terraform"
  }

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]
}

# Per-service databases on the one server (matches the Aurora layout
# where each service owns a logical DB).
resource "azurerm_postgresql_flexible_server_database" "dbs" {
  for_each = toset(var.databases)

  name      = each.value
  server_id = azurerm_postgresql_flexible_server.this.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

# Connection secret in Key Vault — same JSON shape as the AWS module's
# Secrets Manager entry so ESO's property extraction is identical.
resource "azurerm_key_vault_secret" "connection" {
  name         = var.secret_name
  key_vault_id = var.key_vault_id

  value = jsonencode({
    username = azurerm_postgresql_flexible_server.this.administrator_login
    password = random_password.admin.result
    host     = azurerm_postgresql_flexible_server.this.fqdn
    port     = 5432
  })

  tags = {
    environment = var.environment
  }
}
