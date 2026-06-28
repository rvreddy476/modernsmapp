# VNet + subnets. `aks` subnet hosts the cluster nodes; `data` subnet is
# delegated to PostgreSQL Flexible Server (Phase 2) and used for private
# endpoints (Redis). Cloud-specific networking; the app is unaffected.
#
# Subnet ranges are DERIVED from the VNet CIDR (cidrsubnet) so they always
# fall inside it regardless of the per-env VNet (staging 10.41/16, prod
# 10.42/16): aks = first /20 (4096 IPs for Azure CNI), data = a /24 at
# offset 16 (10.x.16.0/24), non-overlapping. Override via the *_subnet_cidr
# vars only if you need a specific layout.
locals {
  aks_subnet_cidr  = coalesce(var.aks_subnet_cidr, cidrsubnet(var.vnet_cidr, 4, 0))
  data_subnet_cidr = coalesce(var.data_subnet_cidr, cidrsubnet(var.vnet_cidr, 8, 16))
}

resource "azurerm_virtual_network" "this" {
  name                = "atpost-${var.environment}-vnet"
  resource_group_name = var.resource_group_name
  location            = var.location
  address_space       = [var.vnet_cidr]
}

resource "azurerm_subnet" "aks" {
  name                 = "aks"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [local.aks_subnet_cidr]
}

resource "azurerm_subnet" "data" {
  name                 = "data"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [local.data_subnet_cidr]

  # Delegation for Azure DB for PostgreSQL Flexible Server (Phase 2).
  delegation {
    name = "postgres-flexible"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}
