output "fqdn" {
  value = azurerm_postgresql_flexible_server.this.fqdn
}

output "server_name" {
  value = azurerm_postgresql_flexible_server.this.name
}

output "secret_name" {
  value = azurerm_key_vault_secret.connection.name
}
