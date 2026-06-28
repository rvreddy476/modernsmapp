variable "environment" {
  type = string
}

variable "crds_chart_version" {
  description = "Pin the prometheus-operator-crds chart version. Empty = latest."
  type        = string
  default     = ""
}
