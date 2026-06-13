variable "environment" {
  type = string
}

variable "aws_region" {
  type = string
}

variable "namespace" {
  description = "Kubernetes namespace to install into. Re-use the observability namespace so Grafana finds the datasource ConfigMap."
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
  description = "tempo Helm chart version."
  type        = string
  default     = "1.17.2"
}

variable "retention_days" {
  description = "Trace retention in days. 7d default; bump to 30d once we've sized the bucket cost."
  type        = number
  default     = 7
}

variable "random_suffix" {
  description = "8-char hex suffix on the S3 bucket name. Provide a stable value (e.g. the value from the media module's random_id) so apply doesn't churn."
  type        = string
}
