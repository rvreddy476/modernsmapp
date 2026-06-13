variable "environment" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs across 3 AZs. MSK Serverless picks 2-3 automatically."
  type        = list(string)
}

variable "eks_node_security_group_id" {
  description = "EKS node SG. MSK 9098 (IAM SASL) ingress restricted to this SG."
  type        = string
}
