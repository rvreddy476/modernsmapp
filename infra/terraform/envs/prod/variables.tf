variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "environment" {
  type    = string
  default = "prod"
}

# Service list lives here in parallel with staging so a new service
# added in staging doesn't auto-create in prod without explicit
# review. Keep them in sync at PR time.
variable "service_names" {
  type = list(string)
  default = [
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
    "identity-auth-service",
    "identity-profile-service",
    "identity-user-service",
    "chat-call-service",
    "chat-message-service",
    "chat-ws-gateway",
  ]
}

variable "github_repos" {
  description = <<EOT
Repos allowed to assume the prod CI role. Should narrow to main only:
  ["org/atpost:ref:refs/heads/main"]
... once the IAM module is taught the `ref:` suffix. Today the module
allows any branch — fine for staging, NOT fine for prod-apply. Tighten
before the first prod cutover.
EOT
  type        = list(string)
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
  type = string
}

variable "tfstate_lock_table_arn" {
  type = string
}

variable "cluster_endpoint_public_access_cidrs" {
  description = <<EOT
CIDRs allowed to reach the EKS public API endpoint. MUST be set in
prod.tfvars — leaving default 0.0.0.0/0 lights up the API to the
internet. Office IPs + VPN + ops on-call addresses; tighten further
as the team's network shape solidifies.
EOT
  type        = list(string)
}

variable "cluster_admin_arns" {
  description = "IAM principals to grant cluster-admin via EKS access entries. Break-glass + on-call only — the CI role doesn't need admin."
  type        = list(string)
  default     = []
}
