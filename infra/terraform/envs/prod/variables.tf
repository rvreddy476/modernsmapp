# Prod variables. Every size knob has the LEAN first-stage default from
# docs/runbooks/aws-first-production-deployment.md §2.1 and the aws-fixes
# contract (3 Oct 2026). Growing to the full design is a tfvars change.

variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "environment" {
  type    = string
  default = "prod"
}

# ─── Names and hosts ───────────────────────────────────────────────────

variable "domain" {
  description = "Public apex domain (DNS at Cloudflare)."
  type        = string
  default     = "cleestudio.com"
}

variable "public_subdomains" {
  description = "Host labels on the public ALB certificate, besides the apex: api, ws, app, admin."
  type        = list(string)
  default     = ["api", "ws", "app", "admin"]
}

variable "media_subdomain" {
  description = "Host label for the media CDN (media.cleestudio.com)."
  type        = string
  default     = "media"
}

variable "media_custom_domain_enabled" {
  description = <<EOT
false on the FIRST apply: the us-east-1 certificate is created pending and
its validation CNAME is output for Cloudflare. Once ACM reports ISSUED,
set true and apply again — CloudFront refuses a pending certificate.
EOT
  type        = bool
  default     = false
}

variable "ops_alert_email" {
  description = "REQUIRED. Receives budget, backup, GuardDuty and SES bounce/complaint alerts (SNS sends a confirmation link first)."
  type        = string
}

variable "dmarc_report_address" {
  description = "Mailbox for DMARC aggregate reports. Null → ops_alert_email."
  type        = string
  default     = null
}

variable "ses_from_address" {
  description = "The only From address identity-auth may send as."
  type        = string
  default     = "no-reply@cleestudio.com"
}

variable "ses_configuration_set_name" {
  description = "Must match SES_CONFIGURATION_SET in deploy/services/identity-auth-service/values-prod.yaml."
  type        = string
  default     = "atpost-prod-transactional"
}

# ─── Images ─────────────────────────────────────────────────────────────

# Service list lives here in parallel with staging so a new service
# added in staging doesn't auto-create in prod without explicit
# review. Keep them in sync at PR time.
#
# 3 Oct 2026: food-service (values pending, gated off at the gateway) and
# suggestion-service (values existed with no registry or role) added so
# every deployable has an ECR repo, an IRSA role and a secret shell.
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
    "food-service",
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
    "suggestion-service",
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

# Second images a service ships beside its server image. They go through the
# same ECR module (immutable tags, scan on push, the 50-image lifecycle) and
# the CI role's push grant, which module.iam derives from the ECR ARNs.
#   media-worker: media-service's transcode worker (Dockerfile.worker: ffmpeg +
#   cmd/worker), built by build-push.yml beside the server image and deployed
#   through worker.image.tag (docs/designs/copyright-match-plan.md P-17).
variable "worker_image_names" {
  type    = list(string)
  default = ["media-worker"]
}

# Web images (contract): atpost/web = postbook-ui, atpost/admin-console =
# atpost-web-ui apps/admin. Each also gets a secret shell atpost/prod/<name>.
variable "web_image_names" {
  type    = list(string)
  default = ["web", "admin-console"]
}

variable "web_zone_names" {
  description = "ECR repo names for the retired atpost-web Multi-Zone apps. Empty in prod (W6 parks the 11 zones); list them here to recreate the repos."
  type        = list(string)
  default     = []
}

# ─── CI / state ─────────────────────────────────────────────────────────

variable "github_repos" {
  description = "Repos (org/repo) whose ANY branch may assume the CI role. Ignored when ci_github_subjects is set — which prod should always do."
  type        = list(string)
}

variable "ci_github_subjects" {
  description = <<EOT
Exact GitHub OIDC `sub` patterns allowed to assume the CI (ECR push + plan)
role. Set in prod.tfvars, e.g.
  ["repo:ORG/atpost:ref:refs/heads/main",
   "repo:ORG/atpost:ref:refs/heads/release/prod",
   "repo:ORG/atpost:pull_request"]
EOT
  type        = list(string)
  default     = []
}

variable "create_terraform_apply_role" {
  type    = bool
  default = true
}

variable "terraform_apply_github_subjects" {
  description = "Exact `sub` values that may assume the Terraform apply role. Prod: [\"repo:ORG/atpost:environment:prod\"]; the GitHub environment `prod` must restrict deployment branches to main."
  type        = list(string)
  default     = []
}

variable "tfstate_bucket_arn" {
  type = string
}

variable "tfstate_lock_table_arn" {
  type = string
}

# ─── Network ────────────────────────────────────────────────────────────

