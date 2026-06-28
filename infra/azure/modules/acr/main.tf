# Azure Container Registry — the ACR equivalent of the AWS ECR module. Holds the
# same images (atpost/<service>, atpost/web-<zone>); CI pushes here for the Azure
# cluster. admin_enabled=false — pulls use the AKS kubelet identity (AcrPull),
# pushes use the CI workload identity (AcrPush, granted in the identity module).
resource "azurerm_container_registry" "this" {
  name                = "atpost${var.environment}" # globally unique, alphanumeric
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = "Standard"
  admin_enabled       = false

  tags = {
    Project     = "atpost"
    Environment = var.environment
  }
}

resource "azurerm_role_assignment" "aks_acrpull" {
  scope                = azurerm_container_registry.this.id
  role_definition_name = "AcrPull"
  principal_id         = var.aks_kubelet_object_id
}
