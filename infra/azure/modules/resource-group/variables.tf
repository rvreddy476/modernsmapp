variable "environment" {
  type = string
}

variable "location" {
  description = "Azure region (e.g. centralindia — matches the AWS ap-south-1 residency intent)."
  type        = string
}
