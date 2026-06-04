variable "environment" {
  type = string
}

variable "aws_region" {
  type = string
}

variable "namespace" {
  description = "Re-use the observability namespace so the Grafana sidecar finds the datasource ConfigMap."
  type        = string
  default     = "observability"
}

variable "oidc_provider_arn" {
  type = string
}

variable "oidc_provider_url" {
  type = string
}

variable "chart_version" {
  description = "loki Helm chart version."
  type        = string
  default     = "6.18.0"
}

variable "alloy_chart_version" {
  description = "alloy Helm chart version (the log-shipper DaemonSet)."
  type        = string
  default     = "0.9.2"
}

variable "retention_days" {
  description = "Log retention in days. 14d default; bump for compliance if needed."
  type        = number
  default     = 14
}

variable "random_suffix" {
  description = "8-char hex suffix on the S3 bucket name. Reuse a stable value across applies (e.g. the media module's)."
  type        = string
}
