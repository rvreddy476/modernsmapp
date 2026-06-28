variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "key_vault_id" {
  description = "Key Vault to write the connection secret into."
  type        = string
}

variable "secret_name" {
  description = "Key Vault secret name (alphanumeric + dashes), e.g. atpost-staging-redis."
  type        = string
}

variable "sku_name" {
  description = "Basic | Standard | Premium. Standard+ for HA."
  type        = string
  default     = "Standard"
}

variable "family" {
  description = "C (Basic/Standard) or P (Premium)."
  type        = string
  default     = "C"
}

variable "capacity" {
  description = "Size tier (0-6 for C family)."
  type        = number
  default     = 1
}
