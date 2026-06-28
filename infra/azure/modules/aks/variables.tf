variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "aks_subnet_id" {
  type = string
}

variable "system_vm_size" {
  type    = string
  default = "Standard_D2s_v5"
}

variable "system_min" {
  type    = number
  default = 1
}

variable "system_max" {
  type    = number
  default = 3
}

variable "general_vm_size" {
  type    = string
  default = "Standard_D4s_v5"
}

variable "general_min" {
  type    = number
  default = 2
}

variable "general_max" {
  type    = number
  default = 10
}
