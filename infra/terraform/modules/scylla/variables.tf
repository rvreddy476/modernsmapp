variable "environment" {
  type = string
}

variable "availability_zones" {
  description = "AZ names where Scylla pods land. One pod per AZ. Match the VPC's AZs."
  type        = list(string)
}

variable "operator_chart_version" {
  description = "scylla-operator Helm chart version."
  type        = string
  default     = "1.14.0"
}

variable "scylla_version" {
  description = "Scylla server image tag."
  type        = string
  default     = "6.1.1"
}

variable "scylla_agent_version" {
  description = "scylla-agent image tag — manages backups + repairs."
  type        = string
  default     = "3.3.2"
}

variable "cpu_per_replica" {
  description = "CPU request/limit per Scylla pod. Matches the memory node group's instance class (r7g.xlarge = 4 vCPU; r7g.large = 2 vCPU)."
  type        = string
  default     = "3"
}

variable "memory_per_replica" {
  description = "Memory request/limit per Scylla pod. Reserve ~2GB for OS + agent."
  type        = string
  default     = "28Gi"
}

variable "storage_per_replica" {
  description = "EBS gp3 volume size per pod. Conservative start; expand via allowVolumeExpansion later."
  type        = string
  default     = "100Gi"
}

variable "operator_replicas" {
  description = "Scylla Operator controller replicas. 2 (HA) by default; QA runs 1."
  type        = number
  default     = 2
}

# ─── Backups (backup.tf) ────────────────────────────────────────────

variable "backup_enabled" {
  description = "Install Scylla Manager and schedule the daily backup task to S3. When true, backup_bucket_name and backup_irsa_role_arn are required."
  type        = bool
  default     = false
}

variable "backup_bucket_name" {
  description = "S3 bucket Scylla Manager backs up to (envs/<env>/scylla-backup.tf)."
  type        = string
  default     = ""
}

variable "backup_irsa_role_arn" {
  description = "IAM role for ServiceAccount scylla/<cluster>-member: read/write/delete on the backup bucket."
  type        = string
  default     = ""
}

variable "backup_cron" {
  description = "When the daily backup runs, cron in UTC. 20:30 UTC = 02:00 IST."
  type        = string
  default     = "30 20 * * *"
}

variable "backup_retention" {
  description = "How many backups Scylla Manager keeps. One backup a day, so this is days."
  type        = number
  default     = 14
}

variable "aws_region" {
  description = "Region of the backup bucket (the agent's s3.region)."
  type        = string
  default     = "ap-south-1"
}
