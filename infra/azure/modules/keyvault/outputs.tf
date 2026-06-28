output "id" {
  value = azurerm_key_vault.this.id
}

output "name" {
  value = azurerm_key_vault.this.name
}

output "uri" {
  value = azurerm_key_vault.this.vault_uri
}

output "tenant_id" {
  value = data.azurerm_client_config.current.tenant_id
}
