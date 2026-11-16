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

# ─── Alert routing (alerting.tf) ────────────────────────────────────

variable "alert_routing_enabled" {
  description = "Give Alertmanager a receiver and routes: alerts from the atpost-service chart (label source=atpost-service) are published to alert_sns_topic_arn. A plain bool (not derived from the topic ARN) because it drives count. When true, alert_sns_topic_arn, oidc_provider_arn and oidc_provider_url are required."
  type        = bool
  default     = false
}

variable "alert_sns_topic_arn" {
  description = "SNS topic Alertmanager publishes to: the ops-baseline module's ops_alerts_topic_arn (e-mails the ops address)."
  type        = string
  default     = ""
}

variable "oidc_provider_arn" {
  description = "EKS OIDC provider ARN (eks module output). For the Alertmanager IRSA role."
  type        = string
  default     = ""
}

variable "oidc_provider_url" {
  description = "EKS OIDC issuer URL (eks module output). For the Alertmanager IRSA role."
  type        = string
  default     = ""
}

variable "aws_region" {
  description = "Region of the SNS topic (SigV4 signing region)."
  type        = string
  default     = "ap-south-1"
}

variable "route_platform_critical" {
  description = "Also e-mail kube-prometheus-stack's own severity=critical alerts. Off by default: on EKS the stack's scheduler/controller-manager/etcd rules fire permanently until those components are disabled in the stack."
  type        = bool
  default     = false
}
