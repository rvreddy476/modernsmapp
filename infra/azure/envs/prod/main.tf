# Prod Azure environment — same module composition as staging; prod sizing.
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
  vnet_cidr           = "10.42.0.0/16"
}

module "aks" {
  source = "../../modules/aks"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  aks_subnet_id       = module.network.aks_subnet_id
  system_min          = 2
  system_max          = 4
  general_vm_size     = "Standard_D4s_v5"
  general_min         = 3
  general_max         = 12
}

module "acr" {
  source = "../../modules/acr"

  environment           = var.environment
  resource_group_name   = module.resource_group.name
  location              = module.resource_group.location
  aks_kubelet_object_id = module.aks.kubelet_object_id
  registry_name         = var.registry_name
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
