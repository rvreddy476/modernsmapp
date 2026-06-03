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
