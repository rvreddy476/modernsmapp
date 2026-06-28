variable "environment" {
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

variable "image" {
  description = "Redis container image."
  type        = string
  default     = "redis:7-alpine"
}

variable "memory_limit" {
  type    = string
  default = "512Mi"
}
