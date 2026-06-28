output "namespace" {
  value = kubernetes_namespace.argocd.metadata[0].name
}

output "admin_secret_name" {
  value = azurerm_key_vault_secret.admin.name
}
