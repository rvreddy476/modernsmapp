# CI identity — GitHub Actions federates (OIDC) into this user-assigned managed
# identity (the Entra equivalent of the AWS GitHub-OIDC CI role). No static
# secrets: workflows `azure/login` with client-id/tenant/subscription. Granted
# AcrPush (build/push images) + AKS deploy. Service/ESO Workload Identities are
# added in Phase 2 alongside Key Vault.
resource "azurerm_user_assigned_identity" "ci" {
  name                = "atpost-${var.environment}-ci"
  resource_group_name = var.resource_group_name
  location            = var.location
}

resource "azurerm_federated_identity_credential" "ci_github" {
  for_each = toset(var.github_subjects)

  name      = "gh-${replace(replace(each.value, ":", "-"), "/", "-")}"
  parent_id = azurerm_user_assigned_identity.ci.id
  audience  = ["api://AzureADTokenExchange"]
  issuer    = "https://token.actions.githubusercontent.com"
  subject   = each.value
}

# Push images to ACR.
resource "azurerm_role_assignment" "ci_acrpush" {
  scope                = var.acr_id
  role_definition_name = "AcrPush"
  principal_id         = azurerm_user_assigned_identity.ci.principal_id
}

# Deploy to AKS (cluster-user; ArgoCD does most deploys, this is for bootstrap
# kubectl/helm from CI).
resource "azurerm_role_assignment" "ci_aks" {
  scope                = var.aks_id
  role_definition_name = "Azure Kubernetes Service Cluster User Role"
  principal_id         = azurerm_user_assigned_identity.ci.principal_id
}
