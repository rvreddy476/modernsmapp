output "zone_name" {
  value = azurerm_dns_zone.this.name
}

output "name_servers" {
  description = "Delegate these from your registrar / parent zone."
  value       = azurerm_dns_zone.this.name_servers
}
