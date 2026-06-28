output "ci_client_id" {
  description = "AZURE_CLIENT_ID for the GitHub Actions azure/login step."
  value       = azurerm_user_assigned_identity.ci.client_id
}

output "ci_principal_id" {
  value = azurerm_user_assigned_identity.ci.principal_id
}
