# QA variables. Defaults are the QA size from the AWS QA contract (3 Oct
# 2026): single copies, no high availability. The QA account is QA for good;
# production gets its own account and envs/prod.

variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "environment" {
  description = "Environment name. Resource prefix atpost-qa, secrets atpost/qa/*."
  type        = string
  default     = "qa"
}

# ─── Names and hosts ───────────────────────────────────────────────────

variable "domain" {
  description = "Public parent domain (DNS at Cloudflare). QA never uses the bare domain or the prod host names."
  type        = string
  default     = "cleestudio.com"
}

variable "public_subdomains" {
  description = "Host labels on the public ALB certificate: web, API, websocket, admin. The FIRST is the certificate's primary name."
  type        = list(string)
  default     = ["qa", "api-qa", "ws-qa", "admin-qa"]
}

variable "media_subdomain" {
  description = "Host label for the media CDN (media-qa.cleestudio.com)."
  type        = string
  default     = "media-qa"
}

variable "internal_zone_name" {
  description = "Route 53 zone for the internal host names (ArgoCD, Grafana), NS-delegated from Cloudflare. Not aws.cleestudio.com: that name is kept for the production account."
  type        = string
  default     = "aws-qa.cleestudio.com"
}

variable "ingress_alb_hostnames" {
  description = <<EOT
ALB DNS names for the public hosts, keyed by host label (qa, api-qa,
ws-qa, admin-qa). Unknown until ArgoCD has created the Ingresses; fill
from `kubectl -n atpost get ingress` and apply again so the
cloudflare_dns_records output carries real targets. Empty → the host rows
say PENDING.
EOT
  type        = map(string)
  default     = {}
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
  description = "REQUIRED. The founder's address: budget ($1,000/month), backup, GuardDuty and SES bounce/complaint alerts (SNS sends a confirmation link first)."
  type        = string
}

variable "dmarc_report_address" {
  description = "Mailbox for DMARC aggregate reports. Null → ops_alert_email."
  type        = string
  default     = null
}

variable "ses_domain" {
  description = "SES domain identity. QA sends from its own subdomain so its reputation never touches the production domain."
  type        = string
  default     = "qa.cleestudio.com"
}

variable "ses_from_address" {
  description = "The only From address identity-auth may send as."
  type        = string
  default     = "no-reply@qa.cleestudio.com"
}

variable "ses_configuration_set_name" {
  description = "Must match SES_CONFIGURATION_SET in deploy/services/identity-auth-service/values-qa.yaml."
  type        = string
  default     = "atpost-qa-transactional"
}

# ─── Images ─────────────────────────────────────────────────────────────

# Same list as envs/prod/variables.tf — keep the two in sync at PR time.
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

variable "worker_image_names" {
  description = "Second images (media-worker = media-service's transcode worker)."
  type        = list(string)
  default     = ["media-worker"]
}

variable "web_image_names" {
  description = "atpost/web = postbook-ui, atpost/admin-console = atpost-web-ui apps/admin. Each also gets a secret shell atpost/qa/<name>."
  type        = list(string)
  default     = ["web", "admin-console"]
}

variable "extra_secret_shells" {
  description = "Secret shells that are not deployables: the ArgoCD repository credential deploy/argocd/repo-credentials.yaml reads (as atpost/qa/argocd-repo-modernsmapp for QA)."
  type        = list(string)
  default     = ["argocd-repo-modernsmapp"]
}

# ─── CI / state ─────────────────────────────────────────────────────────

variable "github_repository" {
  description = "owner/repo of the monorepo (CI role, Terraform apply role)."
  type        = string
  default     = "rvreddy476/modernsmapp"
}

variable "ci_github_branches" {
  description = "Branches whose workflows may assume the CI role (ECR push + plan). Pull requests are always allowed (plan only). Ignored when ci_github_subjects is set."
  type        = list(string)
  default     = ["main", "qa"]
}

variable "ci_github_subjects" {
  description = "Exact GitHub OIDC `sub` patterns for the CI role. Empty → built from github_repository + ci_github_branches + pull_request."
  type        = list(string)
  default     = []
}

variable "create_terraform_apply_role" {
  type    = bool
  default = true
}

variable "terraform_apply_github_environment" {
  description = "GitHub environment whose jobs may assume the Terraform apply role (required reviewer = founder). The sub becomes repo:<github_repository>:environment:<this>."
  type        = string
  default     = "qa"
}

variable "terraform_apply_github_subjects" {
  description = "Exact `sub` values for the apply role. Empty → [\"repo:<github_repository>:environment:<terraform_apply_github_environment>\"]."
  type        = list(string)
  default     = []
}

variable "tfstate_bucket_arn" {
  description = "REQUIRED. bootstrap/ output in the QA account: arn:aws:s3:::atpost-tfstate-<qa-account-id>."
  type        = string
}

variable "tfstate_lock_table_arn" {
  description = "REQUIRED. bootstrap/ output in the QA account: arn:aws:dynamodb:ap-south-1:<qa-account-id>:table/atpost-tfstate-locks."
  type        = string
}

