variable "environment" {
  type = string
}

variable "key_vault_id" {
  description = "Key Vault to mirror the admin password into."
  type        = string
}

variable "chart_version" {
  description = "argo-cd Helm chart version."
  type        = string
  default     = "7.7.5"
}

variable "argocd_hostname" {
  description = "Ingress host for the ArgoCD UI (behind Front Door)."
  type        = string
  default     = ""
}

variable "allowed_source_repos" {
  description = "Git repos ArgoCD may sync from."
  type        = list(string)
  default     = ["*"]
}

variable "applicationset_manifest_path" {
  description = "Path to the Azure ApplicationSet YAML (multi-doc). Empty = skip."
  type        = string
  default     = ""
}
