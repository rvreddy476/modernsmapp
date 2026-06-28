output "id" {
  value = azurerm_container_registry.this.id
}

output "login_server" {
  description = "e.g. atpoststaging.azurecr.io — image prefix for the Azure Helm values."
  value       = azurerm_container_registry.this.login_server
}
