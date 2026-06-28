output "hostname" {
  value = azurerm_redis_cache.this.hostname
}

output "ssl_port" {
  value = azurerm_redis_cache.this.ssl_port
}

output "secret_name" {
  value = azurerm_key_vault_secret.connection.name
}
