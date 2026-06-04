variable "environment" {
  type = string
}

variable "service_name" {
  description = "Service identifier — feeds the role name and the ServiceAccount subject claim."
  type        = string
}

variable "oidc_provider_arn" {
  description = "EKS OIDC provider ARN. From the eks module output."
  type        = string
}

variable "oidc_provider_url" {
  description = "EKS OIDC issuer URL. From the eks module output."
  type        = string
}

variable "k8s_namespace" {
  description = "Kubernetes namespace where the pod runs. Default `atpost` matches the ApplicationSet destination."
  type        = string
  default     = "atpost"
}

variable "k8s_service_account" {
  description = "ServiceAccount name. Defaults to the service name (matches the chart's _helpers.tpl naming convention)."
  type        = string
  default     = ""
}

variable "policy_arns" {
  description = "IAM policy ARNs to attach to the role. Standard set: MSK client; media-service adds the media S3 policy."
  type        = list(string)
  default     = []
}
