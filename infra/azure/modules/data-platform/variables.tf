variable "environment" {
  type = string
}

variable "location" {
  type = string
}

variable "zones" {
  description = "Availability zone suffixes (e.g. [\"1\",\"2\",\"3\"]) for Scylla rack spread."
  type        = list(string)
  default     = ["1", "2", "3"]
}

variable "key_vault_id" {
  description = "Key Vault to write store connection secrets into."
  type        = string
}

# ── Scylla ──
variable "scylla_operator_chart_version" {
  type    = string
  default = "1.14.0"
}
variable "scylla_version" {
  type    = string
  default = "6.0.1"
}
variable "scylla_agent_version" {
  type    = string
  default = "3.2.8"
}
variable "scylla_developer_mode" {
  description = "Relax production guards (single-node friendly). true for staging."
  type        = bool
  default     = false
}
variable "scylla_cpu_per_replica" {
  type    = string
  default = "2"
}
variable "scylla_memory_per_replica" {
  type    = string
  default = "8Gi"
}
variable "scylla_storage_per_replica" {
  type    = string
  default = "100Gi"
}
variable "scylla_secret_name" {
  type    = string
  default = "atpost-scylla"
}

# ── Redpanda ──
variable "redpanda_chart_version" {
  type    = string
  default = "5.9.5"
}
variable "redpanda_replicas" {
  type    = number
  default = 3
}
variable "redpanda_storage" {
  type    = string
  default = "100Gi"
}
variable "redpanda_cpu" {
  type    = number
  default = 1
}
variable "redpanda_memory" {
  type    = string
  default = "2Gi"
}
variable "redpanda_secret_name" {
  type    = string
  default = "atpost-redpanda"
}

# ── MinIO ──
variable "minio_chart_version" {
  type    = string
  default = "5.3.0"
}
variable "minio_mode" {
  description = "standalone | distributed."
  type        = string
  default     = "distributed"
}
variable "minio_replicas" {
  type    = number
  default = 4
}
variable "minio_root_user" {
  type    = string
  default = "atpost"
}
variable "minio_storage" {
  type    = string
  default = "100Gi"
}
variable "minio_memory" {
  type    = string
  default = "1Gi"
}
variable "minio_buckets" {
  type    = list(string)
  default = ["atpost-media", "atpost-uploads"]
}
variable "minio_secret_name" {
  type    = string
  default = "atpost-minio"
}
