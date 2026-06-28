variable "subscription_id" {
  type = string
}

variable "location" {
  type    = string
  default = "centralindia"
}

variable "environment" {
  type    = string
  default = "staging"
}

variable "github_subjects" {
  description = "GitHub OIDC subjects allowed to assume the CI identity."
  type        = list(string)
  default = [
    "repo:rvreddy476/modernsmapp:ref:refs/heads/main",
    "repo:rvreddy476/modernsmapp:environment:staging",
  ]
}

variable "key_vault_name" {
  description = "Globally-unique Key Vault name (3-24 chars). Override if the default is taken."
  type        = string
  default     = "atpost-staging-454350"
}

variable "registry_name" {
  description = "Globally-unique ACR name (alphanumeric, 5-50)."
  type        = string
  default     = "atpoststaging454350"
}

variable "argocd_hostname" {
  description = "Ingress host for the ArgoCD UI (behind Front Door)."
  type        = string
  default     = ""
}

variable "postgres_databases" {
  description = "Logical databases to create on the Flexible Server (one per data-owning service)."
  type        = list(string)
  default = [
    "user_service",
    "post_service",
    "feed_service",
    "media_service",
    "notification_service",
    "search_service",
    "group_service",
    "trust_safety_service",
    "analytics_service",
    "admin_service",
    "monetization_service",
    "payments_service",
    "channel_service",
    "community_service",
    "qa_service",
    "commerce_service",
    "dating_service",
    "wallet_service",
    "bill_pay_service",
    "rider_service",
    "ai_service",
    "identity_profile_service",
    "graph_service",
  ]
}

variable "enable_edge" {
  description = "Provision Front Door + WAF + DNS. Enable after the nginx LB IP + DNS zone exist."
  type        = bool
  default     = false
}

variable "edge_origin_host_name" {
  description = "nginx ingress public IP / DNS name (Front Door origin). Required when enable_edge."
  type        = string
  default     = ""
}

variable "edge_frontdoor_sku" {
  description = "Standard_AzureFrontDoor or Premium_AzureFrontDoor (managed WAF rules)."
  type        = string
  default     = "Premium_AzureFrontDoor"
}

variable "edge_zone_name" {
  description = "Public DNS zone, e.g. azure.cleestudio.com. Required when enable_edge."
  type        = string
  default     = ""
}

variable "edge_cname_records" {
  description = "subdomain → Front Door endpoint host. e.g. { api = \"atpost-staging-xxxx.z01.azurefd.net\" }"
  type        = map(string)
  default     = {}
}

# AKS node sizing — overridable so you can switch to a VM family you have
# quota for (see scripts/azure-check-quota.sh). Staging defaults are small.
variable "system_vm_size" {
  type    = string
  default = "Standard_D2s_v5"
}
variable "general_vm_size" {
  type    = string
  default = "Standard_D4s_v5"
}
variable "system_min" {
  type    = number
  default = 1
}
variable "system_max" {
  type    = number
  default = 2
}
variable "general_min" {
  type    = number
  default = 1
}
variable "general_max" {
  type    = number
  default = 4
}
