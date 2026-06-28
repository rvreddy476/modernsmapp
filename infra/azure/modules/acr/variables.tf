variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "aks_kubelet_object_id" {
  description = "AKS kubelet identity object id (from the aks module) — granted AcrPull."
  type        = string
}
