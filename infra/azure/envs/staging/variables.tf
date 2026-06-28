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
  default     = "atpost-staging-kv"
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
