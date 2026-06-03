variable "environment" {
  description = "Environment name (staging, prod) — feeds into resource names + tags."
  type        = string
}

variable "aws_region" {
  description = "AWS region. Inherited from the env so VPC endpoints land in the right service namespace."
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR block. /16 gives 65k IPs — three /20 publics + three /20 privates + three /22 isolateds + headroom."
  type        = string
  default     = "10.10.0.0/16"
}

variable "single_nat_gateway" {
  description = "Use one shared NAT gateway across all AZs (true) or one per AZ (false, prod). One-per-AZ is the HA shape; shared is cost-only for non-prod."
  type        = bool
  default     = false
}
