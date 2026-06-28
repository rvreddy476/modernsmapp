# Staging Azure environment — composes the skeleton (Phase 1). The platform
# layer (nginx/ESO/ArgoCD/Redpanda/MinIO/Scylla, managed Postgres/Redis) and
# Front Door land in Phases 2–3. AWS infra/terraform/ is untouched.
module "resource_group" {
  source = "../../modules/resource-group"

  environment = var.environment
  location    = var.location
}

module "network" {
  source = "../../modules/network"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  vnet_cidr           = "10.41.0.0/16"
}

module "aks" {
  source = "../../modules/aks"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  aks_subnet_id       = module.network.aks_subnet_id
  general_min         = 1
  general_max         = 4
}

module "acr" {
  source = "../../modules/acr"

  environment           = var.environment
  resource_group_name   = module.resource_group.name
  location              = module.resource_group.location
  aks_kubelet_object_id = module.aks.kubelet_object_id
}

module "identity" {
  source = "../../modules/identity"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  acr_id              = module.acr.id
  aks_id              = module.aks.id
  github_subjects     = var.github_subjects
}

output "aks_name" { value = module.aks.name }
output "aks_oidc_issuer_url" { value = module.aks.oidc_issuer_url }
output "acr_login_server" { value = module.acr.login_server }
output "ci_client_id" { value = module.identity.ci_client_id }
output "resource_group" { value = module.resource_group.name }
