variable "environment" {
  type = string
}

variable "vpc_id" {
  description = "VPC ID from the vpc module."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs across 3 AZs. EKS managed node groups + the control plane ENIs land here."
  type        = list(string)
}

variable "cluster_endpoint_public_access_cidrs" {
  description = <<EOT
CIDR blocks allowed to reach the public EKS API endpoint. Default ["0.0.0.0/0"]
is permissive — narrow to office/VPN CIDRs in prod tfvars. The cluster also
enforces IAM auth on top, so this is defence-in-depth, not the sole gate.
EOT
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention for control-plane logs. 30d default; bump to 90d for compliance evidence."
  type        = number
  default     = 30
}

variable "cluster_admin_arns" {
  description = <<EOT
Extra IAM principal ARNs that get cluster-admin via EKS access entries.
The terraform-applying principal already gets admin via
enable_cluster_creator_admin_permissions; this is for break-glass /
on-call. Format:
  ["arn:aws:iam::123456789012:role/Admin",
   "arn:aws:iam::123456789012:user/oncall-bob"]
EOT
  type        = list(string)
  default     = []
}

# ─── General node group ─────────────────────────────────────────────────
variable "general_node_min" {
  type    = number
  default = 3
}
variable "general_node_max" {
  type    = number
  default = 12
}
variable "general_node_desired" {
  type    = number
  default = 6
}

# ─── Memory node group (Scylla) ─────────────────────────────────────────
variable "memory_node_instance_type" {
  description = "Memory-tier instance type. r7g.xlarge (4 vCPU, 32 GB) is the prod default; r7g.large (2 vCPU, 16 GB) for staging."
  type        = string
  default     = "r7g.xlarge"
}
variable "memory_node_min" {
  type    = number
  default = 3
}
variable "memory_node_max" {
  type    = number
  default = 6
}
variable "memory_node_desired" {
  type    = number
  default = 3
}
