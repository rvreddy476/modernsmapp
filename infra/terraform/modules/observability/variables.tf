variable "environment" {
  type = string
}

variable "chart_version" {
  description = "kube-prometheus-stack chart version."
  type        = string
  default     = "65.8.1"
}

variable "prometheus_replicas" {
  type    = number
  default = 2
}

variable "prometheus_retention" {
  description = "Prometheus on-disk retention. 15d default; bump for long backfill windows."
  type        = string
  default     = "15d"
}

variable "prometheus_retention_size" {
  description = "Soft retention size — Prometheus deletes oldest blocks when storage exceeds this. Should be ~80% of the PVC size."
  type        = string
  default     = "70GB"
}

variable "prometheus_storage_size" {
  description = "Per-replica PVC size. Sized for the retention above."
  type        = string
  default     = "100Gi"
}

variable "alertmanager_replicas" {
  type    = number
  default = 2
}

variable "grafana_hostname" {
  description = "Hostname for the Grafana UI. Example: grafana.staging.aws.cleestudio.com."
  type        = string
}

variable "acm_certificate_arn" {
  description = "ACM wildcard cert ARN. From the dns module output."
  type        = string
}

variable "grafana_ingress_scheme" {
  description = "ALB scheme. internet-facing in staging, internal in prod."
  type        = string
  default     = "internal"
}
