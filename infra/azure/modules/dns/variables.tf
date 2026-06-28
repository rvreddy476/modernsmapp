variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "zone_name" {
  description = "DNS zone, e.g. azure.cleestudio.com."
  type        = string
}

variable "cname_records" {
  description = "Map of subdomain label → CNAME target (Front Door endpoint host). e.g. { api = \"atpost-staging-xxxx.z01.azurefd.net\" }"
  type        = map(string)
  default     = {}
}

variable "ttl" {
  type    = number
  default = 300
}
