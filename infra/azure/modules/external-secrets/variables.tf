variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "aks_oidc_issuer_url" {
  description = "AKS OIDC issuer URL (module.aks.oidc_issuer_url) for the federated credential."
  type        = string
}

variable "key_vault_id" {
  description = "Key Vault resource ID — scope for the Secrets User role assignment."
  type        = string
}

variable "key_vault_uri" {
  description = "Key Vault vault URI — used as vaultUrl in the ClusterSecretStore."
  type        = string
}

variable "chart_version" {
  description = "external-secrets Helm chart version."
  type        = string
  default     = "0.10.4"
}
