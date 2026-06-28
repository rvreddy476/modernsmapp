variable "environment" {
  type = string
}

variable "chart_version" {
  description = "ingress-nginx Helm chart version."
  type        = string
  default     = "4.11.3"
}

variable "replica_count" {
  type    = number
  default = 2
}
