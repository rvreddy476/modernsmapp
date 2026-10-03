variable "environment" {
  type = string
}

variable "purpose" {
  description = "Short bucket purpose used in the name: live-recordings, commerce-invoices, food-files."
  type        = string
}

variable "service_tag" {
  description = "Service tag value (cost allocation)."
  type        = string
}

variable "versioning" {
  description = "Enable object versioning (invoices: yes, legal record; recordings/food: no)."
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "Customer-managed KMS key ARN for SSE-KMS. Null → SSE-S3 (AES256), which external writers such as LiveKit egress can use without key grants."
  type        = string
  default     = null
}

variable "noncurrent_version_expiration_days" {
  description = "When versioning is on: days after which noncurrent versions expire."
  type        = number
  default     = 90
}

variable "transition_to_ia_after_days" {
  description = "Move current objects to STANDARD_IA after N days. Null disables. Minimum 30."
  type        = number
  default     = null
}

variable "expire_after_days" {
  description = "Delete current objects after N days. Null keeps them forever."
  type        = number
  default     = null
}
