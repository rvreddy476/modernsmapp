variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "environment" {
  type    = string
  default = "staging"
}

# Service list. One entry per Go service that gets shipped as a
# container. Keep alphabetised so a missed addition is obvious in the
# diff. Web frontend (postbook-ui) lives in its own repo + ECR account.
variable "service_names" {
  type = list(string)
  default = [
    # Architecture/ services
    "admin-service",
    "ai-service",
    "analytics-service",
    "api-gateway",
    "bill-pay-service",
    "channel-service",
    "commerce-service",
    "community-service",
    "dating-service",
    "feed-service",
    "graph-service",
    "group-service",
    "live-service-v2",
    "media-service",
    "monetization-service",
    "notification-service",
    "payments-service",
    "post-service",
    "qa-service",
    "rider-service",
    "search-service",
    "trust-safety-service",
    "user-service",
    "wallet-service",
    # identity-platform
    "identity-auth-service",
    "identity-profile-service",
    "identity-user-service",
    # chat-service
    "chat-call-service",
    "chat-message-service",
    "chat-ws-gateway",
  ]
}

variable "github_repos" {
  description = "GitHub repos allowed to assume the CI role."
  type        = list(string)
  default     = ["TODO/atpost"] # TODO: replace with the real org/repo once known
}

variable "web_zone_names" {
  description = "ECR repo names for the atpost-web Multi-Zone apps (atpost/<name>)."
  type        = list(string)
  default = [
    "web-shell", "web-commerce", "web-admin", "web-dating", "web-social",
    "web-community", "web-creator", "web-messenger", "web-live",
    "web-memories", "web-miniapps",
  ]
}

variable "tfstate_bucket_arn" {
  description = "Output from `bootstrap/`. Fill in after `terraform apply` in bootstrap/."
  type        = string
}

variable "tfstate_lock_table_arn" {
  description = "Output from `bootstrap/`. Fill in after `terraform apply` in bootstrap/."
  type        = string
}

variable "cluster_admin_arns" {
  description = "IAM principals to grant cluster-admin via EKS access entries. Empty by default; populate in staging.tfvars for break-glass / on-call."
  type        = list(string)
  default     = []
}
