variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "key_vault_name" {
  description = "Globally-unique Key Vault name (3-24 chars, alphanumeric + dashes)."
  type        = string
}

variable "purge_protection_enabled" {
  description = "Block permanent deletion during the soft-delete window. Enable for prod."
  type        = bool
  default     = false
}

variable "network_default_action" {
  description = "Default network ACL action. Allow for first bring-up; tighten to Deny + private endpoint later."
  type        = string
  default     = "Allow"
}

variable "allowed_ip_rules" {
  description = "CIDRs allowed when network_default_action = Deny (e.g. CI egress, operator IPs)."
  type        = list(string)
  default     = []
}