# ─── Network ────────────────────────────────────────────────────────────

variable "vpc_cidr" {
  type    = string
  default = "10.40.0.0/16"
}

variable "az_count" {
  description = "Availability zones (2 = the minimum MSK and the Aurora/ElastiCache subnet groups accept)."
  type        = number
  default     = 2
}

variable "cluster_endpoint_public_access_cidrs" {
  description = "REQUIRED. CIDRs allowed to reach the EKS public API endpoint (the founder's and the lead's addresses). Never 0.0.0.0/0."
  type        = list(string)
}

variable "cluster_admin_arns" {
  description = "IAM principals with cluster-admin via EKS access entries (break-glass). The principal that runs pass 1 is admin already."
  type        = list(string)
  default     = []
}

# ─── EKS ────────────────────────────────────────────────────────────────

variable "eks_cluster_version" {
  description = "Same as prod: 1.36 (newest version every pinned add-on supports on 3 Oct 2026)."
  type        = string
  default     = "1.36"
}

variable "general_node_instance_type" {
  type    = string
  default = "m7g.xlarge"
}
variable "general_node_min" {
  type    = number
  default = 2
}
variable "general_node_max" {
  type    = number
  default = 4
}
variable "general_node_desired" {
  type    = number
  default = 2
}

variable "system_node_instance_type" {
  description = "m7g.medium (1 vCPU, 4 GB, no CPU credits). t4g.medium (2 vCPU burstable) is the cheaper alternative the contract allows."
  type        = string
  default     = "m7g.medium"
}
variable "system_node_min" {
  type    = number
  default = 2
}
variable "system_node_max" {
  type    = number
  default = 2
}
variable "system_node_desired" {
  type    = number
  default = 2
}

variable "memory_node_instance_type" {
  description = "Scylla node. m7g.large (2 vCPU, 8 GB)."
  type        = string
  default     = "m7g.large"
}

variable "scylla_node_count" {
  description = "Scylla nodes = memory nodes = racks, one per AZ from the first. QA: 1 (keyspaces RF 1 — the values lane's QA schema variant)."
  type        = number
  default     = 1

  validation {
    condition     = var.scylla_node_count >= 1 && var.scylla_node_count <= 2
    error_message = "scylla_node_count must be 1 or 2 (one per AZ; QA has 2 AZs)."
  }
}

variable "karpenter_chart_version" {
  type    = string
  default = "1.14.1"
}

variable "karpenter_capacity_types" {
  type    = list(string)
  default = ["on-demand"]
}

variable "karpenter_cpu_limit" {
  description = "Ceiling on the vCPUs Karpenter may add on top of the managed groups (16 = four m7g.xlarge). It is the cost brake: raise only on purpose."
  type        = string
  default     = "16"
}

variable "karpenter_memory_limit" {
  type    = string
  default = "64Gi"
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
  default = "50Gi"
}

# ─── Aurora PostgreSQL ──────────────────────────────────────────────────

variable "aurora_engine_version" {
  type    = string
  default = "16.15"
}
variable "aurora_min_acu" {
  type    = number
  default = 0.5
}
variable "aurora_max_acu" {
  type    = number
  default = 2
}
variable "aurora_backup_retention_days" {
  type    = number
  default = 3
}

variable "aurora_databases" {
  description = "Same databases as prod."
  type        = list(string)
  default     = ["app", "identity_db", "chat_db", "call_db", "commerce_db"]
}
variable "aurora_extensions" {
  description = "Same extensions as prod."
  type        = list(string)
  default     = ["postgis", "pg_trgm", "pgcrypto"]
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
  description = "2 = the minimum for two AZs."
  type        = number
  default     = 2
}
variable "msk_broker_ebs_volume_size_gb" {
  type    = number
  default = 50
}
variable "msk_topic_partitions" {
  type    = number
  default = 3
}
variable "msk_topic_replication_factor" {
  type    = number
  default = 2
}
variable "msk_min_insync_replicas" {
  type    = number
  default = 1
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
  description = "0 = one node, no failover (the module then turns Multi-AZ and automatic failover off)."
  type        = number
  default     = 0
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
variable "opensearch_ebs_volume_size_gb" {
  type    = number
  default = 20
}

# ─── Operations ────────────────────────────────────────────────────────

variable "budget_thresholds_usd" {
  description = "Monthly ACTUAL-spend alerts to ops_alert_email. The largest ($1,000) is the budget limit; a 100% FORECAST alert comes with it."
  type        = list(number)
  default     = [500, 1000]
}

variable "backup_retention_days" {
  description = "AWS Backup daily Aurora snapshots."
  type        = number
  default     = 7
}

variable "cloudtrail_retention_days" {
  type    = number
  default = 30
}

variable "secret_recovery_window_days" {
  description = "Secrets Manager recovery window on the per-service secret shells (7 = the minimum)."
  type        = number
  default     = 7
}
