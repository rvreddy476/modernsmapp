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
