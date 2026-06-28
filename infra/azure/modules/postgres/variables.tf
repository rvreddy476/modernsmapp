variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "vnet_id" {
  description = "VNet ID for the private DNS zone link."
  type        = string
}

variable "data_subnet_id" {
  description = "Delegated subnet (Microsoft.DBforPostgreSQL/flexibleServers) for VNet integration."
  type        = string
}

variable "key_vault_id" {
  description = "Key Vault to write the connection secret into."
  type        = string
}

variable "secret_name" {
  description = "Key Vault secret name (alphanumeric + dashes), e.g. atpost-staging-postgres."
  type        = string
}

variable "admin_username" {
  type    = string
  default = "atpostadmin"
}

variable "postgres_version" {
  type    = string
  default = "16"
}

variable "sku_name" {
  description = "Flexible Server SKU, e.g. GP_Standard_D2ds_v5."
  type        = string
  default     = "GP_Standard_D2ds_v5"
}

variable "storage_mb" {
  type    = number
  default = 65536
}

variable "backup_retention_days" {
  type    = number
  default = 7
}

variable "ha_enabled" {
  description = "Zone-redundant HA. Enable for prod."
  type        = bool
  default     = false
}

variable "databases" {
  description = "Logical databases to create on the server."
  type        = list(string)
  default     = []
}
