variable "environment" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "isolated_subnet_ids" {
  description = "Isolated subnet IDs across 3 AZs."
  type        = list(string)
}

variable "eks_node_security_group_id" {
  description = "EKS node SG. Valkey 6379 ingress restricted to this SG."
  type        = string
}

variable "node_type" {
  description = "Cache node type. cache.t4g.medium (Graviton burst) for staging; cache.r7g.large for prod."
  type        = string
  default     = "cache.r7g.large"
}

variable "num_replicas" {
  description = "Number of replicas (in addition to the primary). Prod: 2 (one per remaining AZ); staging: 1."
  type        = number
  default     = 2
}

variable "snapshot_retention_days" {
  description = "Daily snapshot retention. 0 disables. 5d default; 1d in staging."
  type        = number
  default     = 5
}

variable "apply_immediately" {
  description = "Apply changes immediately (true) or wait for the maintenance window (false). false in prod."
  type        = bool
  default     = false
}
