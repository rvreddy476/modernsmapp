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

variable "tfstate_bucket_arn" {
  type = string
}

variable "tfstate_lock_table_arn" {
  type = string
}
