variable "environment" {
  type = string
}

variable "aws_region" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "cluster_name" {
  description = "EKS cluster name. Required because the controller tags AWS resources with the cluster's identifier."
  type        = string
}

variable "oidc_provider_arn" {
  description = "EKS OIDC provider ARN."
  type        = string
}

variable "chart_version" {
  description = "aws-load-balancer-controller Helm chart version. Bump after checking the eks-charts CHANGELOG for IAM policy diffs."
  type        = string
  default     = "1.9.2"
}
