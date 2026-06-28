# AKS cluster — the Azure equivalent of the AWS EKS module. OIDC issuer +
# Workload Identity enabled (the Entra equivalent of IRSA) so ESO/CI/services
# can federate without static secrets. System pool for platform, autoscaling
# general pool for app workloads (node label workload=general matches the chart's
# nodeSelector). azurerm ~> 4.x attribute names.
resource "azurerm_kubernetes_cluster" "this" {
  name                = "atpost-${var.environment}"
  resource_group_name = var.resource_group_name
  location            = var.location
  dns_prefix          = "atpost-${var.environment}"
  sku_tier            = var.environment == "prod" ? "Standard" : "Free"

  oidc_issuer_enabled       = true
  workload_identity_enabled = true

  default_node_pool {
    name                         = "system"
    vm_size                      = var.system_vm_size
    vnet_subnet_id               = var.aks_subnet_id
    auto_scaling_enabled         = true
    min_count                    = var.system_min
    max_count                    = var.system_max
    node_labels                  = { workload = "system" }
    only_critical_addons_enabled = true
  }

  identity {
    type = "SystemAssigned"
  }

  network_profile {
    network_plugin = "azure"
    network_policy = "calico"
  }

  tags = {
    Project     = "atpost"
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# General workload pool (app services + web zones); scales independently.
resource "azurerm_kubernetes_cluster_node_pool" "general" {
  name                  = "general"
  kubernetes_cluster_id = azurerm_kubernetes_cluster.this.id
  vm_size               = var.general_vm_size
  vnet_subnet_id        = var.aks_subnet_id
  auto_scaling_enabled  = true
  min_count             = var.general_min
  max_count             = var.general_max
  node_labels           = { workload = "general" }

  tags = {
    Project     = "atpost"
    Environment = var.environment
  }
}
