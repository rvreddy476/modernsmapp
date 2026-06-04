variable "environment" {
  type = string
}

variable "cluster_name" {
  description = "EKS cluster name. Karpenter discovers ENIs, SGs, and subnets via cluster-name tags."
  type        = string
}

variable "oidc_provider_arn" {
  description = "EKS OIDC provider ARN for IRSA."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs Karpenter may launch nodes into."
  type        = list(string)
}

variable "chart_version" {
  description = "karpenter chart version."
  type        = string
  default     = "1.0.7"
}

variable "capacity_types" {
  description = <<EOT
Capacity types Karpenter is allowed to provision. ["on-demand"] for the
strictest availability story; ["spot", "on-demand"] for cost-aware
auto-fallback (Karpenter prefers spot when bid clears, falls back to
on-demand on interruption).
EOT
  type        = list(string)
  default     = ["spot", "on-demand"]
}

variable "cpu_limit" {
  description = "NodePool ceiling — Karpenter stops adding nodes when total CPU across this pool exceeds this value."
  type        = string
  default     = "1000"
}

variable "memory_limit" {
  description = "NodePool memory ceiling."
  type        = string
  default     = "1000Gi"
}
