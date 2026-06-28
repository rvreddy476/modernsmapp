# Azure Cache for Redis — managed Redis (AWS twin is ElastiCache).
# Redis is cache/rebuildable, so unlike Postgres this is not part of the
# data-migration critical path on a cloud switch. Services read it via
# REDIS_ADDR (+ password); the Key Vault secret carries {host, port,
# password} for ESO to map.

resource "azurerm_redis_cache" "this" {
  name                = "atpost-${var.environment}-redis"
  resource_group_name = var.resource_group_name
  location            = var.location

  capacity            = var.capacity
  family              = var.family
  sku_name            = var.sku_name
  non_ssl_port_enabled = false
  minimum_tls_version = "1.2"

  redis_configuration {
    maxmemory_policy = "allkeys-lru"
  }

  tags = {
    environment = var.environment
    managed-by  = "terraform"
  }
}

# Connection secret in Key Vault. host:port is the TLS endpoint; the
# password is the primary access key.
resource "azurerm_key_vault_secret" "connection" {
  name         = var.secret_name
  key_vault_id = var.key_vault_id

  value = jsonencode({
    host     = azurerm_redis_cache.this.hostname
    port     = azurerm_redis_cache.this.ssl_port
    password = azurerm_redis_cache.this.primary_access_key
    addr     = "${azurerm_redis_cache.this.hostname}:${azurerm_redis_cache.this.ssl_port}"
  })

  tags = {
    environment = var.environment
  }
}
