output "endpoint_host_name" {
  description = "Default Front Door endpoint hostname (CNAME target for your DNS)."
  value       = azurerm_cdn_frontdoor_endpoint.this.host_name
}

output "profile_id" {
  value = azurerm_cdn_frontdoor_profile.this.id
}

output "firewall_policy_id" {
  value = azurerm_cdn_frontdoor_firewall_policy.this.id
}
