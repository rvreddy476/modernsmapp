variable "environment" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "isolated_subnet_ids" {
  description = "Isolated subnet IDs across 3 AZs (no internet route). Aurora cluster lives here."
  type        = list(string)
}

variable "eks_node_security_group_id" {
  description = "EKS node SG. Aurora ingress 5432 is restricted to this SG (no other source)."
  type        = string
}

variable "master_username" {
  description = "Aurora master username. Conventional 'postgres' or 'atpost_admin'."
  type        = string
  default     = "atpost_admin"
}

variable "instance_class" {
  description = "Aurora instance class. db.r7g.large for prod; db.t4g.medium for staging (cheaper Graviton burst tier, fine for non-prod)."
  type        = string
  default     = "db.r7g.large"
}

variable "create_reader" {
  description = "Provision a reader instance? Prod: true (failover target + SELECT load distribution). Staging: false to save cost."
  type        = bool
  default     = true
}

variable "backup_retention_days" {
  description = "Aurora automated backup retention. 7d staging, 30d prod (DPDP + PCI evidence)."
  type        = number
  default     = 7
}

variable "deletion_protection" {
  description = "Block accidental `terraform destroy`. true in prod; false in staging only."
  type        = bool
  default     = true
}

variable "apply_immediately" {
  description = "Apply parameter / class changes immediately (true) or wait for the maintenance window (false). false in prod — surprise restarts are not your friend."
  type        = bool
  default     = false
}

variable "engine_version" {
  description = "Aurora PostgreSQL engine version. 16.4 (the original pin) is deprecated by AWS; 16.15 is the newest 16.x on 3 Oct 2026 (17.11 and 18.6 exist — dev runs PostgreSQL 16, so prod stays on 16). Must match the parameter-group family (aurora-postgresql16)."
  type        = string
  default     = "16.15"
}

variable "serverless_enabled" {
  description = "Aurora Serverless v2: every instance becomes db.serverless and scales between the ACU bounds. The lean prod profile; false keeps instance_class."
  type        = bool
  default     = false
}

variable "serverless_min_acu" {
  description = "Serverless v2 minimum ACUs (0.5 is the floor; one ACU ≈ 2 GB)."
  type        = number
  default     = 0.5
}

variable "serverless_max_acu" {
  description = "Serverless v2 maximum ACUs."
  type        = number
  default     = 4
}
