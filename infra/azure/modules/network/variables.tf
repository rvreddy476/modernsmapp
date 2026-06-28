variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "vnet_cidr" {
  type    = string
  default = "10.40.0.0/16"
}

variable "aks_subnet_cidr" {
  description = "Override the AKS subnet CIDR. Empty = derived from vnet_cidr (first /20)."
  type        = string
  default     = ""
}

variable "data_subnet_cidr" {
  description = "Override the data subnet CIDR. Empty = derived from vnet_cidr (/24 at offset 16)."
  type        = string
  default     = ""
}
