variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "sku_name" {
  description = "Standard_AzureFrontDoor (no managed WAF) or Premium_AzureFrontDoor (managed rule sets)."
  type        = string
  default     = "Premium_AzureFrontDoor"
}

variable "origin_host_name" {
  description = "nginx ingress public IP / DNS name (operator-supplied once the AKS LoadBalancer is allocated)."
  type        = string
}

variable "origin_host_header" {
  description = "Host header sent to the origin. Empty = use origin_host_name."
  type        = string
  default     = ""
}

variable "waf_mode" {
  description = "Prevention (enforce) or Detection (log only — use first to catch false positives)."
  type        = string
  default     = "Prevention"
}

variable "rate_limit_threshold" {
  description = "Requests per minute per IP before the rate-limit rule blocks."
  type        = number
  default     = 1000
}
