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

# ─── Versions and instance shapes ───────────────────────────────────────
variable "cluster_version" {
  description = <<EOT
Kubernetes version. Default stays at the original 1.31 for staging; prod
passes the newest version that every pinned add-on supports (1.36 on
3 Oct 2026 — 1.37 is in standard support but Karpenter's compatibility
matrix stops at 1.36). Check https://docs.aws.amazon.com/eks/latest/userguide/kubernetes-versions-standard.html
and https://karpenter.sh/docs/upgrading/compatibility/ before bumping.
EOT
  type        = string
  default     = "1.31"
}

variable "general_node_instance_type" {
  description = "General node group instance type. m7g.large default; m7g.xlarge (4 vCPU, 16 GB) in the prod lean profile."
  type        = string
  default     = "m7g.large"
}

variable "system_node_instance_type" {
  description = "System node group instance type (ingress, ArgoCD, ESO, Karpenter controller)."
  type        = string
  default     = "m7g.medium"
}

variable "system_node_min" {
  type    = number
  default = 3
}
variable "system_node_max" {
  type    = number
  default = 6
}
variable "system_node_desired" {
  type    = number
  default = 3
}

variable "memory_node_subnet_ids" {
  description = "Subnets for the memory (Scylla) node group. Null = the cluster's private subnets (prod/staging). QA passes the one subnet of its single Scylla rack."
  type        = list(string)
  default     = null
}
