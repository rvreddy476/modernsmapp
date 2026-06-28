output "identity_client_id" {
  value = azurerm_user_assigned_identity.eso.client_id
}

output "identity_principal_id" {
  value = azurerm_user_assigned_identity.eso.principal_id
}

output "secret_store_name" {
  value = "azure-key-vault"
}
