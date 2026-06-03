variable "environment" {
  type = string
}

variable "aws_region" {
  type = string
}

variable "oidc_provider_arn" {
  description = "EKS cluster OIDC provider ARN. From the eks module output."
  type        = string
}

variable "oidc_provider_url" {
  description = "EKS cluster OIDC issuer URL. From the eks module output."
  type        = string
}

variable "kms_key_arns" {
  description = "KMS key ARNs ESO needs Decrypt on — Aurora, ElastiCache, OpenSearch, and any future Secrets Manager-backed store."
  type        = list(string)
}

variable "chart_version" {
  description = "external-secrets Helm chart version. Bump after reading the project's CHANGELOG for CRD migrations."
  type        = string
  default     = "0.10.4"
}