variable "single_nat_gateway" {
  description = "One NAT gateway (lean) or one per AZ (HA). A single NAT means an AZ outage removes private egress — acceptable for the pilot."
  type        = bool
  default     = true
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

# ─── EKS ────────────────────────────────────────────────────────────────

variable "eks_cluster_version" {
  description = "Kubernetes version. 1.36 = newest version every pinned add-on supports on 3 Oct 2026 (1.37 is in standard support; Karpenter's matrix stops at 1.36)."
  type        = string
  default     = "1.36"
}

variable "general_node_instance_type" {
  type    = string
  default = "m7g.xlarge"
}
variable "general_node_min" {
  type    = number
  default = 3
}
variable "general_node_max" {
  type    = number
  default = 8
}
variable "general_node_desired" {
  type    = number
  default = 3
}

variable "system_node_instance_type" {
  type    = string
  default = "m7g.medium"
}
variable "system_node_min" {
  type    = number
  default = 2
}
variable "system_node_max" {
  type    = number
  default = 3
}
variable "system_node_desired" {
  type    = number
  default = 2
}

variable "memory_node_instance_type" {
  description = "Scylla node group. m7g.large (2 vCPU, 8 GB) lean; r7g.xlarge for the full design."
  type        = string
  default     = "m7g.large"
}
variable "memory_node_min" {
  type    = number
  default = 3
}
variable "memory_node_max" {
  type    = number
  default = 3
}
variable "memory_node_desired" {
  type    = number
  default = 3
}

variable "karpenter_chart_version" {
  description = "Karpenter chart. 1.14.1 (LTS) supports Kubernetes 1.36."
  type        = string
  default     = "1.14.1"
}

variable "karpenter_capacity_types" {
  type    = list(string)
  default = ["on-demand"]
}

# ─── Scylla (per replica; must fit the memory node type) ────────────────

variable "scylla_cpu_per_replica" {
  type    = string
  default = "1500m"
}
variable "scylla_memory_per_replica" {
  description = "m7g.large has 8 GB; 5 Gi leaves room for the OS, kubelet and the agent."
  type        = string
  default     = "5Gi"
}
variable "scylla_storage_per_replica" {
  type    = string
  default = "100Gi"
}

# ─── Aurora PostgreSQL ──────────────────────────────────────────────────

variable "aurora_engine_version" {
  type    = string
  default = "16.15"
}
variable "aurora_serverless_enabled" {
  type    = bool
  default = true
}
variable "aurora_min_acu" {
  type    = number
  default = 0.5
}
variable "aurora_max_acu" {
  type    = number
  default = 4
}
variable "aurora_instance_class" {
  description = "Only used when aurora_serverless_enabled = false."
  type        = string
  default     = "db.r7g.large"
}
variable "aurora_create_reader" {
  description = "Second (reader) instance. false in the lean profile — a writer failure then means minutes of downtime, not seconds."
  type        = bool
  default     = false
}
variable "aurora_backup_retention_days" {
  description = "Aurora automated backups (point-in-time). AWS Backup keeps daily snapshots separately (ops module, 14 days)."
  type        = number
  default     = 7
}

variable "aurora_databases" {
  type    = list(string)
  default = ["app", "identity_db", "chat_db", "call_db", "commerce_db"]
}
variable "aurora_extensions" {
  type    = list(string)
  default = ["postgis", "pg_trgm", "pgcrypto"]
}

# ─── Kafka (MSK Provisioned) ───────────────────────────────────────────

variable "msk_kafka_version" {
  type    = string
  default = "3.9.x"
}
variable "msk_broker_instance_type" {
  type    = string
  default = "kafka.t3.small"
}
variable "msk_number_of_broker_nodes" {
  type    = number
  default = 2
}
variable "msk_broker_ebs_volume_size_gb" {
  type    = number
  default = 100
}
variable "msk_topic_partitions" {
  type    = number
  default = 6
}

# ─── Cache (ElastiCache Valkey) ────────────────────────────────────────

variable "elasticache_engine_version" {
  type    = string
  default = "8.1"
}
variable "elasticache_node_type" {
  type    = string
  default = "cache.t4g.small"
}
variable "elasticache_num_replicas" {
  type    = number
  default = 1
}

# ─── Search (OpenSearch) ───────────────────────────────────────────────

variable "opensearch_engine_version" {
  type    = string
  default = "OpenSearch_2.19"
}
variable "opensearch_data_instance_type" {
  type    = string
  default = "t3.small.search"
}
variable "opensearch_data_instance_count" {
  type    = number
  default = 1
}
variable "opensearch_dedicated_master_enabled" {
  type    = bool
  default = false
}
variable "opensearch_ebs_volume_size_gb" {
  type    = number
  default = 30
}

# ─── Operations ────────────────────────────────────────────────────────

variable "budget_thresholds_usd" {
  type    = list(number)
  default = [500, 1000, 1500]
}

variable "backup_retention_days" {
  type    = number
  default = 14
}

variable "cloudtrail_retention_days" {
  type    = number
  default = 90
}

variable "secret_recovery_window_days" {
  description = "Secrets Manager recovery window on the per-service secret shells."
  type        = number
  default     = 30
}
